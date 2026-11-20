# Testagram Runtime and Repository Boundaries

This document describes the current Testagram-owned architecture. The seven repositories are separate deployable or governance codebases, not separate products. Testagram Backend is the sole authority for user identity, authentication, sessions and product authorization.

## 1. Repositories and authoritative ownership

| Repository | Primary responsibility | Must not become authoritative for |
|---|---|---|
| `testagram-flutter-client-` | Android/web client, UI state, user interactions, device permissions and Testagram-native authentication client | Password authority, canonical identity, private service credentials, product authorization |
| `Testagram-backend-` | Canonical identities, native password/email-verification/recovery flows, PostgreSQL sessions, staff authorization, social graph, posts, visibility, moderation, quotas and API orchestration | Physical storage placement, ActivityPub protocol internals, ranking truth |
| `testagram-api` | OpenAPI, internal contracts, schemas, SDKs, route inventories, immutable source pins and compatibility CI | Runtime requests, identity, product data, authorization or gateway behavior |
| `Testagram-data-centre-` | VPS assembly, TLS/edge routing, compute/storage control plane, media infrastructure, node registration, host operations and backup/restore | Canonical user identity, authentication, social graph, post/media ownership or feed ranking |
| `cdn-` | Authenticated byte delivery, cache behavior, range requests and origin protection | Product visibility, ownership, user identity or authorization decisions |
| `the-algorithm` | Candidate scoring, ranking policy, recommendations and feed-quality evaluation | Canonical post/account data, authentication, visibility permissions or candidate pagination |
| `federated-content` | ActivityPub actors, remote actor cache, inbox/outbox, HTTP signatures, federation delivery and retry state | Canonical local accounts, local post ownership or user authentication |

## 2. Authentication and request flow

- Flutter submits credentials to the Backend native authentication API. The Backend verifies password hashes and account state in PostgreSQL and issues a short-lived access JWT plus an opaque rotating refresh token.
- Flutter stores the refresh credential only in platform secure storage. It never sends a refresh token as a bearer credential and never calls a third-party identity endpoint.
- The Backend validates access-token signature, issuer, audience, expiry, token version, live PostgreSQL session, account status and email-verification state before authorizing protected requests.
- Refresh rotation, replay detection, logout, logout-all and password-reset session revocation are owned by the Backend and persisted in PostgreSQL.
- Staff authorization uses canonical Testagram staff UUID principals, role grants and Backend-verified MFA elevation. WebAuthn enrollment remains closed unless the independent authorization, PostgreSQL schema, credential quorum and private Admin Console readiness attestation all pass.
- Data Centre video and notification services validate the native Backend access token and recheck live session/account state through the native session resolver. They do not accept refresh tokens or external-provider credentials.
- The API contract repository governs these edges at build time only; it is never a runtime hop.

## 3. Data ownership rules

- Backend PostgreSQL is the system of record for local users, password hashes, email verification, sessions, staff principals, social relationships, local posts, reactions, privacy settings, moderation decisions and product-level media associations.
- Federation stores protocol-specific state and remote actor snapshots; local account identity and canonical ownership are resolved by Backend.
- Algorithm stores derived features, aggregates and evaluation artifacts only. It receives bounded Backend-authorized candidate IDs and returns ranking/order; Backend rehydrates and re-authorizes every result before returning a timeline.
- Backend owns media object metadata, owner/visibility relationships, upload intents and quota accounting. Data Centre owns host storage mounts, node health/capacity, physical object placement, checksums, replicas and infrastructure backup/restore; it must not decide who owns a media object or post.
- CDN delivers bytes using Backend-issued viewer assertions and separate origin authentication. CDN does not decide whether a user may view an object.
- API contracts own schemas, generated SDKs, route inventories and compatibility checks. Source pins are immutable integration evidence, not deployment attestations.

## 4. Runtime and service-to-service trust

- Flutter calls only the public Backend API and approved CDN URLs. It never receives database credentials, service tokens, private service URLs or storage-provider secrets.
- Backend-to-Data-Centre, Backend-to-CDN, Backend-to-Algorithm and Backend-to-Federation calls use distinct directional credentials and bounded, versioned contracts.
- User access JWTs and refresh tokens are never reused as service-to-service credentials.
- Every cross-service event has a stable event ID, versioned schema, explicit producer/consumer, bounded payload, timeout, idempotency rule and retry/reconciliation behavior.
- Consumers acknowledge only after durable acceptance, deduplicate by event ID and tolerate retries/replay.
- No external identity-provider endpoint, SDK, bearer token, database, server key or authentication fallback is part of the supported runtime.

## 5. Media durability and placement

- Backend authorizes uploads, reserves logical quota and issues bounded upload/download capabilities.
- Data Centre selects an eligible provider or node, reserves physical capacity, verifies size and SHA-256, and commits replica state only after durable acknowledgement.
- A second replica is valid only after independent read-back and checksum verification. A single removable SD card is not a backup.
- HLS segments, thumbnails and transcodes are derived artifacts; preserve originals and make derived assets reproducible.
- Quotas, minimum-free-space thresholds, signed URLs, range requests, content-type validation and per-account authorization are enforced by Backend and the configured storage/CDN boundary.
- Never acknowledge an upload as durable until the configured durability policy passes. Never treat a missing heartbeat as proof an object was deleted.

## 6. Android Node B safety contract

Android Node B is removable-SD-card-only storage:

- The selected removable SD card is the only media volume. Internal phone storage is not a fallback.
- The card is selected through Android's Storage Access Framework and revalidated on mount, write, read and heartbeat.
- The node advertises `storageOnly=true`; it must never advertise `workloadExecution=true`.
- No transcoding, feed generation, database hosting, AI inference or arbitrary workloads run on the phone.
- Phone work is bounded to storage I/O, signed transfer, health checks and heartbeats. Treat it as intermittently available.
- A missing, replaced, full, read-only or unmounted card makes the node unavailable for new writes. Existing media references remain intact and can be repaired from other replicas/backups.

## 7. Operational acceptance gates

No capability is production-ready solely because it compiles or its source pin exists. Require:

- unit, integration, race and API contract tests on the exact candidate revisions;
- PostgreSQL-backed native account creation, email verification, password login, refresh rotation, replay rejection, logout and recovery;
- independent staff enrollment/approval, real WebAuthn assertion, credential revocation and protected-action MFA elevation;
- readiness checks and bounded resource use for each private service;
- metrics for latency, error rate, queue age, retry count, storage free space and replication health;
- encrypted off-host backups and successful isolated restore drills;
- staging end-to-end tests for login, feed, upload, playback and federation;
- rollback plan and schema compatibility;
- physical Android removable-SD validation before counting phone capacity;
- live DNS, TLS, origin health and route smoke tests before production traffic cutover.

The API governance repository remains build-time only. A green CI run, open pull request, source pin or Compose file does not prove live deployment or end-to-end production acceptance.
