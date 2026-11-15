param(
    [ValidateSet('cache-aside', 'write-through', 'write-behind')]
    [string]$Policy = 'write-through',
    [string]$GoExe = '',
    [switch]$Wait
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$root = Split-Path $PSScriptRoot -Parent
$local = Join-Path $root '.local'
$tools = Join-Path $local 'tools'
$data = Join-Path $local 'pgdata'
$stateFile = Join-Path $local 'processes.json'
New-Item -ItemType Directory -Force $tools, (Join-Path $local 'logs'), (Join-Path $local 'bin') | Out-Null

if (Test-Path $stateFile) {
    throw 'A previous run is recorded. Run scripts\stop-local.ps1 before starting again.'
}
foreach ($port in @(55432, 8101, 8102, 8103, 5101, 5102, 5103, 8080, 8081, 8082)) {
    $listener = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, $port)
    try { $listener.Start() } catch { throw "Port $port is occupied. Stop the process using it before starting." }
    finally { $listener.Stop() }
}
if (!$GoExe -and (Test-Path (Join-Path $local 'go-path.txt'))) {
    $savedGo = (Get-Content -Raw (Join-Path $local 'go-path.txt')).Trim()
    if (Test-Path $savedGo) { $GoExe = $savedGo }
}
if (!$GoExe) {
    $command = Get-Command go -ErrorAction SilentlyContinue
    if ($command) { $GoExe = $command.Source }
    else { $GoExe = Join-Path $tools 'go\bin\go.exe' }
}
if (!(Test-Path $GoExe)) {
    Write-Host 'Downloading portable Go...'
    $releases = Invoke-RestMethod 'https://go.dev/dl/?mode=json'
    $archive = $releases[0].files | Where-Object { $_.os -eq 'windows' -and $_.arch -eq 'amd64' -and $_.kind -eq 'archive' } | Select-Object -First 1
    $zip = Join-Path $tools 'go.zip'
    Invoke-WebRequest ('https://go.dev/dl/' + $archive.filename) -OutFile $zip -UseBasicParsing
    if ((Get-FileHash $zip -Algorithm SHA256).Hash.ToLower() -ne $archive.sha256) { throw 'Go download checksum mismatch' }
    Expand-Archive -LiteralPath $zip -DestinationPath $tools -Force
}
$GoExe | Set-Content (Join-Path $local 'go-path.txt')
$pgBin = Join-Path $tools 'pgsql\bin'
if (!(Test-Path (Join-Path $pgBin 'initdb.exe'))) {
    Write-Host 'Downloading portable PostgreSQL...'
    $zip = Join-Path $tools 'postgresql.zip'
    if (!(Test-Path $zip)) {
        Invoke-WebRequest 'https://get.enterprisedb.com/postgresql/postgresql-17.11-3-windows-x64-binaries.zip' -OutFile $zip -UseBasicParsing
    }
    Expand-Archive -LiteralPath $zip -DestinationPath $tools -Force
}

