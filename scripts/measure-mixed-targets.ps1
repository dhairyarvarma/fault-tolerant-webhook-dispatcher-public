#Requires -Version 7.0
[CmdletBinding()]
param([ValidateRange(5,120)][int]$Seconds=30, [ValidateRange(0,4)][int]$TargetConcurrency=0, [switch]$SelfTest)
$ErrorActionPreference='Stop'

function Assert-Outcome($meta,$delivery,[int]$code,[bool]$failing) {
    if (-not $meta.load_succeeded -or $meta.delivery_expected -le 0 -or $delivery.accepted -ne $meta.delivery_expected) { throw 'Load/count verification failed' }
    if ($failing) {
        if ($code -eq 0 -or $meta.status -ne 'verification_failed' -or $meta.delivery_verification -notin @('drain_timeout','terminal_failures') -or $delivery.succeeded -ne 0 -or $delivery.canceled -ne 0 -or ($delivery.pending+$delivery.dlq) -ne $delivery.accepted) { throw 'Unexpected failing-target outcome' }
    } elseif ($code -ne 0 -or $meta.status -ne 'completed' -or $meta.delivery_verification -ne 'verified' -or $delivery.succeeded -ne $delivery.accepted -or ($delivery.pending+$delivery.dlq+$delivery.canceled) -ne 0) { throw 'Successful target did not drain' }
}
if ($SelfTest) {
    $good=@{load_succeeded=$true;delivery_expected=2;status='completed';delivery_verification='verified'}
    $done=@{accepted=2;succeeded=2;pending=0;dlq=0;canceled=0}
    Assert-Outcome $good $done 0 $false
    $bad=@{load_succeeded=$true;delivery_expected=2;status='verification_failed';delivery_verification='drain_timeout'}
    $waiting=@{accepted=2;succeeded=0;pending=1;dlq=1;canceled=0}
    Assert-Outcome $bad $waiting 1 $true
    foreach ($reason in @('query_failed','count_mismatch','not_configured','connection_setup_failed')) {
        $bad.delivery_verification=$reason
        $rejected=$false
        try { Assert-Outcome $bad $waiting 1 $true } catch { $rejected=$true }
        if (-not $rejected) { throw "Accepted invalid reason: $reason" }
    }
    $rejected=$false
    try { Assert-Outcome $good $waiting 0 $false } catch { $rejected=$true }
    if (-not $rejected) { throw 'Accepted unfinished healthy work' }
    $good.load_succeeded=$false
    $rejected=$false
    try { Assert-Outcome $good $done 0 $false } catch { $rejected=$true }
    if (-not $rejected) { throw 'Accepted failed HTTP load' }
    Write-Host 'PASS: mixed-target outcome self-tests'
    return
}
$repo=Split-Path $PSScriptRoot -Parent
Set-Location $repo
if (-not (Get-Command k6 -ErrorAction SilentlyContinue)) { throw 'Native k6 must be on PATH' }
$run='mixed-'+[DateTime]::UtcNow.ToString('yyyyMMddHHmmss')+'-'+[guid]::NewGuid().ToString('N').Substring(0,8)
$env:VERIFY_RUN_ID=$run
$compose=@('compose','--env-file','deploy/verification.env','-p',$run,'-f','deploy/compose.verification.yml')
$artifact=Join-Path $repo "reports/generated/$run"
New-Item -ItemType Directory -Path $artifact | Out-Null
$children=[Collections.Generic.List[object]]::new()
$report=@{run=$run;status='failed';seconds=$Seconds;target_concurrency=$TargetConcurrency;started_at=[DateTime]::UtcNow;results=[Collections.Generic.List[object]]::new()}
function Compose {
    $out=& docker.exe @compose @args
    if ($LASTEXITCODE -ne 0) { throw 'Docker command failed' }
    $out
}
function Target([string]$path) {
    (Invoke-RestMethod "$base/api/v1/targets" -Headers @{'X-API-Key'='verification-only'} -Method Post -ContentType application/json -Body (@{name='mixed-experiment';url="http://target-simulator:9090$path"}|ConvertTo-Json) -TimeoutSec 10).target_id
}
function Start-Load([string]$phase,[string]$kind,[int]$rate,[string]$target) {
    $out=Join-Path $artifact "$phase-$kind"
    New-Item -ItemType Directory -Path $out | Out-Null
    $drain=if ($kind -eq 'failing') {'10s'} else {'90s'}
    $arguments=@('-scenario','ingestion','-target-id',$target,'-base-url',$base,'-rate',"$rate",'-duration',"${Seconds}s",'-workers','4','-api-commit',$report.commit,'-machine','DockerDesktop-API2cpu512MiB-PG1cpu512MiB-Redis0.5cpu128MiB-Sim1cpu128MiB','-target-behavior',$kind,'-drain-wait',$drain,'-output',('"'+$out+'"'))
    $process=Start-Process -FilePath $benchmark -ArgumentList $arguments -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $out 'runner-output.txt') -RedirectStandardError (Join-Path $out 'runner-error.txt')
    $job=@{process=$process;phase=$phase;kind=$kind;rate=$rate;target=$target;output=$out}
    $children.Add($job)
    $job
}
function Finish-Loads($jobs) {
    $deadline=[DateTime]::UtcNow.AddSeconds($Seconds+120)
    foreach ($job in $jobs) {
        while (-not $job.process.WaitForExit(1000)) { if ([DateTime]::UtcNow -gt $deadline) { throw 'Benchmark child timeout' } }
        $directories=@(Get-ChildItem -LiteralPath $job.output -Directory)
        if ($directories.Count -ne 1) { throw 'Expected exactly one retained benchmark run' }
        $dir=$directories[0].FullName
        $meta=Get-Content (Join-Path $dir 'metadata.json') -Raw | ConvertFrom-Json
        $delivery=Get-Content (Join-Path $dir 'delivery.json') -Raw | ConvertFrom-Json
        Assert-Outcome $meta $delivery $job.process.ExitCode ($job.kind -eq 'failing')
        $summary=Get-Content (Join-Path $dir 'k6-summary.json') -Raw | ConvertFrom-Json
        $report.results.Add(@{phase=$job.phase;kind=$job.kind;rate=$job.rate;target=$job.target;exit_code=$job.process.ExitCode;verification=$meta.delivery_verification;delivery=$delivery;api_p95_ms=$summary.metrics.http_req_duration.'p(95)';load_started_at=$meta.load_started_at;load_finished_at=$meta.load_finished_at;artifact=[IO.Path]::GetRelativePath($artifact,$dir)})
        Write-Host "PASS: $($job.phase)/$($job.kind) recorded ($($meta.delivery_verification))"
    }
}
try {
    $report.commit=(git -c "safe.directory=$repo" rev-parse HEAD).Trim()
    $report.dirty=[bool](git -c "safe.directory=$repo" status --porcelain)
    $report.docker=(& docker.exe version --format '{{.Server.Version}}')
    if ($LASTEXITCODE -ne 0) { throw 'Docker unavailable' }
    $report.docker_host=(& docker.exe info --format '{"cpus":{{.NCPU}},"memory_bytes":{{.MemTotal}},"os":"{{.OperatingSystem}}"}')|ConvertFrom-Json
    $report.cpu=@(Get-CimInstance Win32_Processor|Select-Object Name,NumberOfCores,NumberOfLogicalProcessors)
    $report.os=[Runtime.InteropServices.RuntimeInformation]::OSDescription
    $report.go=go version
    $report.k6=k6 version
    Copy-Item deploy/compose.verification.yml (Join-Path $artifact 'compose.yml')
    # Mechanical override is retained alongside the exact base configuration.
    $override=Join-Path $artifact 'target-cap.yml'
    "services:`n  api:`n    environment:`n      WORKER_TARGET_CONCURRENCY: '$TargetConcurrency'" | Set-Content $override
    $compose+=@('-f',$override)
    Copy-Item $PSCommandPath (Join-Path $artifact 'measure-mixed-targets.ps1')
    Compose build api target-simulator
    Compose up -d --wait --wait-timeout 120 api target-simulator
    $base='http://'+((Compose port api 8080)|Select-Object -Last 1).Trim()
    $null=Invoke-RestMethod "$base/readyz" -TimeoutSec 5
    $report.images=@(Compose images --format json)
    $pg=((Compose port postgres 5432)|Select-Object -Last 1).Trim()
    $env:TEST_DATABASE_URL="postgres://webhook:verification-only@$pg/webhook?sslmode=disable"
    $env:API_KEY='verification-only';$env:K6_NO_USAGE_REPORT='true'
    $env:PRE_ALLOCATED_VUS='20';$env:MAX_VUS='50'
    $benchmark=Join-Path $artifact 'benchmark.exe'
    go build -buildvcs=false -o $benchmark ./cmd/benchmark
    if ($LASTEXITCODE -ne 0) { throw 'Benchmark build failed' }
    $healthy=Target '/sim/status/200'
    Finish-Loads @((Start-Load 'before' 'healthy' 5 $healthy))
    $slow=Target '/sim/latency/500/200'
    $failing=Target '/sim/status/503'
    $jobs=@((Start-Load 'mixed' 'healthy' 5 $healthy),(Start-Load 'mixed' 'slow' 10 $slow),(Start-Load 'mixed' 'failing' 2 $failing))
    Finish-Loads $jobs
    # Disable only our synthetic failing target, preventing retries contaminating the final control.
    $null=Invoke-RestMethod "$base/api/v1/targets/$failing" -Headers @{'X-API-Key'='verification-only'} -Method Delete -TimeoutSec 10
    Finish-Loads @((Start-Load 'after' 'healthy' 5 $healthy))
    $snapshot=(Compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U webhook -d webhook -Atc "SELECT coalesce(json_agg(t),'[]') FROM (SELECT target_id,status,count(*) AS events FROM events GROUP BY target_id,status ORDER BY target_id,status) t;") -join ''
    $report.final_sql=$snapshot|ConvertFrom-Json
    $report.status='measured'
} finally {
    foreach ($job in $children) { if (-not $job.process.HasExited) { Stop-Process -Id $job.process.Id -ErrorAction SilentlyContinue } }
    $report.finished_at=[DateTime]::UtcNow
    $report|ConvertTo-Json -Depth 15|Set-Content (Join-Path $artifact 'experiment.json')
    try { Compose logs --no-color|Set-Content (Join-Path $artifact 'containers.log') } finally {
        Compose --profile restore down --volumes --remove-orphans
        Write-Host "Evidence: $artifact"
    }
}
