# Testagram VPS — Supabase integration boundary

This directory does **not** fork, edit, or restructure Supabase. The official
Supabase self-hosted Docker distribution remains the source of truth.

## Boundary

- `prepare.sh` downloads a pinned official `supabase/supabase` self-hosted release.
- The official `docker/` directory is copied verbatim into the VPS data root.
- `docker-compose.vps.yml` is an external Compose override containing only a
  network-name integration. It does not redefine Supabase services.
- Secrets and runtime `.env` state live outside Git.
- `healthcheck.sh` verifies the public API surface.
- `status.sh` validates and reports the resolved Compose stack.

Supabase documents Docker as the recommended self-hosting path and distributes
the self-hosted Docker configuration from the official `supabase/supabase`
repository.

## Gadget communication

The VPS control plane and Supabase are separate boundaries. Gadget nodes
communicate with the Testagram control plane through authenticated outbound
HTTP(S), so phones and laptops do not need an inbound listening port.

1. Gadget registers with the bootstrap token.
2. Control plane issues a per-node token.
3. Gadget sends resource heartbeats.
4. Gadget polls for assigned workloads.
5. Gadget executes workloads where its runtime supports them.
6. Gadget reports success/failure.
7. The control plane persists node/workload state.

The Android node follows the same outbound model. It can register and maintain
a heartbeat while using Android's persisted external-storage selection for its
local data volume. Arbitrary Linux container execution is intentionally not
claimed for Android.

## Security boundary

- Never expose Postgres directly to the Internet.
- Never commit database passwords, JWT secrets, service-role/secret keys, or
  private signing keys.
- Use HTTPS for a remotely reachable control plane and public Supabase API.
- The control plane supports native TLS with `VPS_CONTROL_PLANE_TLS_CERT_FILE` and `VPS_CONTROL_PLANE_TLS_KEY_FILE`; both must be set together.
- Keep service-role/secret credentials server-side.
- Bootstrap credentials are only for enrollment; steady-state gadget traffic
  uses the issued node token.
- Nodes that stop heartbeating are excluded from scheduling.
- Container workloads run read-only, with all Linux capabilities dropped and
  no-new-privileges enabled; digest-pinned images are accepted when supplied.

## Official components

The eight official backend projects are inventoried in `UPSTREAMS.yaml`.
The orchestration repository `supabase/supabase` is additionally used as the
official self-hosted Docker source. None of these upstream projects is edited
by this repository.
