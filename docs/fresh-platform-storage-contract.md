# Fresh Testagram platform — storage contract

This repository owns deployment, storage placement and node operations. Appwrite Auth owns identity and account sessions. Testagram Backend owns authorization and the canonical social/application data plane in Neon PostgreSQL (profiles, posts, follows, blocks, messages, notifications and lifecycle state). Federation owns ActivityPub protocol state; Algorithm owns ranking only. Data Centre does not own user identity or canonical social records.

## 150 GB target and accounting

The target is 150,000,000,000 bytes of logical usable media capacity. Do not claim that capacity until provider accounts/buckets and measured available quotas support it. Count each logical asset once. Track physical copies separately.

- Backblaze B2 is the initial durable origin.
- Cloudflare R2 is a second object provider for explicitly configured overflow or verified replica placement.
- Cloudflare CDN is delivery/cache only; cache bytes never count as durable capacity.
- Android Node B is removable-SD-only, storage-only and intermittently available. It is an optional capacity contributor, not a durable origin; do not count it as verified usable capacity until its object placement protocol, checksums, second-copy policy and restore tests are complete.
- Computer nodes have independent computeNode and storageNode capabilities. A node can be compute-only, storage-only, or explicitly dual-purpose. Its media disk must be a separately configured dedicated path with an explicit byte quota; do not expose the OS disk or compute scratch volume by default. Compute scheduling requires computeNode=true and workloadExecution=true; storage-only nodes are never schedulable for compute.
- Capacity contributions from computer and Android nodes are opt-in, heartbeat-verified and quota-limited. Reserve capacity before writes, report reserved/verified bytes separately, and exclude offline/unverified bytes from available capacity.
- Android internal storage is never a fallback and never silently included in capacity.

## Android Node B requirements

Use the existing Android Node B application and Storage Access Framework flow. The selected tree must map to a mounted removable StorageVolume UUID. Reject primary/internal storage and cloud-provider trees. Recheck volume identity and writability before each placement and heartbeat. If the card is removed, replaced, read-only or inaccessible, immediately stop new placements and fail in-flight writes. A replacement card is a new empty volume identity.

Node B advertises storageOnly=true, storageNode=true, computeNode=false, workloadExecution=false and storageType=removable_sd. The control plane must reject CPU, video transcoding, feed ranking, database and arbitrary compute jobs for this node. Keep the phone as a low-workload storage endpoint; never run FFmpeg or database services on it.

## Computer node enrollment

The current computer-node agent supports Linux hosts. Windows and macOS need their own native disk/container adapters before enrollment. The Go node agent defaults to no contributed storage and no workload execution. Enable compute independently with VPS_ENABLE_COMPUTE_NODE=true and a strict allowlist of immutable image@sha256 references. Enable media storage independently with VPS_ENABLE_STORAGE_NODE=true, VPS_STORAGE_ROOT and a positive VPS_STORAGE_QUOTA_BYTES. The media path must be absolute and disjoint from VPS_VOLUME_ROOT; filesystem root and overlapping paths are rejected. The agent reports media capacity/free bytes separately in its capabilities and calculates used bytes without following symlinks. The hosting inventory/usage API labels these as reported capacity; they are not verified usable bytes until the storage service writes an object, reads it back, verifies SHA-256 and records a healthy replica. A dual-purpose computer is explicitly configured for both capabilities.

## Placement and durability

1. Backend authenticates and authorizes an upload, reserves logical quota and issues a bounded capability.
2. Storage control plane chooses an eligible provider/node and reserves physical capacity.
3. Upload is written to a temporary key/path, then size and SHA-256 are verified.
4. Object metadata and replica state are committed only after durable acknowledgement.
5. Only then may the Backend mark the asset ready.
6. A second replica is considered valid only after independent read-back and checksum verification.

Start with B2 as the single durable origin. Add R2 dual-copy replication only after restore and reconciliation tests. Android SD and computer nodes are optional additional replicas and must never be the only copy. Maintain at least two independently verified physical copies before marking an asset durable. Never treat a missing heartbeat as proof an object was deleted. No automated failover or cleanup until the placement ledger is authoritative and idempotent.

## Operational gates

