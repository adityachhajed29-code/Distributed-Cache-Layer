$ErrorActionPreference = 'Stop'
$url = 'http://127.0.0.1:8081/v1/cache/demo:windows'
$value = 'Hello from my Docker-free distributed cache!'
$body = @{val=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($value));ttl_ms=30000} | ConvertTo-Json
Write-Host 'Saving a value...'
Invoke-RestMethod -Method Put -Uri $url -ContentType 'application/json' -Body $body | ConvertTo-Json -Depth 5
Write-Host 'Reading it through the client and coordinator...'
$record = Invoke-RestMethod $url
$decoded = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($record.val))
if ($decoded -ne $value) { throw 'Read returned an incorrect value' }
Write-Host "Decoded value: $decoded"
Write-Host "Served by: $($record.provenance.node_id)"
Write-Host 'PASS. Open http://localhost:8081/v1/cache/demo:windows to see the JSON response.'
