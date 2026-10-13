#!/usr/bin/env python3
"""50 concurrent read clients; run against dedicated 100-organization fixture database."""
import concurrent.futures
import json
import os
import statistics
import time
import urllib.request
url = os.environ.get('LOAD_URL', 'http://127.0.0.1:8080')
host = os.environ.get('LOAD_HOST', 'school.localhost')
def request(i):
    start = time.perf_counter()
    req = urllib.request.Request(url + ['/api/v1/site', '/api/v1/catalog?page=1', '/api/v1/content?kind=news'][i % 3], headers={'Host': host})
    with urllib.request.urlopen(req, timeout=10) as response:
        assert response.status == 200
        json.load(response)
    return time.perf_counter() - start
with concurrent.futures.ThreadPoolExecutor(max_workers=50) as pool:
    timings = list(pool.map(request, range(1000)))
p95 = sorted(timings)[int(len(timings)*.95)-1]
print(f'1000 requests / 50 clients; errors=0; p95={p95:.3f}s; mean={statistics.mean(timings):.3f}s')
assert p95 <= 2, 'API read p95 exceeds 2s'
