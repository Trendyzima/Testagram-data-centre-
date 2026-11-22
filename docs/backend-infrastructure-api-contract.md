# Data Centre <-> centralized Backend infrastructure API contract

Data Centre owns runtime and infrastructure: private networking, VPS/edge routing, object-provider access, storage placement/replica health, capacity reservation, node enrollment/heartbeat, infrastructure cache/rate-limit/queue adapters and operational telemetry.

Backend owns canonical Testagram identity, authorization, product records, media ownership, lifecycle decisions and user-visible API responses. Data Centre must never write profiles, posts, follows, likes, messages or canonical user-deletion state. The Backend calls Data Centre through private authenticated, versioned infrastructure APIs; browser clients never receive provider, node, queue or service credentials.

Physical media operations must be idempotent and report provider, object key, bytes, checksum, replica state, request ID and verification timestamp. A placement is not durable until an independent read-back and SHA-256 verification succeeds. A missing node heartbeat is not proof of deletion. Quota is reclaimed only after Backend-authorized cleanup has independently confirmed every tracked replica absent.

The Data Centre media service exposes a narrow, versioned media lifecycle API for upload grants, verification, status, download grants and deletion. Its provider contract is published in `docs/platform/openapi/media-lifecycle-v1.yaml`.

Identity evidence is a separate private namespace and trust boundary, documented in `docs/platform/openapi/identity-evidence-v1.yaml`. It uses `IDENTITY_EVIDENCE_INTERNAL_TOKEN`, which must be distinct from `MEDIA_INTERNAL_TOKEN`, and dedicated B2/R2 identity-evidence buckets. The six `/internal/v1/identity-evidence/*` routes reuse the media lifecycle handlers but require the identity-evidence bearer, enforce the `identity-evidence-private` namespace, bind object keys to the owning application, and fail readiness unless the database plus both evidence stores pass write/read/delete probes. Backend remains responsible for staff authentication, application authorization and retention decisions.

The existing `internal/platformclient` is a separate infrastructure API client contract for cache, rate limits and queues. Do not treat that platform client, the media lifecycle API, and the identity-evidence API as interchangeable. Full generic Backend-to-Data-Centre platform orchestration remains a separate integration gate. Keep infrastructure clients and tests in this repository; no separate API repository.
