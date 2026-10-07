# Testagram Platform Core

Testagram-owned internal platform layer for cache, queues, rate limiting, feature configuration and platform telemetry.

- Valkey-backed TTL cache.
- Atomic fixed-window rate limiting.
- Durable Redis Streams queues with consumer groups.
- Public, non-secret feature configuration backed by PostgreSQL.
- Prometheus-compatible counters.
- Readiness checks for both PostgreSQL and Valkey.

The service is private-network only. Internal endpoints require `Authorization: Bearer $PLATFORM_INTERNAL_TOKEN`.

Queue delivery is at-least-once. Consumers must be idempotent and acknowledge only after successful processing.
