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

The optional activity worker is disabled unless `VPS_ENABLE_ACTIVITY_WATCHDOG=true`. Enable it only on the designated activation VPS after the Backend control-plane PR and migration are deployed.

Required settings in `/etc/testagram/node-agent.env`:

- `VPS_WATCHDOG_CONTROL_URL`: Backend control-plane loopback origin, normally `http://127.0.0.1:8788` (the Backend Compose stack binds this port to loopback only).
- `VPS_WATCHDOG_TOKEN`: dedicated 32+ character secret matching Backend `VPS_WATCHDOG_TOKEN`; distinct from `VPS_NODE_BOOTSTRAP_TOKEN` and node registration token.
- `VPS_WATCHDOG_DATA_CENTRE_COMPOSE_FILE`: absolute path to the deployed Data Centre `backend/docker-compose.yml`.
- `VPS_WATCHDOG_BACKEND_COMPOSE_FILE`: absolute path to the deployed Backend `deploy/self-hosted/docker-compose.yml`.

The worker claims up to ten durable impulses at a time, starts only compiled-in service targets, waits for container health, then acknowledges the event. It never accepts Compose paths, service names, profiles, commands, images or environment values from the queue. It uses `docker compose up -d --no-deps --no-recreate --pull never`; no dependency migrations are run and live containers are not recreated.

The fixed target map is: Algorithm → `algorithm`; post/notification processing → `worker` and `notification`; media processing → `media` and `video-worker`; federation → `federation-api`; CDN recovery → Backend `media-cache`. The media profile is activated with `--no-deps`, so its migration dependency is not run by user activity. Databases, public edge, API, ingress and the node agent are never stopped by this worker.

The systemd unit grants access only to the existing Docker socket and existing Testagram paths. Verify the actual Docker group/socket ownership on the VPS before enabling. Service leases are advisory; this version deliberately does not stop services on lease expiry until queue-drain and in-flight-work safety checks are implemented.
