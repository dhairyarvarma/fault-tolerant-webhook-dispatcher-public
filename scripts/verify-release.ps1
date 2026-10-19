#Requires -Version 7.0
[CmdletBinding()]
param(
    [switch]$Load,
    [switch]$Profile,
    [ValidateRange(120,3600)][int]$SustainedSeconds=120,
    [switch]$KeepStack
)
if ($Profile) { $Load=$true }

$ErrorActionPreference = 'Stop'
$repo = Split-Path $PSScriptRoot -Parent
Set-Location $repo
$run = 'verify-' + [DateTime]::UtcNow.ToString('yyyyMMddHHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0,8)
$env:VERIFY_RUN_ID = $run
$compose = @('compose','--env-file','deploy/verification.env','-p',$run,'-f','deploy/compose.verification.yml')
$artifact = Join-Path $repo "reports/generated/$run"
New-Item -ItemType Directory -Path $artifact | Out-Null
$checks = [System.Collections.Generic.List[object]]::new()
$headers = @{'X-API-Key'='verification-only'}
$report = @{run=$run; started_at=[DateTime]::UtcNow; checks=$checks; status='running'}
$sampler = $null
$success = $false
function Docker {
    $output = & docker.exe @args
    if ($LASTEXITCODE -ne 0) { throw "Docker command failed: $($args[0])" }
    $output
}
function Compose { Docker @compose @args }
function Check([string]$name, $detail) {
    $checks.Add(@{name=$name; at=[DateTime]::UtcNow; detail=$detail})
    Write-Host "PASS: $name"
}
function Address([string]$service, [int]$port) {
    'http://' + ((Compose port $service $port) | Select-Object -Last 1).Trim()
}
function Wait-HTTP([string]$url, [scriptblock]$accept, [int]$seconds=90) {
    $end = [DateTime]::UtcNow.AddSeconds($seconds)
    do {
        try {
            $value = Invoke-RestMethod $url -Headers $headers -TimeoutSec 3
            if (& $accept $value) { return $value }
        } catch { }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $end)
    throw "Timed out waiting for $url"
}
function API([string]$base, [string]$method, [string]$path, $body, [string]$key='') {
    $h = @{'X-API-Key'='verification-only'}
    if ($key) { $h['Idempotency-Key']=$key }
    $params = @{Uri=$base+$path; Method=$method; Headers=$h; TimeoutSec=10}
    if ($null -ne $body) { $params.Body=($body | ConvertTo-Json -Depth 10 -Compress); $params.ContentType='application/json' }
    Invoke-RestMethod @params
}
function Target([string]$base, [string]$path, [string]$secret='') {
    (API $base POST '/api/v1/targets' @{name='verification';url="http://target-simulator:9090$path";secret=$secret}).target_id
}
function Event([string]$base, [string]$target) {
    (API $base POST "/api/v1/targets/$target/events" @{payload=@{type='verification'}} ([guid]::NewGuid().ToString())).event_id
}
function Succeeded([string]$base, [string]$event, [int]$seconds=30) {
    Wait-HTTP "$base/api/v1/events/$event" {param($v) $v.status -eq 'SUCCEEDED'} $seconds
}
function SQL([string]$db, [string]$query) {
    ((Compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U webhook -d $db -Atc $query) -join "`n").Trim()
}
try {
    Start-Transcript -Path (Join-Path $artifact 'transcript.txt') | Out-Null
    $report.commit = (git -c "safe.directory=$repo" rev-parse HEAD).Trim()
    $report.dirty = [bool](git -c "safe.directory=$repo" status --porcelain)
    $report.docker = Docker version --format '{{.Server.Version}}'
    $report.docker_host = (Docker info --format '{"cpus":{{.NCPU}},"memory_bytes":{{.MemTotal}},"os":"{{.OperatingSystem}}"}') | ConvertFrom-Json
    $report.cpu = @(Get-CimInstance Win32_Processor | Select-Object Name,NumberOfCores,NumberOfLogicalProcessors)
    $report.host_os = [System.Runtime.InteropServices.RuntimeInformation]::OSDescription
    $report.go = go version
    Compose config --quiet
    Compose build
    Compose up -d --wait --wait-timeout 120
    $base = Address api 8080
    $null = Wait-HTTP "$base/readyz" { $true }
    $uid = Compose exec -T api id -u
    if ($uid.Trim() -eq '0') { throw 'API runs as root' }
    Compose exec -T api test -s /etc/ssl/certs/ca-certificates.crt
    $report.images = @(Compose images --format json)
    Check 'container startup, readiness, non-root and CA bundle' @{api=$base;uid=$uid}

    $env:TEST_BASE_URL=$base; $env:TEST_TARGET_URL='http://target-simulator:9090'
    $env:TEST_API_KEY='verification-only'; $env:TEST_SHOWCASE='1'
    Remove-Item Env:TEST_SIGNING_TARGET_URL -ErrorAction SilentlyContinue
    go test -count=1 -v ./test/e2e 2>&1 | Tee-Object -FilePath (Join-Path $artifact 'http-acceptance.txt')
    if ($LASTEXITCODE -ne 0) { throw 'Container HTTP acceptance failed' }
    Check 'container HTTP acceptance and narrated showcase' 'Signing receiver test is native-only; skipped here.'
    $null = Wait-HTTP ((Address prometheus 9090)+'/api/v1/query?query=up') {
        param($v) @($v.data.result | Where-Object { $_.metric.job -eq 'webhook-dispatcher' -and $_.value[1] -eq '1' }).Count -eq 1
    }
    $grafana = Address grafana 3000
    $null = Wait-HTTP "$grafana/api/health" {param($v) $v.database -eq 'ok'}
    $auth=@{Authorization='Basic '+[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes('admin:verification-only'))}
    $dashboards=Invoke-RestMethod "$grafana/api/search?type=dash-db" -Headers $auth
    if (@($dashboards | Where-Object title -eq 'Webhook Dispatcher').Count -ne 1) { throw 'Provisioned dashboard missing' }
    Check 'live Prometheus scrape and Grafana dashboard provisioning' $dashboards

    $target = Target $base '/sim/status/200' 'synthetic-restore-signing-key'
    $event = Event $base $target
    $null = Succeeded $base $event
    Compose stop api
    $fingerprint = "SELECT json_build_object(" + ((@('targets','events','delivery_attempts','outbox_messages','schema_migrations') | ForEach-Object {
        $order = if ($_ -eq 'schema_migrations') {'version'} else {'id'}
        "'$_',(SELECT md5(COALESCE(string_agg(to_jsonb(t)::text,'' ORDER BY $order),'')) FROM $_ t)"
    }) -join ',') + ");"
    $source = SQL webhook $fingerprint
    Compose exec -T postgres pg_dump -U webhook -d webhook -Fc -f /tmp/verification.dump
    Compose exec -T postgres createdb -U webhook verification_restore
    Compose exec -T postgres pg_restore --exit-on-error -U webhook -d verification_restore /tmp/verification.dump
    $restored = SQL verification_restore $fingerprint
    if ($source -ne $restored) { throw 'Restored table fingerprints differ' }
    Compose up -d restored-api
    $restoreBase = Address restored-api 8080
    $null = Wait-HTTP "$restoreBase/readyz" { $true }
    $null = Succeeded $restoreBase $event
    $null = Succeeded $restoreBase (Event $restoreBase $target)
    Check 'backup/restore preserves all five table fingerprints and signing-key usability' @{source=($source|ConvertFrom-Json);restored=($restored|ConvertFrom-Json);event=$event}
    Compose stop restored-api
    Compose start api
    $base = Address api 8080
    $null = Wait-HTTP "$base/readyz" { $true }

    Compose stop redis
    $queued = Event $base $target
    $watch=[Diagnostics.Stopwatch]::StartNew()
    Compose start redis
    $null = Succeeded $base $queued 45
    Check 'Redis restart: accepted durable work eventually succeeds' @{event=$queued;restart_to_success_seconds=$watch.Elapsed.TotalSeconds}
    $slow = Target $base '/sim/latency/2000/200'
    $leased = Event $base $slow
    $deadline=[DateTime]::UtcNow.AddSeconds(10)
    do {
        $hasLease=SQL webhook "SELECT delivery_token IS NOT NULL FROM events WHERE id='$leased';"
        if ($hasLease -eq 't') { break }
        Start-Sleep -Milliseconds 50
    } while ([DateTime]::UtcNow -lt $deadline)
    if ($hasLease -ne 't') { throw 'Did not observe active delivery claim' }
    $watch=[Diagnostics.Stopwatch]::StartNew()
    Compose kill -s SIGKILL api
    Compose start api
    $base = Address api 8080
    $null = Succeeded $base $leased 120
    Check 'SIGKILL after observed claim: durable event recovered' @{event=$leased;kill_command_to_success_seconds=$watch.Elapsed.TotalSeconds;definition='Includes restart and polling; proves claim recovery, not exact HTTP-success crash timing.'}

    if ($Load) {
        if (-not (Get-Command k6 -ErrorAction SilentlyContinue)) { throw 'Install native k6 or add .tmp/tools to PATH' }
        $env:API_KEY='verification-only'; $env:K6_NO_USAGE_REPORT='true'
        $env:PRE_ALLOCATED_VUS='50'; $env:MAX_VUS='100'
        $report.load_results=[System.Collections.Generic.List[object]]::new()
        $pg=(Compose port postgres 5432).Trim()
        $env:TEST_DATABASE_URL="postgres://webhook:verification-only@$pg/webhook?sslmode=disable"
        $benchmark=Join-Path $artifact 'benchmark.exe'
        go build -buildvcs=false -o $benchmark ./cmd/benchmark
        if ($LASTEXITCODE -ne 0) { throw 'Benchmark runner build failed' }
        $ids=@(Compose ps -q api postgres redis target-simulator)
        $sampler=Start-Job -ArgumentList $ids,(Join-Path $artifact 'resources.jsonl') -ScriptBlock {
            param($containers,$path)
            while ($true) {
                $rows=& docker stats --no-stream --format '{{json .}}' @containers
                @{at=[DateTime]::UtcNow;exit_code=$LASTEXITCODE;containers=@($rows)} | ConvertTo-Json -Compress | Add-Content $path
                Start-Sleep -Seconds 2
            }
        }
        $scenarios=@(
            @{scenario='ingestion';rate=25;duration='30s';path='/sim/status/200';behavior='HTTP 200, zero injected delay'},
            @{scenario='soak';rate=25;duration="${SustainedSeconds}s";path='/sim/status/200';behavior="HTTP 200, zero injected delay; ${SustainedSeconds}-second sustained check"},
            @{scenario='ingestion';rate=20;duration='20s';path='/sim/latency/500/200';behavior='HTTP 200, 500ms injected delay; 4 workers'}
        )
        if ($Profile) { $scenarios=@($scenarios[0]) }
        foreach ($test in $scenarios) {
            $destination=Target $base $test.path
            $sessionsBefore=[long](SQL webhook "SELECT sessions FROM pg_stat_database WHERE datname='webhook';")
            & $benchmark -scenario $test.scenario -target-id $destination -base-url $base -rate $test.rate -duration $test.duration -workers 4 -api-commit $report.commit -machine 'Docker Desktop Linux VM; API 2 CPU/512MiB, PostgreSQL 1 CPU/512MiB, Redis 0.5 CPU/128MiB, simulator 1 CPU/128MiB; shared Windows host' -target-behavior $test.behavior -output (Join-Path $artifact 'load')
            $code=$LASTEXITCODE
            $sessionsAfter=[long](SQL webhook "SELECT sessions FROM pg_stat_database WHERE datname='webhook';")
            $report.load_results.Add(@{scenario=$test.scenario;rate=$test.rate;duration=$test.duration;behavior=$test.behavior;exit_code=$code;database_sessions_before=$sessionsBefore;database_sessions_after=$sessionsAfter;database_sessions_added=$sessionsAfter-$sessionsBefore})
            if ($code -ne 0) { Write-Warning "Load or delivery verification exited $code; retaining evidence and continuing remaining scenarios." }
        }
        Check 'load runs executed' 'See load_results (including database-session deltas), load/* and resources.jsonl; thresholds and delivery totals must also be inspected.'
    }
    $report.status='passed'
    if ($Load -and @($report.load_results | Where-Object exit_code -ne 0).Count) { $report.status='load_failed' }
    $success=$true
} finally {
    if ($sampler) { Stop-Job $sampler; Receive-Job $sampler; Remove-Job $sampler }
    $report.finished_at=[DateTime]::UtcNow
    if (-not $success) { $report.status='failed' }
    $report | ConvertTo-Json -Depth 15 | Set-Content (Join-Path $artifact 'release.json')
    try { Compose logs --no-color | Set-Content (Join-Path $artifact 'containers.log') } catch { Write-Warning $_ }
    if (-not ($KeepStack -and $success)) {
        # Only this invocation's unique project and disposable volumes are removed.
        Compose --profile restore down --volumes --remove-orphans
    }
    Stop-Transcript | Out-Null
    Write-Host "Evidence: $artifact"
}
if ($report.status -ne 'passed') { throw 'One or more load or delivery checks failed; all available evidence was retained.' }