- B2 and R2 test-bucket upload/read/head/delete/SHA-256 tests.
- Capacity reservation and concurrent quota race tests.
- Replica repair/reconciliation and interrupted-upload tests.
- Node heartbeat authentication, token rotation and revocation.
- Physical Android tests: internal storage rejected, removable card selected, capacity matches, card removal mid-write safe, same-card reinsert, replacement-card identity changes.
- Database and all required object bytes restored from backups.
- Cloudflare cache bypass for private media; immutable versioned public cache keys.

## Private media delivery and node transfer boundary

- The private-media Cloudflare Worker is the intended CDN hostname in front of the private R2 bucket. It validates short-lived HMAC URLs, enforces an exact frontend-origin allowlist, supports byte ranges, and sends `private, no-store` headers. It is not deployed until the bucket binding, route, origins and shared signing secret are configured.
- The Android LocalMediaServer is loopback-only, rejects query-string bearer tokens, limits signed-token lifetime, validates object paths and MIME types, checks free SD-card space, and verifies SHA-256 before retaining an uploaded file. It is a device-local endpoint, not a remote storage transport endpoint.
- Computer nodes currently report dedicated-volume capacity and compute capability; they do not yet accept media object transfers. Reported node bytes must not be included in verified logical capacity until an authenticated transfer protocol, object read-back, SHA-256 verification, replica ledger and recovery path are implemented.


## Media retention, archive and node replica lifecycle

Backend is the canonical retention authority. Data Centre supplies replica inventory and node operation contracts; it must never independently decide that a user's post or media has expired.

- Each node replica record must include object ID, node ID, storage generation/mount identity, relative object key, byte count, SHA-256, verified timestamp, lease/heartbeat and replica state.
- A node copy is eligible for cleanup only after Backend has recorded a verified archive or another required durable replica and issues an authenticated, scoped deletion command. A node must recheck object ID/key and generation before deleting; stale commands must not delete a replacement SD card's contents.
- Android Node B remains removable-SD-only and storage-only. If the SD card is removed, replaced, unmounted, or its generation changes, the node reports the replica unavailable; it must never fall back to internal storage.
- Offline nodes are not assumed deleted. Their replica remains charged/unverified until the node reconnects and confirms deletion or an operator reconciles the physical device. Do not release quota based on an HTTP timeout.
- Node restore is a pull/stream operation initiated by Backend after authorization. Verify SHA-256 and size before promoting the restored copy to a verified replica.
- Cloudflare R2 and CDN are not cold archive by themselves. A real archive destination must be configured and independently verified before hot-copy cleanup is allowed.
- Node cleanup endpoints require authenticated mTLS/HMAC identity, short-lived command leases, idempotency keys, replay protection, and append-only operation results.
- Retention kill switch must stop new deletion commands while allowing safe reads and reconciliation. No node may purge content based on its own local clock.

No node cleanup or archive migration is enabled by this contract.


## Backend retention purge integration (2026-11-07)

The Backend now has a fail-closed retention purge worker. Data Centre node replicas are part of the canonical per-object replica inventory and must never be silently omitted:

- When the Backend changes a media object's lifecycle state to `purging`, node agents must reject new replica reservations, restores, or writes for that object. Backend remains the sole authority for retention eligibility and deletion timing.
- A `provider=node` replica currently blocks Backend quota reclamation. The Backend will not assume an offline node is deleted, and will not release quota until the node's replica is explicitly confirmed absent.
- The future node-delete command must be authenticated, scoped and idempotent. It must bind object ID, node ID, removable-volume identity/generation, object key, expected byte count, SHA-256, command ID and expiry/lease. Recheck the same mounted-volume generation immediately before deletion so a replacement SD card is never affected.
- After deletion, the node agent must verify the exact object is absent from the mounted storage volume and return a durable acknowledgement containing the command ID, volume generation, object ID and result. Backend updates the replica ledger to deleted only after validating that acknowledgement.
- Offline nodes, missing SD cards, mismatched generations, provider timeouts and unverifiable acknowledgements remain charged/unverified and block quota reclamation. Never fall back to Android internal storage.
- Android Node B remains removable-SD-only. Computer nodes may be storage-only, compute-only or both, but retention deletion is never decided by a node.

Do not enable Backend destructive retention until the node-delete acknowledgement path is implemented and exercised in staging, or until all affected objects are proven to have no node replicas. This contract does not authorize any current node deletion.
