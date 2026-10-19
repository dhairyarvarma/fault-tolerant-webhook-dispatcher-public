$ErrorActionPreference='Stop'
$root='docs/benchmarks/2026-10-19-operations'
$report=Get-Content "$root/operations.json" -Raw|ConvertFrom-Json
$result=@{resource_samples=0;resources=@{};workloads=@();peak_sampled_pending=0;peak_sampled_inflight=0}
function MiB([string]$value){
 $m=[regex]::Match($value,'^([\d.]+)([A-Za-z]+)')
 $factors=@{B=1;KiB=1024;MiB=1048576;GiB=1073741824;kB=1000;MB=1000000;GB=1000000000}
 if(-not $m.Success -or -not $factors.ContainsKey($m.Groups[2].Value)){throw 'Unknown memory unit'}
 [double]$m.Groups[1].Value*$factors[$m.Groups[2].Value]/1048576
}
foreach($line in (Get-Content "$root/resources.jsonl")){
 $sample=$line|ConvertFrom-Json;if($sample.exit_code -ne 0){throw 'Resource sample failed'};$result.resource_samples++
 foreach($row in $sample.containers){$v=$row|ConvertFrom-Json;$kind=if($v.Name -match '-target-simulator-1$'){'simulator'}elseif($v.Name -match '-postgres-1$'){'postgres'}elseif($v.Name -match '-redis-1$'){'redis'}elseif($v.Name -match '-api-1$'){'api'}else{throw 'Unknown container'}
  $memory=MiB $v.MemUsage
  if(-not $result.resources.ContainsKey($kind)){$result.resources[$kind]=@{samples=0;cpu_sum=0;memory_first_mib=$memory;memory_last_mib=0;memory_peak_mib=0}}
  $r=$result.resources[$kind];$r.samples++;$r.cpu_sum += [double]$v.CPUPerc.TrimEnd('%');$r.memory_last_mib=$memory;$r.memory_peak_mib=[Math]::Max([double]$r.memory_peak_mib,[double]$memory)
 }
}
foreach($r in $result.resources.Values){$r.cpu_mean_percent_per_core=$r.cpu_sum/$r.samples;$r.Remove('cpu_sum')}
foreach($check in $report.checks){if($check.detail.workloads){foreach($work in $check.detail.workloads){
 $summary=Get-Content (Join-Path (Join-Path $root $work.artifact) 'k6-summary.json') -Raw|ConvertFrom-Json
 if($summary.metrics.http_req_failed.value -ne 0 -or $summary.metrics.dropped_iterations.count -ne 0){throw 'Load failures in successful report'}
 $result.workloads+=@{kind=$work.kind;delivery=$work.delivery;api_p95_ms=$summary.metrics.http_req_duration.'p(95)';http_failed=$summary.metrics.http_req_failed.value;dropped=$summary.metrics.dropped_iterations.count}
 if($work.kind -eq 'slow'){foreach($line in (Get-Content (Join-Path (Join-Path $root $work.artifact) 'metrics.jsonl'))){$metrics=($line|ConvertFrom-Json).metrics;foreach($pair in @(@('peak_sampled_pending','webhook_events_pending'),@('peak_sampled_inflight','webhook_delivery_in_flight'))){$m=[regex]::Match($metrics,'(?m)^'+$pair[1]+' (\d+)');if($m.Success){$result[$pair[0]]=[Math]::Max($result[$pair[0]],[int]$m.Groups[1].Value)}}}}
}}}
$result|ConvertTo-Json -Depth 12|Set-Content "$root/analysis.json"
$result|ConvertTo-Json -Depth 6
