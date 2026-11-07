# Data Centre <-> centralized Backend infrastructure API contract

Data Centre owns runtime and infrastructure: private networking, VPS/edge routing, object-provider access, storage placement/replica health, capacity reservation, node enrollment/heartbeat, infrastructure cache/rate-limit/queue adapters and operational telemetry.

Backend owns canonical Testagram identity, authorization, product records, media ownership, lifecycle decisions and user-visible API responses. Data Centre must never write profiles, posts, follows, likes, messages or canonical user-deletion state. The Backend calls Data Centre through private authenticated, versioned infrastructure APIs; browser clients never receive provider, node, queue or service credentials.

Physical media operations must be idempotent and report provider, object key, bytes, checksum, replica state, request ID and verification timestamp. A placement is not durable until an independent read-back and SHA-256 verification succeeds. A missing node heartbeat is not proof of deletion. Quota is reclaimed only after Backend-authorized cleanup has independently confirmed every tracked replica absent.

The existing `internal/platformclient` is an infrastructure API client contract for cache, rate limits and queues; the full Backend-to-Data-Centre media placement/replica API is still to be implemented. Keep infrastructure clients and tests in this repository; no separate API repository.


## Private media lifecycle API (implementation branch)

Data Centre is the physical storage control plane. Backend remains the only public
product API and the authority for user access decisions. The private API is
authenticated with a dedicated `FRESH_DATA_CENTRE_INTERNAL_TOKEN`, separate from
node bootstrap credentials.

| Method | Route | Purpose |
|---|---|---|
| POST | `/internal/v1/media/upload-url` | Register a tenant-scoped upload and return a short-lived B2 PUT URL to Backend |
| POST | `/internal/v1/media/verify` | Record expected size/type/SHA-256 and queue asynchronous B2 read-back + R2 replication |
| GET | `/internal/v1/media/status` | Read the durable upload/replica state |
| POST | `/internal/v1/media/download-url` | Return a short-lived URL only for a protected, verified object; Backend must authorize first |
| POST | `/internal/v1/media/delete` | Tombstone and retry deletion across both providers |

The verify endpoint returns `202 replication_pending`; Backend must not mark the
asset ready until status becomes `protected`. The control plane claims durable
replication jobs with leases, retries transient failures, dead-letters after 12
attempts, and retries delete-pending objects. Public media and private media use
separate buckets on both providers.

This API is opt-in (`MEDIA_STORAGE_ENABLED=false` by default) and remains a
draft until Backend's media handlers use it instead of direct B2/R2 SDK access,
the public Cloudflare worker is bound to the public R2 bucket, and staging proves
upload, checksum, replica recovery, authorization, playback and deletion.
