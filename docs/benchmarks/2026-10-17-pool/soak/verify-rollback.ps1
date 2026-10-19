$ErrorActionPreference='Stop'
$env:VERIFY_RUN_ID='verify-20261017100129-616dc327'
$artifact=Join-Path (Get-Location) "reports/generated/$env:VERIFY_RUN_ID"
$compose=@('compose','--env-file','deploy/verification.env','-p',$env:VERIFY_RUN_ID,'-f','deploy/compose.verification.yml')
$headers=@{'X-API-Key'='verification-only'}
function Compose { $value=& docker.exe @compose @args; if ($LASTEXITCODE -ne 0) { throw 'Compose failed' }; $value }
function SQL($query) { ((Compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U webhook -d webhook -Atc $query) -join "`n").Trim() }
function Wait($url,$status) {
    $end=[datetime]::UtcNow.AddSeconds(45)
    do { try { $value=Invoke-RestMethod $url -Headers $headers -TimeoutSec 2; if (-not $status -or $value.status -eq $status) { return $value } } catch { }; Start-Sleep -Milliseconds 200 } while ([datetime]::UtcNow -lt $end)
    throw "Timed out: $url"
}
function Send($base,$target) {
    $requestHeaders=@{'X-API-Key'='verification-only';'Idempotency-Key'=[guid]::NewGuid().ToString()}
    $event=Invoke-RestMethod "$base/api/v1/targets/$target/events" -Method Post -Headers $requestHeaders -ContentType application/json -Body '{"payload":{"type":"rollback-verification"}}'
    $null=Wait "$base/api/v1/events/$($event.event_id)" 'SUCCEEDED'
    $event.event_id
}
if ((Get-Content "$artifact/release.json" -Raw | ConvertFrom-Json).status -ne 'passed') { throw 'Requires completed synthetic soak' }
$metadata=Get-Content "$artifact/load/20261017T100505.759483800Z/metadata.json" -Raw | ConvertFrom-Json
$target=$metadata.target_id
$oldEvent=SQL "SELECT id FROM events WHERE target_id='$target' AND status='SUCCEEDED' ORDER BY id LIMIT 1;"
if (-not $oldEvent) { throw 'Missing prior event' }
$fingerprint="SELECT json_build_object("+((@('targets','events','delivery_attempts','outbox_messages','schema_migrations') | ForEach-Object { $order=if ($_ -eq 'schema_migrations') {'version'} else {'id'}; "'$_',(SELECT md5(COALESCE(string_agg(to_jsonb(t)::text,'' ORDER BY $order),'')) FROM $_ t)" }) -join ',')+");"
Start-Transcript "$artifact/rollback-transcript.txt" | Out-Null
try {
    if ((SQL "SELECT count(*) FROM events WHERE status IN ('PENDING','QUEUED','RETRY_WAIT');") -ne '0') { throw 'Work has not drained' }
    Compose stop api
    $source=SQL $fingerprint
    Compose -f "$artifact/rollback.compose.yml" up -d --no-deps --no-build api
    $base='http://'+(Compose port api 8080).Trim()
    $null=Wait "$base/readyz" ''
    $restored=SQL $fingerprint
    if ($source -ne $restored) { throw 'Rollback changed durable history' }
    $prior=Wait "$base/api/v1/events/$oldEvent" 'SUCCEEDED'
    $rollbackEvent=Send $base $target
    $rollbackImage=Compose images api --format json
    Compose stop api
    Compose up -d --no-deps --no-build api
    $base='http://'+(Compose port api 8080).Trim()
    $null=Wait "$base/readyz" ''
    $null=Wait "$base/api/v1/events/$oldEvent" 'SUCCEEDED'
    $null=Wait "$base/api/v1/events/$rollbackEvent" 'SUCCEEDED'
    $forwardEvent=Send $base $target
    @{status='passed';at=[datetime]::UtcNow;run=$env:VERIFY_RUN_ID;source_fingerprints=($source|ConvertFrom-Json);rollback_fingerprints=($restored|ConvertFrom-Json);prior_event=$oldEvent;rollback_event=$rollbackEvent;forward_event=$forwardEvent;rollback_image=$rollbackImage;forward_image=(Compose images api --format json);scope='Same-schema local image rollback; no schema downgrade or cloud rollout claimed.'} | ConvertTo-Json -Depth 10 | Tee-Object "$artifact/rollback.json"
} finally {
    Compose logs --no-color api | Tee-Object "$artifact/rollback-container.log" | Out-Null
    Compose --profile restore down --volumes --remove-orphans
    Stop-Transcript | Out-Null
}
