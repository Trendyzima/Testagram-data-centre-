# Testagram computer node agent

The current node agent targets Linux computers. Do not enroll Windows or macOS machines until platform-specific filesystem and container-runtime adapters are implemented and tested.

## Independent capabilities

The agent defaults to both capabilities disabled. A computer can be enrolled as:

- Compute-only: VPS_ENABLE_COMPUTE_NODE=true, with a strict VPS_ALLOWED_WORKLOAD_IMAGES allowlist of immutable image@sha256 references. VPS_ENABLE_STORAGE_NODE=false.
- Storage-only: VPS_ENABLE_STORAGE_NODE=true, VPS_STORAGE_ROOT set to a dedicated absolute directory and VPS_STORAGE_QUOTA_BYTES set to a positive byte quota. VPS_ENABLE_COMPUTE_NODE=false.
- Dual-purpose: enable both capabilities explicitly. Use separate, non-overlapping paths for media storage and compute volumes.

The media path must not be /, the compute volume root, or a parent/child of the compute volume root. Media capacity and free bytes are reported separately as mediaStorageBytes and mediaStorageFreeBytes; they are not inferred from the host's entire filesystem. The agent accounts files beneath the media path without following symlinks. Keep media paths on a dedicated disk or partition where possible, and never use an operating-system or home directory as an unbounded pool.

A computer node's CPU/RAM is advertised for scheduling, but compute is scheduled only when computeNode=true and workloadExecution=true. Storage-only nodes are never scheduled for compute. Node enrollment is not proof that bytes are safely stored; the storage controller must verify each object and replica independently. Current computer-node support reports capability and mounted-volume capacity only; remote media object transport and verified replica placement are not implemented yet, so do not count these bytes toward the usable 150 GB pool.

## Authenticated-activity watchdog activation

The optional activation worker is disabled by default. It uses the existing enrolled node token (not the public user session or Backend internal bearer) to claim and acknowledge impulses from the VPS control plane.

Enable it only on a trusted Linux compute node with Docker Compose v2 and the reviewed deployment files mounted locally:

- `VPS_ENABLE_WATCHDOG_ACTIVATION=true`
- `VPS_WATCHDOG_DATA_CENTRE_COMPOSE_FILE=/opt/testagram/data-centre/vps/docker-compose.yml`
- `VPS_WATCHDOG_CDN_COMPOSE_FILE=/opt/testagram/cdn/docker-compose.yml`

The agent advertises `watchdogActivation=true` only when Compose and both absolute compose-file paths are available. The control plane additionally requires a recent heartbeat and this capability before allowing node-token claims.

The service allowlist is compiled into the agent; queue data cannot provide a compose path, project, command, image, environment or arbitrary service name:

| Impulse service | Compose project/service | Profile |
|---|---|---|
| algorithm | testagram-datacentre / algorithm | ranking |
| data-centre | testagram-datacentre / media | media |
| cdn | testagram-cdn / edge | none |
| federation | testagram-datacentre / federation-api | federation |

Activation uses `docker compose up --no-build --pull never -d`; images must already be installed by the normal reviewed deployment pipeline. No `down`, stop or prune operation is issued. Backend, PostgreSQL, authentication, public ingress, control plane and persistent data services are never stopped by idle timers. CDN and Federation are deliberately start-only in this first release because public delivery and inbound federation may arrive without a fresh local user request.

Keep the worker disabled until the matching Backend control-plane migration and node-token claim/ack routes are deployed. Use a dedicated least-privilege VPS node and ensure the compose files and environment files are root-owned and not writable by the application user. This worker does not authorize browser/mobile clients to invoke control-plane routes.
