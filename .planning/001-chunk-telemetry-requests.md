# Chunk telemetry requests

## Problem

The agent currently serializes every signal collected during a metrics interval into one HTTP request. In the development cluster this request exceeds the Collector's 1 MiB Fastify body limit and is rejected with HTTP 413. Kubernetes events still arrive because their batches are small, while all metric snapshots are dropped before Kafka and ClickHouse.

## Goal

Keep every agent request comfortably below the Collector limit without raising server memory exposure or losing signal order.

## Acceptance criteria

- telemetry is split using the actual serialized JSON size;
- every request is at most 750,000 bytes by default;
- signal order and total signal count are preserved across requests;
- requests are sent sequentially and failures identify the affected batch;
- a single signal larger than the limit fails before any request is sent;
- Go formatting, vet, race tests, and Helm validation pass.

## Phases

### Phase 0 — diagnosis

Confirmed recurring `413 Request body is too large` responses from the Collector. The Collector accepts 1,048,576-byte bodies, while ingress already permits 20 MiB. The agent sends one unbounded `TelemetryBatch` per collection interval.

### Phase 1 — bounded batching

Add byte-accurate batching in the sender with a conservative 750,000-byte default and sequential delivery.

### Phase 2 — regression tests

Verify request size, ordering, completeness, headers, failure context, and oversized-signal handling.

### Phase 3 — validation

Run `gofmt`, `go vet`, `go test -race`, Helm lint, and Helm template.

## Risks and non-goals

- Partial delivery remains possible when a later HTTP request fails; successful batches are not replayed by this change.
- Compression and persistent on-disk retries are out of scope.
- The Collector body limit remains unchanged.

## Implementation log

- 2026-07-29: Added byte-accurate request batching with a 750,000-byte default, sequential delivery, oversized-signal rejection, and batch-position error context.
- 2026-07-29: Added transport-level regression tests for request size, authorization, ordering, completeness, oversized signals, and partial-send failure reporting.
- 2026-07-29: `gofmt`, `go vet ./...`, and `go test -race ./...` pass. Helm validation could not run locally because the Helm CLI is not installed.
