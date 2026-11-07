# Data Centre <-> centralized Backend infrastructure API contract

Data Centre owns runtime and infrastructure: private networking, VPS/edge routing, object-provider access, storage placement/replica health, capacity reservation, node enrollment/heartbeat, infrastructure cache/rate-limit/queue adapters and operational telemetry.

Backend owns canonical Testagram identity, authorization, product records, media ownership, lifecycle decisions and user-visible API responses. Data Centre must never write profiles, posts, follows, likes, messages or canonical user-deletion state. The Backend calls Data Centre through private authenticated, versioned infrastructure APIs; browser clients never receive provider, node, queue or service credentials.

Physical media operations must be idempotent and report provider, object key, bytes, checksum, replica state, request ID and verification timestamp. A placement is not durable until an independent read-back and SHA-256 verification succeeds. A missing node heartbeat is not proof of deletion. Quota is reclaimed only after Backend-authorized cleanup has independently confirmed every tracked replica absent.

The existing `internal/platformclient` is an infrastructure API client contract for cache, rate limits and queues; the full Backend-to-Data-Centre media placement/replica API is still to be implemented. Keep infrastructure clients and tests in this repository; no separate API repository.


## Versioned transport and client behavior

All private platform calls use API version `1`, carry a unique `X-Request-ID`, and expect the server to echo both headers. The client accepts only HTTPS or a private Docker/VPC hostname over HTTP, requires a strong service token, disables redirects, bounds response bodies, and applies a short request timeout. Never expose this API or its bearer token to XClone/browser clients.
