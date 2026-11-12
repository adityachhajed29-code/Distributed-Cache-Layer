"""Measure successful HTTP reads and report failures separately."""
import argparse
import concurrent.futures
import json
import math
import time
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('--url', default='http://localhost:8081/v1/cache/benchmark')
parser.add_argument('--requests', type=int, default=10000)
parser.add_argument('--concurrency', type=int, default=100)
args = parser.parse_args()
if args.requests <= 0 or args.concurrency <= 0:
    parser.error('requests and concurrency must be positive')
request = urllib.request.Request(args.url, data=json.dumps({'val': 'dmFsdWU='}).encode(), method='PUT', headers={'Content-Type': 'application/json'})
with urllib.request.urlopen(request, timeout=10) as response:
    response.read()

def read(_):
    start = time.perf_counter()
    try:
        with urllib.request.urlopen(args.url, timeout=10) as response:
            payload = json.loads(response.read())
            if payload.get("val") != "dmFsdWU=":
                return None
        return (time.perf_counter() - start) * 1000
    except Exception:
        return None

start = time.perf_counter()
with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as pool:
    results = list(pool.map(read, range(args.requests)))
elapsed = time.perf_counter() - start
latencies = sorted(value for value in results if value is not None)
report = {'requests': args.requests, 'errors': args.requests - len(latencies), 'successful_requests_per_second': len(latencies) / elapsed}
for percentile in (50, 95, 99):
    report[f'p{percentile}_ms'] = latencies[max(0, math.ceil(len(latencies) * percentile / 100) - 1)] if latencies else None
print(json.dumps(report, indent=2))

if report["errors"]:
    raise SystemExit(1)
