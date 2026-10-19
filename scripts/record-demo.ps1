#Requires -Version 7.0
# Output-only, line-timed asciicast v2; captures no keys or environment values.
$ErrorActionPreference='Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
$dir=Join-Path (Get-Location) ('reports/generated/demo-'+[DateTime]::UtcNow.ToString('yyyyMMddHHmmss'))
New-Item -ItemType Directory -Path $dir | Out-Null
$path=Join-Path $dir 'showcase.cast'
$writer=[IO.StreamWriter]::new($path,$false,[Text.UTF8Encoding]::new($false))
$text=[System.Collections.Generic.List[string]]::new()
$watch=[Diagnostics.Stopwatch]::StartNew()
try {
    $writer.WriteLine((@{version=2;width=160;height=32;timestamp=[DateTimeOffset]::UtcNow.ToUnixTimeSeconds();title='Webhook dispatcher: real failure and recovery demo';command='go run ./cmd/acceptance -demo'}|ConvertTo-Json -Compress))
    go run ./cmd/acceptance -demo 2>&1 | ForEach-Object {
        $line=$_.ToString()
        $text.Add($line)
        $writer.WriteLine((@([Math]::Round($watch.Elapsed.TotalSeconds,6),'o',($line+"`r`n"))|ConvertTo-Json -Compress))
        Write-Host $line
    }
    $code=$LASTEXITCODE
} finally {
    $writer.Dispose()
    $text | Set-Content (Join-Path $dir 'showcase.txt')
}
# Validate the recording structure and the actual demo's exit status.
$rows=@(Get-Content $path)
$header=$rows[0]|ConvertFrom-Json
if ($header.version -ne 2 -or $rows.Count -lt 2) { throw 'Invalid recording header' }
$previous=0.0
foreach ($row in $rows[1..($rows.Count-1)]) {
    $event=$row|ConvertFrom-Json
    if ($event.Count -ne 3 -or $event[1] -ne 'o' -or $event[0] -lt $previous) { throw 'Invalid recording event' }
    $previous=$event[0]
}
if ($code -ne 0) { throw "Demo failed with exit code $code; recording retained" }
Write-Host "Recording and transcript: $dir"
