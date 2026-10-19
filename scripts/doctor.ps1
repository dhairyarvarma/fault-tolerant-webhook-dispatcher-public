$commands = @('git','go','node','npm','docker','gh','k6','claude','codex')

foreach ($cmd in $commands) {
    $found = Get-Command $cmd -ErrorAction SilentlyContinue
    if ($found) {
        Write-Host "[OK]   $cmd -> $($found.Source)"
    } else {
        Write-Host "[MISS] $cmd"
    }
}

Write-Host ""
Write-Host "Docker Compose:"
try {
    docker compose version
} catch {
    Write-Host "[MISS] docker compose"
}
