# Testagram computer node agent

The current node agent targets Linux computers. Do not enroll Windows or macOS machines until platform-specific filesystem and container-runtime adapters are implemented and tested.

## Independent capabilities

The agent defaults to both capabilities disabled. A computer can be enrolled as:

- Compute-only: VPS_ENABLE_COMPUTE_NODE=true, with a strict VPS_ALLOWED_WORKLOAD_IMAGES allowlist of immutable image@sha256 references. VPS_ENABLE_STORAGE_NODE=false.
- Storage-only: VPS_ENABLE_STORAGE_NODE=true, VPS_STORAGE_ROOT set to a dedicated absolute directory and VPS_STORAGE_QUOTA_BYTES set to a positive byte quota. VPS_ENABLE_COMPUTE_NODE=false.
- Dual-purpose: enable both capabilities explicitly. Use separate, non-overlapping paths for media storage and compute volumes.

The media path must not be /, the compute volume root, or a parent/child of the compute volume root. Media capacity and free bytes are reported separately as mediaStorageBytes and mediaStorageFreeBytes; they are not inferred from the host's entire filesystem. The agent accounts files beneath the media path without following symlinks. Keep media paths on a dedicated disk or partition where possible, and never use an operating-system or home directory as an unbounded pool.

A computer node's CPU/RAM is advertised for scheduling, but compute is scheduled only when computeNode=true and workloadExecution=true. Storage-only nodes are never scheduled for compute. Node enrollment is not proof that bytes are safely stored; the storage controller must verify each object and replica independently. Current computer-node support reports capability and mounted-volume capacity only; remote media object transport and verified replica placement are not implemented yet, so do not count these bytes toward the usable 150 GB pool.
