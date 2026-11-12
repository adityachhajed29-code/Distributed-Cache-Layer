# Distributed In-Memory Cache Layer

Go caching proxy extending [minicache](https://github.com/danielvegamyhre/minicache).
See [reconstructed history](docs/HISTORY.md) for the explicitly labeled August 20
through November 10, 2026 commits and team attribution. This reconstruction was
created on November 12, 2026.

## Run

Requires Docker Engine and Compose.

```sh
docker compose -f deploy/compose.yml up --build -d
curl -X PUT http://localhost:8081/v1/cache/demo -H 'Content-Type: application/json' -d '{"val":"aGVsbG8=","ttl_ms":30000}'
curl http://localhost:8081/v1/cache/demo
curl -X DELETE http://localhost:8081/v1/cache/demo
```

The deployment contains PostgreSQL, three shards, a primary coordinator gateway on port
8080, a standby coordinator, and a client-facing proxy on port 8081. Set WRITE_POLICY to cache-aside,
write-through, or write-behind before starting Compose. Default is write-through.
The supplied database credentials are for local demonstration.

## Implemented

- Thread-safe O(1) LRU with per-entry TTL, deletion, and active expiry sweeping.
- A 32-bit consistent hash ring with 50 virtual nodes per physical shard.
- PostgreSQL connection pooling, parameterized queries, transactional batches,
  monotonic database-assigned versions, and persistent deletion tombstones.
- Cache-aside write eviction, synchronous write-through, and bounded asynchronous
  write-behind with retry backoff and shutdown draining.
- Singleflight read coalescing and bounded background X-Fetch refresh on hot reads.
- JSON granules with base64 values, version, TTL, observed database latency, node
  provenance, and SHA-256 checksum.
- HTTP GET/PUT/DELETE, inherited HTTP/gRPC access, membership discovery, Bully
  election, and heartbeat monitoring. Shared cluster state is synchronized.
- Health-filtered coordinator routing, Compose services, CI checks, core regression
  tests, an in-memory benchmark, and HTTP latency load generator.

The new API normalizes keys to lowercase and rejects characters outside
[a-z0-9:_-], with lengths from 1 to 250. Decoded values are limited to 512 KiB.
Binary values, including zero bytes, are stored as PostgreSQL BYTEA. Cache TTL
expires memory entries while retaining durable source records. TTL defaults to
30 seconds, with a maximum request TTL of 365 days.

## Consistency and limitations

Write-through commits PostgreSQL before updating memory. Those two operations do
not form a single atomic transaction. Write-behind acknowledges volatile queued
writes with HTTP 202 and provisional version 0. The database assigns the committed
version during flushing. A full queue returns 503. Abrupt shard loss can lose
unflushed writes. Retries following an ambiguous database commit can increment a
version again. Cross-shard concurrent writes are resolved by database commit order.

Cache coherence is bounded by TTL during ownership changes. Existing cached copies
are not invalidated across the entire cluster. There is no cross-shard replication,
quorum fencing, durable write queue, or network-partition consistency guarantee.
Bully election handles the inherited shard membership coordinator. The HTTP gateways
are replicated as primary and standby services. The client proxy health-checks
both and routes through surviving coordinators. Each gateway discovers additional
shards from the membership endpoint and probes known endpoints for recovery.
Failover converges after health polling, so in-flight requests can fail during a
transition. The client-facing proxy remains the deployment entry point.

The new JSON API is the supported interface for TTL and provenance. The inherited
HTTP/gRPC interface retains its original string payload behavior. Production-grade
hardening of all inherited transport paths remains pending.

## Verify

Requires Go 1.23 or newer.

```sh
go build ./...
go test ./lru ./ring ./pkg/... ./cmd/... ./benchmarks/...
go test -race ./lru ./ring ./pkg/... ./cmd/...
go vet ./pkg/... ./cmd/...
go test ./benchmarks -run '^$' -bench . -benchmem
python benchmarks/load.py --concurrency 1000 --requests 10000
```

Core tests cover concurrent LRU access, TTL expiration, virtual-node remapping,
100 concurrent misses sharing one database read, and write-policy ordering using
an in-memory storage test double. The local Windows cache-hit benchmark measured
70.75 ns/op and zero allocations. This measures memory lookup only.

Docker Engine was unavailable in the local Windows environment. GitHub Actions
runs race checks, real PostgreSQL integration, Compose startup, coordinator and
shard failure/recovery checks, and the HTTP load generator. Inspect the latest
workflow result for the validation status of a given commit.
The performance figures in the supplied lab report have not been reproduced.
The original root integration suite requires generated TLS certificates and is
not included in the core verification command above.

## Upstream

Imported source commit cab597d65b85f00201135d95eec3c41a0b056e6e.
The original MIT license is preserved in LICENSE. Original documentation is in
[docs/UPSTREAM_README.md](docs/UPSTREAM_README.md). The original Go module path is
retained to preserve imports. Consistent hashing provides expected remapping near
1/N under balanced hashing, without a strict worst-case key-count bound.