$savedEnv = @{}
foreach ($name in @('GOPATH','GOCACHE','GOMODCACHE','DATABASE_URL','WRITE_POLICY','BIND_HOST','SHARDS','DISCOVER_MEMBERS','PGPASSWORD')) {
    $savedEnv[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
$processes = @()
function Start-ServiceProcess($Name, $Executable, $Arguments) {
    $stopFile = Join-Path $local ($Name + '.stop')
    if (Test-Path $stopFile) { Remove-Item -LiteralPath $stopFile }
    $quoted = @($Arguments | ForEach-Object { '"' + $_ + '"' })
    $quoted += @('-stop_file', ('"' + $stopFile + '"'))
    $process = Start-Process -FilePath $Executable -ArgumentList $quoted -WorkingDirectory $root -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $local "logs\$Name.log") -RedirectStandardError (Join-Path $local "logs\$Name.err")
    $script:processes += [pscustomobject]@{ Name=$Name; Id=$process.Id; StartTicks=$process.StartTime.ToUniversalTime().Ticks; StopFile=$stopFile }
    $script:processes | ConvertTo-Json -Depth 3 | Set-Content $stateFile
}
try {
    $env:GOPATH = Join-Path $local 'gopath'
    $env:GOCACHE = Join-Path $local 'gocache'
    $env:GOMODCACHE = Join-Path $local 'gomodcache'
    Write-Host 'Building cache shards and gateways...'
    & $GoExe -C $root build -o (Join-Path $local 'bin\cache.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Cache build failed' }
    & $GoExe -C $root build -o (Join-Path $local 'bin\gateway.exe') ./cmd/gateway
    if ($LASTEXITCODE -ne 0) { throw 'Gateway build failed' }

    $passwordFile = Join-Path $local 'pg-password.txt'
    if (!(Test-Path $passwordFile)) {
        if (Test-Path (Join-Path $data 'PG_VERSION')) { throw 'Existing database password file is missing' }
        [Guid]::NewGuid().ToString('N') | Set-Content $passwordFile -Encoding ASCII
    }
    $password = (Get-Content -Raw $passwordFile).Trim()
    $env:PGPASSWORD = $password
    if (!(Test-Path (Join-Path $data 'PG_VERSION'))) {
        $initialData = Join-Path $local 'pgdata-initializing'
        if (Test-Path $initialData) { throw 'A previous database initialization is incomplete in .local\pgdata-initializing. Inspect its logs before retrying.' }
        & (Join-Path $pgBin 'initdb.exe') -D $initialData -U cache -A scram-sha-256 --pwfile=$passwordFile --encoding=UTF8 --locale=C
        if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL initialization failed' }
        # Both paths are fixed children of the project-local directory.
        Move-Item -LiteralPath $initialData -Destination $data
    }
    if (!(Test-Path (Join-Path $data 'global\pg_control'))) { throw 'Database initialization is incomplete. Check .local\pgdata before retrying.' }
    & (Join-Path $pgBin 'pg_ctl.exe') -D $data -l (Join-Path $local 'logs\postgres.log') -o '-h 127.0.0.1 -p 55432 -c max_connections=100 -c shared_buffers=32MB' -w start
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL startup failed' }
    $exists = & (Join-Path $pgBin 'psql.exe') -h 127.0.0.1 -p 55432 -U cache -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='cache'"
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL connection failed' }
    if ($exists -ne '1') {
        & (Join-Path $pgBin 'createdb.exe') -h 127.0.0.1 -p 55432 -U cache cache
        if ($LASTEXITCODE -ne 0) { throw 'Database creation failed' }
    }
    $nodes = @{}
    foreach ($i in 1..3) { $nodes["node$i"] = @{ id="node$i"; host='127.0.0.1'; rest_port=(8100+$i); grpc_port=(5100+$i) } }
    $configFile = Join-Path $local 'nodes.json'
    @{nodes=$nodes; enable_https=$false; enable_client_auth=$false; server_logfile='stdout'; server_errfile='stderr'} | ConvertTo-Json -Depth 5 | Set-Content $configFile -Encoding ASCII
    $env:DATABASE_URL = "postgres://cache:${password}@127.0.0.1:55432/cache?sslmode=disable"
    $env:WRITE_POLICY = $Policy
    $env:BIND_HOST = '127.0.0.1'
    foreach ($i in 1..3) {
        Start-ServiceProcess "shard$i" (Join-Path $local 'bin\cache.exe') @('-config',$configFile,'-node_id',"node$i",'-rest_port',"$(8100+$i)",'-grpc_port',"$(5100+$i)",'-bind_host','127.0.0.1','-enable_https=false','-enable_client_auth=false')
    }
    $env:SHARDS = 'http://127.0.0.1:8101,http://127.0.0.1:8102,http://127.0.0.1:8103'
    $env:DISCOVER_MEMBERS = 'true'
    Start-ServiceProcess 'coordinator' (Join-Path $local 'bin\gateway.exe') @('-addr','127.0.0.1:8080')
    Start-ServiceProcess 'coordinator-backup' (Join-Path $local 'bin\gateway.exe') @('-addr','127.0.0.1:8082')
    $env:SHARDS = 'http://127.0.0.1:8080,http://127.0.0.1:8082'
    $env:DISCOVER_MEMBERS = 'false'
    Start-ServiceProcess 'client' (Join-Path $local 'bin\gateway.exe') @('-addr','127.0.0.1:8081')
    $deadline = (Get-Date).AddSeconds(90)
    do {
        foreach ($entry in $processes) {
            if (!(Get-Process -Id $entry.Id -ErrorAction SilentlyContinue)) { throw "$($entry.Name) exited. Check .local\logs\$($entry.Name).err" }
        }
        $ready = $true
        foreach ($port in @(8101,8102,8103,8080,8082,8081)) {
            try { Invoke-WebRequest "http://127.0.0.1:$port/health" -UseBasicParsing -TimeoutSec 2 | Out-Null } catch { $ready=$false }
        }
        if ($ready) { break }
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $deadline)
    if (!$ready) { throw 'Services did not become healthy within 90 seconds. Check .local\logs.' }
    Write-Host "Project ready with $Policy at http://localhost:8081"
    & (Join-Path $PSScriptRoot 'demo-local.ps1')
    if ($Wait) {
        Write-Host 'Keep this window open. Use stop-local.cmd to stop the project gracefully.'
        do {
            Start-Sleep -Seconds 1
            $running = @($processes | Where-Object {
                $process = Get-Process -Id $_.Id -ErrorAction SilentlyContinue
                $process -and $process.StartTime.ToUniversalTime().Ticks -eq [long]$_.StartTicks
            })
        } while ($running.Count -gt 0)
    }
} catch {
    Write-Warning $_
    & (Join-Path $PSScriptRoot 'stop-local.ps1')
    throw
} finally {
    foreach ($name in $savedEnv.Keys) { [Environment]::SetEnvironmentVariable($name, $savedEnv[$name], 'Process') }
}
