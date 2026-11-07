# Testagram computer node agent

## Independent capabilities

The agent defaults to both capabilities disabled. A computer can be enrolled as:

- **Compute-only:** set `VPS_ENABLE_COMPUTE_NODE=true` and configure a strict `VPS_ALLOWED_WORKLOAD_IMAGES` allowlist of immutable `image@sha256` references. Keep `VPS_ENABLE_STORAGE_NODE=false`.
- **Storage-only:** set `VPS_ENABLE_STORAGE_NODE=true`, configure `VPS_STORAGE_ROOT` as a dedicated absolute directory, and set `VPS_STORAGE_QUOTA_BYTES` to a positive byte quota. Keep `VPS_ENABLE_COMPUTE_NODE=false`.
- **Dual-purpose:** explicitly enable both capabilities. Use separate, non-overlapping paths for media storage and compute volumes.

The media path must not be `/`, the compute volume root, or a parent/child of the compute volume root. Symlink-resolved paths are checked after directory creation. Media capacity and free bytes are reported separately as `mediaStorageBytes` and `mediaStorageFreeBytes`; they are not inferred from the host's entire filesystem. The agent accounts regular files beneath the media path without following symlinks and caches the capacity scan briefly to avoid rescanning large trees on every heartbeat.

A computer node's CPU/RAM is advertised for scheduling, but compute is scheduled only when `workloadExecution=true`, which is enabled only by the explicit compute flag, a container runtime, and a strict immutable image allowlist. Storage-only nodes are never scheduled for compute. Node enrollment is not proof that bytes are safely stored; the storage controller must verify each object and replica independently.

## Example: dual-purpose node

```dotenv
VPS_ENABLE_COMPUTE_NODE=true
VPS_ALLOWED_WORKLOAD_IMAGES=ghcr.io/example/testagram-worker@sha256:<approved-64-hex-digest>
VPS_ENABLE_STORAGE_NODE=true
VPS_STORAGE_ROOT=/srv/testagram-media
VPS_STORAGE_QUOTA_BYTES=20000000000
VPS_VOLUME_ROOT=/var/lib/testagram/compute-volumes
```

Use real approved image digests and paths on the host. Never copy the placeholder digest literally. Keep storage quotas conservative, monitor disk pressure, and leave enough free space for the operating system and node agent.
