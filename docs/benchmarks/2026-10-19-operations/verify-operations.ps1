#Requires -Version 7.0
[CmdletBinding()]
param([ValidateRange(120,1800)][int]$SoakSeconds=600,[switch]$SelfTest)
$ErrorActionPreference='Stop'
function Gauge([string]$text,[string]$name) {
 $m=[regex]::Match($text,'(?m)^'+[regex]::Escape($name)+' (\d+)\r?$')
 if (-not $m.Success) {throw "Missing gauge: $name"};[long]$m.Groups[1].Value
}
if ($SelfTest) {
 if ((Gauge "x 2`ny 3`n" x) -ne 2) {throw 'Gauge parser failed'}
 $rejected=$false;try{Gauge 'xx 3' x}catch{$rejected=$true};if (-not $rejected){throw 'Accepted missing gauge'}
 Write-Host 'PASS: operational harness self-tests';return
}
$repo=Split-Path $PSScriptRoot -Parent;Set-Location $repo
if (-not (Get-Command k6 -ErrorAction SilentlyContinue)){throw 'Native k6 must be on PATH'}
$run='operations-'+[DateTime]::UtcNow.ToString('yyyyMMddHHmmss')+'-'+[guid]::NewGuid().ToString('N').Substring(0,8)
$env:VERIFY_RUN_ID=$run
$dir=Join-Path $repo "reports/generated/$run";New-Item -ItemType Directory -Path $dir|Out-Null
$override=Join-Path $dir 'override.yml'
$compose=@('compose','--env-file','deploy/verification.env','-p',$run,'-f','deploy/compose.verification.yml','-f',$override)
$report=@{run=$run;status='failed';started_at=[DateTime]::UtcNow;soak_seconds=$SoakSeconds;checks=[Collections.Generic.List[object]]::new()}
$children=[Collections.Generic.List[object]]::new();$sampler=$null
function Compose { $v=& docker.exe @compose @args;if($LASTEXITCODE -ne 0){throw 'Docker command failed'};$v }
function Override([int]$limit) {
 @"
services:
  api:
    environment:
      WORKER_TARGET_CONCURRENCY: '2'
      EVENTS_MAX_PENDING_EVENTS: '$limit'
  restored-api:
    environment:
      DATABASE_URL: postgres://webhook:verification-only@postgres:5432/webhook?sslmode=disable
      REDIS_STREAM: verification-deliveries
      WORKER_TARGET_CONCURRENCY: '2'
"@ | Set-Content $override
}
function Address([string]$service){'http://'+((Compose port $service 8080)|Select-Object -Last 1).Trim()}
function API([string]$base,[string]$method,[string]$path,$body=$null,[string]$key='') {
 $h=@{'X-API-Key'='verification-only'};if($key){$h['Idempotency-Key']=$key}
 $p=@{Uri=$base+$path;Method=$method;Headers=$h;TimeoutSec=10;SkipHttpErrorCheck=$true}
 if($null -ne $body){$p.ContentType='application/json';$p.Body=$body|ConvertTo-Json -Compress}
 $response=Invoke-WebRequest @p
 @{code=[int]$response.StatusCode;body=($response.Content|ConvertFrom-Json);headers=$response.Headers}
}
function Target([string]$path){$r=API $base POST '/api/v1/targets' @{name='operations';url="http://target-simulator:9090$path"};if($r.code -ne 201){throw 'Target creation failed'};$r.body.target_id}
function Event([string]$target,[string]$key){$r=API $base POST "/api/v1/targets/$target/events" @{payload=@{type='operations'}} $key;if($r.code -ne 201){throw 'Event creation failed'};$r.body.event_id}
function Success([string]$address,[string]$event,[int]$seconds=30){
 $deadline=[DateTime]::UtcNow.AddSeconds($seconds)
 do{$r=API $address GET "/api/v1/events/$event";if($r.code -eq 200 -and $r.body.status -eq 'SUCCEEDED'){return $r.body};Start-Sleep -Milliseconds 200}while([DateTime]::UtcNow -lt $deadline)
 throw 'Event did not reach durable success'
}
function SQL([string]$query){((Compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U webhook -d webhook -Atc $query)-join '').Trim()}
function Check([string]$name,$value){$report.checks.Add(@{name=$name;at=[DateTime]::UtcNow;detail=$value});Write-Host "PASS: $name"}
try {
 $report.commit=(git -c "safe.directory=$repo" rev-parse HEAD).Trim();$report.dirty=[bool](git -c "safe.directory=$repo" status --porcelain)
 $report.cpu=@(Get-CimInstance Win32_Processor|Select-Object Name,NumberOfCores,NumberOfLogicalProcessors)
 $report.os=[Runtime.InteropServices.RuntimeInformation]::OSDescription;$report.go=go version;$report.k6=k6 version
 $report.docker_host=(& docker.exe info --format '{"cpus":{{.NCPU}},"memory_bytes":{{.MemTotal}},"os":"{{.OperatingSystem}}"}')|ConvertFrom-Json
 if($LASTEXITCODE -ne 0){throw 'Docker unavailable'}
 Copy-Item deploy/compose.verification.yml (Join-Path $dir 'compose.yml');Copy-Item $PSCommandPath (Join-Path $dir 'verify-operations.ps1')
 Override 3
 Compose build api target-simulator
 Compose up -d --wait --wait-timeout 120 api target-simulator prometheus grafana
 $base=Address api;$report.images=@(Compose images --format json)
 $null=Invoke-RestMethod "$base/readyz" -TimeoutSec 5
 $target=Target '/sim/status/200'
 Compose stop redis
 $events=@(Event $target 'accepted-1';Event $target 'accepted-2';Event $target 'accepted-3')
 $rejected=API $base POST "/api/v1/targets/$target/events" @{payload=@{type='operations'}} 'rejected'
 if($rejected.code -ne 429 -or -not $rejected.headers['Retry-After']){throw 'Admission did not reject predictably'}
 $duplicate=API $base POST "/api/v1/targets/$target/events" @{payload=@{type='operations'}} 'accepted-1'
 if($duplicate.code -ne 409 -or $duplicate.body.event_id -ne $events[0]){throw 'Duplicate at capacity lost event identity'}
 if((SQL 'SELECT count(*) FROM events') -ne '3' -or (SQL 'SELECT count(*) FROM outbox_messages') -ne '3'){throw 'Rejected request created durable phantom work'}
 Compose start redis
 foreach($event in $events){$null=Success $base $event}
 $resumed=Event $target 'rejected';$null=Success $base $resumed
 Check 'Admission rejects, preserves accepted work, and resumes after Redis recovery' @{accepted=$events;resumed=$resumed;rejection_status=429;duplicate_status=409;retry_after=$rejected.headers['Retry-After']}
 Override 0;Compose up -d --wait api;$base=Address api
 Compose up -d --wait restored-api;$peer=Address restored-api
 $parallel=Target '/sim/latency/1000/200'
 $ids=@(1..16|ForEach-Object{Event $parallel "parallel-$_"})
 $samples=[Collections.Generic.List[object]]::new();$peakA=0;$peakB=0;$peakSum=0
 $deadline=[DateTime]::UtcNow.AddSeconds(30)
 do {
  $a=Gauge (Invoke-WebRequest "$base/metrics" -TimeoutSec 3).Content 'webhook_delivery_in_flight'
  $b=Gauge (Invoke-WebRequest "$peer/metrics" -TimeoutSec 3).Content 'webhook_delivery_in_flight'
  if($a -gt 2 -or $b -gt 2){throw 'Per-process target cap exceeded'}
  $peakA=[Math]::Max($a,$peakA);$peakB=[Math]::Max($b,$peakB);$peakSum=[Math]::Max($a+$b,$peakSum)
  $samples.Add(@{at=[DateTime]::UtcNow;first=$a;second=$b})
  $pending=[int](SQL "SELECT count(*) FROM events WHERE target_id='$parallel' AND status<>'SUCCEEDED'")
  if($pending -eq 0){break};Start-Sleep -Milliseconds 100
 }while([DateTime]::UtcNow -lt $deadline)
 if($pending -ne 0 -or $peakA -eq 0 -or $peakB -eq 0){throw 'Both instances did not demonstrably process work'}
 $samples|ConvertTo-Json -Depth 5|Set-Content (Join-Path $dir 'two-instance-samples.json')
 (Invoke-WebRequest "$base/metrics" -TimeoutSec 3).Content|Set-Content (Join-Path $dir 'metrics-two-instance-first.txt')
 (Invoke-WebRequest "$peer/metrics" -TimeoutSec 3).Content|Set-Content (Join-Path $dir 'metrics-two-instance-peer.txt')
 Check 'Two instances share work with process-local target caps' @{events=$ids;first_peak=$peakA;second_peak=$peakB;sampled_pair_sum_peak=$peakSum;scope='sequential scrapes, not simultaneous wire measurements';containers=@(Compose ps -q api restored-api)}
 Compose stop restored-api
 $slow=Target '/sim/latency/8000/200';$leased=Event $slow 'kill-owner'
 $deadline=[DateTime]::UtcNow.AddSeconds(10)
 do{$token=SQL "SELECT coalesce(delivery_token::text,'') FROM events WHERE id='$leased'";if($token){break};Start-Sleep -Milliseconds 50}while([DateTime]::UtcNow -lt $deadline)
 if(-not $token){throw 'No known first-instance claim observed'}
 Compose up -d --wait restored-api;$peer=Address restored-api
 if((SQL "SELECT coalesce(delivery_token::text,'') FROM events WHERE id='$leased'") -ne $token){throw 'Claim completed or changed before kill; boundary not demonstrated'}
 $watch=[Diagnostics.Stopwatch]::StartNew();Compose kill -s SIGKILL api
 $recovered=Success $peer $leased 120
 Check 'Surviving instance recovers a killed owners event' @{event=$leased;kill_to_success_seconds=$watch.Elapsed.TotalSeconds;status=$recovered.status;attempt_count=$recovered.attempt_count;definition='Observed first-instance claim; includes kill/polling, not exact HTTP-success crash window'}
 Compose start api;$base=Address api;Compose stop restored-api
 # Only the first API remains for the soak; a process-local cap is not a cluster cap.
 $env:API_KEY='verification-only';$env:PRE_ALLOCATED_VUS='20';$env:MAX_VUS='50';$env:K6_NO_USAGE_REPORT='true'
 $pg=((Compose port postgres 5432)|Select-Object -Last 1).Trim();$env:TEST_DATABASE_URL="postgres://webhook:verification-only@$pg/webhook?sslmode=disable"
 $benchmark=Join-Path $dir 'benchmark.exe';go build -buildvcs=false -o $benchmark ./cmd/benchmark
 if($LASTEXITCODE -ne 0){throw 'Benchmark build failed'}
 $containers=@(Compose ps -q api postgres redis target-simulator)
 $sampler=Start-Job -ArgumentList $containers,(Join-Path $dir 'resources.jsonl') -ScriptBlock {
  param($ids,$path)
  while($true){$rows=& docker.exe stats --no-stream --format '{{json .}}' @ids;@{at=[DateTime]::UtcNow;exit_code=$LASTEXITCODE;containers=@($rows)}|ConvertTo-Json -Compress|Add-Content $path;Start-Sleep -Seconds 2}
 }
 $beforeMetrics=(Invoke-WebRequest "$base/metrics").Content;$beforeMetrics|Set-Content (Join-Path $dir 'metrics-before-soak.txt')
 foreach($work in @(@{name='healthy';path='/sim/status/200';rate=5},@{name='slow';path='/sim/latency/500/200';rate=3})) {
  $id=Target $work.path;$out=Join-Path $dir "soak-$($work.name)";New-Item -ItemType Directory -Path $out|Out-Null
  $arguments=@('-scenario','soak','-target-id',$id,'-base-url',$base,'-rate',"$($work.rate)",'-duration',"${SoakSeconds}s",'-workers','4','-api-commit',$report.commit,'-machine','DockerDesktop-API2cpu512MiB-PG1cpu512MiB-Redis0.5cpu128MiB-Sim1cpu128MiB','-target-behavior',$work.name,'-drain-wait','120s','-output',('"'+$out+'"'))
  $process=Start-Process -FilePath $benchmark -ArgumentList $arguments -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $out 'runner-output.txt') -RedirectStandardError (Join-Path $out 'runner-error.txt')
  $children.Add(@{process=$process;name=$work.name;target=$id;output=$out})
 }
 $soakResults=[Collections.Generic.List[object]]::new();$deadline=[DateTime]::UtcNow.AddSeconds($SoakSeconds+180)
 foreach($child in $children){
  while(-not $child.process.WaitForExit(1000)){if([DateTime]::UtcNow -gt $deadline){throw 'Soak process timeout'}}
  $runs=@(Get-ChildItem $child.output -Directory);if($runs.Count -ne 1){throw 'Ambiguous soak artifacts'}
  $meta=Get-Content (Join-Path $runs[0].FullName 'metadata.json') -Raw|ConvertFrom-Json
  $delivery=Get-Content (Join-Path $runs[0].FullName 'delivery.json') -Raw|ConvertFrom-Json
  if($child.process.ExitCode -ne 0 -or $meta.status -ne 'completed' -or $meta.delivery_verification -ne 'verified' -or $delivery.accepted -ne $meta.delivery_expected -or $delivery.succeeded -ne $delivery.accepted -or $delivery.pending -ne 0 -or $delivery.dlq -ne 0 -or $delivery.canceled -ne 0){throw 'Soak verification failed'}
  $soakResults.Add(@{target=$child.target;kind=$child.name;delivery=$delivery;artifact=[IO.Path]::GetRelativePath($dir,$runs[0].FullName)})
 }
 $afterMetrics=(Invoke-WebRequest "$base/metrics").Content;$afterMetrics|Set-Content (Join-Path $dir 'metrics-after-soak.txt')
 Check 'Capped single-process soak completed with SQL-verified delivery' @{workloads=$soakResults;deferrals=(Gauge $afterMetrics 'webhook_target_capacity_deferrals_total')-(Gauge $beforeMetrics 'webhook_target_capacity_deferrals_total');retention_disabled=$true}
 $prom='http://'+((Compose port prometheus 9090)|Select-Object -Last 1).Trim()
 $expressions=@('webhook_target_capacity_deferrals_total','webhook_history_events_pruned_total','webhook_history_cleanup_errors_total')
 $queries=@(foreach($expr in $expressions){$result=Invoke-RestMethod "$prom/api/v1/query?query=$expr" -TimeoutSec 5;if($result.status -ne 'success' -or $result.data.result.Count -eq 0){throw 'New metric not scraped'};@{expression=$expr;response=$result}})
 $queries|ConvertTo-Json -Depth 15|Set-Content (Join-Path $dir 'metric-queries.json')
 $report.status='passed'
} finally {
 foreach($child in $children){if(-not $child.process.HasExited){Stop-Process -Id $child.process.Id -ErrorAction SilentlyContinue}}
 if($sampler){Stop-Job $sampler;Receive-Job $sampler;Remove-Job $sampler}
 $report.finished_at=[DateTime]::UtcNow;$report|ConvertTo-Json -Depth 20|Set-Content (Join-Path $dir 'operations.json')
 try{Compose logs --no-color|Set-Content (Join-Path $dir 'containers.log')}finally{Compose --profile restore down --volumes --remove-orphans;Write-Host "Evidence: $dir"}
}
