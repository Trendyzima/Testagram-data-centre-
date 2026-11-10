# Testagram Data Centre

This repository owns Testagram's self-hosted data, media and infrastructure
services. It is not a Supabase distribution and does not require Supabase,
Redis/Valkey, Upstash, or a generic cache/queue service.

## Ownership and runtime boundaries

- Appwrite owns client authentication. Video and notification services verify
  Appwrite session JWTs with the Appwrite Account API, then resolve the
  canonical Testagram UUID through the protected identity bridge.
- PostgreSQL is the only relational persistence dependency. Point
  `DATABASE_URL` at the Testagram-owned PostgreSQL deployment; do not use a
  Supabase database URL.
- Video API and FFmpeg worker own video upload, quota, processing and playback.
- Notification API owns device registration, notification preferences and
  delivery state.
- Platform API owns PostgreSQL-backed feature configuration, event telemetry
  and audit records only. It does not expose cache, Redis, Valkey or queue APIs.
- Storage and node agents own object placement, checksums, replica inventory,
  removable-SD safety and capacity reporting.
- Control plane owns node/workload inventory and hosting operations.
- Durable asynchronous work remains in the owning service's PostgreSQL
  outbox/lease tables and is processed idempotently.

## External networks

Some capabilities inherently terminate at third-party networks. Testagram owns
the integration, credentials, ledger, retries and audit trail:

- Safaricom M-Pesa
- PayPal and Pesapal
- remote federation peers
- email recipient domains

These are explicit adapters, not dependencies of the core business logic.
