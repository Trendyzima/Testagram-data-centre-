# Fresh Testagram platform — storage contract

This repository owns deployment, storage placement and node operations. The new product uses Appwrite Auth and Appwrite Database as the identity and canonical application-data layer. It does not own user profiles, posts or messages.

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

The Go node agent defaults to no contributed storage and no workload execution. Enable compute independently with VPS_ENABLE_COMPUTE_NODE=true and a strict allowlist of immutable image@sha256 references. Enable media storage independently with VPS_ENABLE_STORAGE_NODE=true, VPS_STORAGE_ROOT and a positive VPS_STORAGE_QUOTA_BYTES. The media path must be absolute and disjoint from VPS_VOLUME_ROOT; filesystem root and overlapping paths are rejected. The agent reports media capacity/free bytes separately in its capabilities and calculates used bytes without following symlinks. The hosting inventory/usage API labels these as reported capacity; they are not verified usable bytes until the storage service writes an object, reads it back, verifies SHA-256 and records a healthy replica. A dual-purpose computer is explicitly configured for both capabilities.

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
