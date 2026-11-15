$ErrorActionPreference = 'Stop'
$local = Join-Path (Split-Path $PSScriptRoot -Parent) '.local'
$stateFile = Join-Path $local 'processes.json'
if (Test-Path $stateFile) {
    $entries = Get-Content -Raw $stateFile | ConvertFrom-Json
    foreach ($entry in $entries) { New-Item -ItemType File -Force -Path $entry.StopFile | Out-Null }
    $deadline = (Get-Date).AddSeconds(40)
    do {
        $remaining = @($entries | Where-Object {
            $process = Get-Process -Id $_.Id -ErrorAction SilentlyContinue
            $process -and $process.StartTime.ToUniversalTime().Ticks -eq [long]$_.StartTicks
        })
        if (!$remaining.Count) { break }
        Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $deadline)
    if ($remaining.Count) { throw 'Some services have not drained. Database remains running. Check .local\logs and retry stopping.' }
    Remove-Item -LiteralPath $stateFile
}
$pgCtl = Join-Path $local 'tools\pgsql\bin\pg_ctl.exe'
$data = Join-Path $local 'pgdata'
if ((Test-Path $pgCtl) -and (Test-Path (Join-Path $data 'postmaster.pid'))) {
    & $pgCtl -D $data status | Out-Null
    if ($LASTEXITCODE -eq 0) {
        & $pgCtl -D $data -m fast -w stop
        if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL did not stop cleanly' }
    }
}
Write-Host 'Project stopped. Database records are preserved in .local\pgdata.'

