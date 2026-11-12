"""Exercise the running Compose stack and coordinator/shard recovery."""
import base64
import json
import subprocess
import time
import urllib.error
import urllib.request

BASE = 'http://localhost:8081'
COMPOSE = ['docker', 'compose', '-f', 'deploy/compose.yml']

def call(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(BASE + path, data=data, method=method, headers={'Content-Type': 'application/json'})
    with urllib.request.urlopen(request, timeout=10) as response:
        raw = response.read()
        return json.loads(raw) if raw else None

def wait_ready():
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        try:
            call('GET', '/health')
            return
        except (OSError, urllib.error.HTTPError):
            time.sleep(1)
    raise RuntimeError('cluster did not become ready')

wait_ready()
keys = [f'smoke:{i}' for i in range(30)]
for key in keys:
    call('PUT', '/v1/cache/' + key, {'val': base64.b64encode(key.encode()).decode(), 'ttl_ms': 100})
time.sleep(1)

def verify():
    for key in keys:
        result = call('GET', '/v1/cache/' + key)
        assert base64.b64decode(result['val']).decode() == key, result
        assert result['version'] > 0, result

verify()
for service in ['coordinator', 'shard2']:
    subprocess.run(COMPOSE + ['stop', service], check=True)
    try:
        time.sleep(5)
        wait_ready()
        verify()
    finally:
        subprocess.run(COMPOSE + ['start', service], check=True)
    time.sleep(5)
    wait_ready()
    verify()
for key in keys:
    call('DELETE', '/v1/cache/' + key)
    try:
        call('GET', '/v1/cache/' + key)
        raise AssertionError('deleted key remains visible')
    except urllib.error.HTTPError as error:
        assert error.code == 404
print('PASS: persistence, coordinator failover, shard failure/recovery, deletion')
