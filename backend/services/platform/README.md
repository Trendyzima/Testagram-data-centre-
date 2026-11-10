# Testagram Platform Core

Testagram-owned internal platform layer for feature configuration and platform telemetry/audit records.

- PostgreSQL-backed public, non-secret feature configuration.
- PostgreSQL-backed event and audit records.
- Prometheus-compatible write counters.
- Readiness checks against PostgreSQL only.

The service is private-network only. Internal endpoints require
`Authorization: Bearer $PLATFORM_INTERNAL_TOKEN`.

This service intentionally has no cache, Redis/Valkey, Upstash, or queue dependency.
Durable asynchronous work is owned by the service that owns the corresponding
transactional outbox and uses PostgreSQL-backed leases there; do not add a generic
cache or queue abstraction as a substitute.
