# Testagram Five-Repository Architecture

This document defines ownership boundaries across the five Testagram repositories. The repositories are separate deployable codebases, not separate products.

## 1. Repositories and authoritative ownership

| Repository | Primary responsibility | Must not become authoritative for |
|---|---|---|
| `XClone-...` | Web/mobile client, UI state, user interactions, client-side caching | Credentials, canonical social graph, ranking truth, media object ownership |
| `Testagram-backend-` | Canonical identity integration, social graph, local posts, likes/replies/reposts, permissions, feed orchestration, API gateway and user-facing business rules | ActivityPub protocol internals, physical storage-node scheduling |
| `federated-content` | ActivityPub actors, remote actor cache, inbox/outbox, HTTP signatures, federation delivery and retry state | Canonical local accounts, local post ownership, user authentication |
| `the-algorithm` | Candidate scoring, ranking policy, recommendations and feed-quality evaluation | Canonical post/account data, authentication, visibility permissions, candidate pagination |
| `Testagram-data-centre-` | Compute/storage control plane, node registration, media placement and retrieval, object durability, backup/restore, platform infrastructure | Canonical social graph, local account identity, authoritative post content, feed ranking |

## 2. Request and event flow

- XClone calls only the public Testagram Backend API. It never calls the ranker, control plane, database, or Android node directly.
- Backend authenticates the user, applies blocks/mutes/privacy/visibility rules, gathers candidates, and calls `the-algorithm` over a private service connection.
- Backend hydrates and re-authorizes ranked IDs before returning the timeline. If ranking is unavailable, Backend serves a bounded chronological fallback.
- Backend publishes versioned events through a durable transactional outbox. Federation consumes post/account events; data-centre consumes media lifecycle and storage-placement events; the algorithm consumes permitted interaction and content-feature events.
- Services acknowledge events only after durable acceptance. Consumers must be idempotent and support replay.

## 3. Data ownership rules

- PostgreSQL in the backend is the system of record for local users, social relationships, local posts, reactions, privacy settings and moderation decisions.
- Federation stores protocol-specific state and remote actor snapshots; local account identity is resolved from the backend.
- The algorithm stores derived features, aggregates and evaluation artifacts only. These are rebuildable and must carry source timestamps/version numbers.
- Data-centre stores object metadata, physical/remote object locations, node health, replication/repair jobs and backup manifests. It must not duplicate social records or become the source of truth for who owns a post.
- Media ownership authorization is checked by Backend before issuing a short-lived signed upload/download capability. Data-centre validates that capability and enforces object-level quotas and integrity checks.

## 4. Android Node B safety contract

Android Node B is removable-SD-card-only storage:
- The selected removable SD card is the only media volume. Internal phone storage is not a fallback.
- The card is selected through Android's Storage Access Framework and revalidated on mount, write, read and heartbeat.
- The node advertises `storageOnly=true`; it must never advertise `workloadExecution=true`.
- No FFmpeg transcoding, feed generation, database hosting, AI inference, or arbitrary Docker workloads run on the phone.
- Phone work is bounded to storage I/O, signed transfer, health checks and heartbeats. Treat it as intermittently available.
- Media service access to the phone is through authenticated encrypted outbound/reverse connectivity; never expose the phone's local server directly to the public Internet.
- A missing, replaced, full, read-only or unmounted card causes the node to become unavailable for new writes. Existing media references remain intact and can be repaired from other replicas/backups.

## 5. Media durability and placement

- Original media and metadata must be separated: backend owns media-to-post/user ownership; data-centre owns object locations, checksums, replicas and repair state.
- Never acknowledge an upload as durable until the object has passed checksum verification and the configured durability policy.
- Use storage adapters for local filesystem, S3-compatible services (including Backblaze B2 and Cloudflare R2), and registered SD-card nodes. Provider choice must not leak into backend domain logic.
- A single Android SD card is not a backup. Critical media requires another independent copy and a restore/repair path.
- HLS segments, thumbnails and transcodes are derived artifacts; preserve originals and make derived assets reproducible.
- Quotas, minimum-free-space thresholds, signed URLs, range requests, content-type validation and per-account authorization must be enforced server-side.

## 6. Internal API and event compatibility

Every cross-repository contract must be versioned and documented. Event envelopes include `eventId`, `eventType`, `schemaVersion`, `occurredAt`, `producer`, and `data`. Consumers deduplicate by event ID and tolerate retries and out-of-order delivery.

Minimum contracts to stabilize:
- Backend → algorithm: feed candidate batch, server-derived ranking features, request ID, cursor metadata.
- Backend → federation: local account/post lifecycle events and authoritative actor resolution.
- Backend → data-centre: upload authorization, object ownership reference, retention/deletion intent.
- Data-centre → backend: object availability, checksum, media rendition readiness, node/object degradation.
- Federation → backend: remote activity references and delivery/visibility outcomes, never untrusted canonical account mutations.

Use service-to-service authentication, bounded payloads, timeouts, idempotency keys, retries with backoff, dead-letter/reconciliation paths and contract tests. Never put service secrets in XClone or Android client builds.

## 7. Migration discipline

The data-centre repository currently contains some application-adjacent platform, video and notification services. Treat those as existing implementations, not permission to duplicate backend domain ownership. Move or retire a capability only after its API consumer, data owner, migration, rollback and operational owner are explicit. Do not delete existing Supabase-compatible paths or persistent volumes during boundary cleanup.

## 8. Operational gates

No capability is production-ready solely because it compiles. Require:
- unit, integration, race and API contract tests;
- readiness checks and bounded resource use;
- metrics for latency, error rate, queue age, retry count, storage free space and replication health;
- encrypted off-host backups and successful restore drills;
- staging E2E for login, feed, upload, playback and federation;
- rollback plan and schema compatibility;
- physical Android SD-card validation before counting phone capacity.
