# Testagram VPS — Supabase integration boundary

This directory does not fork, edit, or restructure Supabase. The official
Supabase self-hosted Docker distribution remains the source of truth.

## VPS-owned storage

The VPS is the storage authority for this deployment. Persistent data is kept
on the VPS filesystem rather than Firebase, Cloudinary, Cloudflare, Bunny,
Upstash, or another hosted object store.

Run storage.sh before starting the stack. It creates the VPS-owned storage tree:
- TESTAGRAM_STORAGE_ROOT/supabase — Supabase Storage objects.
- TESTAGRAM_STORAGE_ROOT/media/posts — post media namespace.
- TESTAGRAM_STORAGE_ROOT/media/images — image namespace.
- TESTAGRAM_STORAGE_ROOT/media/videos — video namespace.
- TESTAGRAM_STORAGE_ROOT/media/originals — uploaded originals.
- TESTAGRAM_STORAGE_ROOT/media/hls — generated HLS playlists/segments.
- TESTAGRAM_STORAGE_ROOT/media/posters — generated posters/thumbnails.
- TESTAGRAM_STORAGE_ROOT/media/avatars — profile media.
- TESTAGRAM_STORAGE_ROOT/media/stories — story media.
- TESTAGRAM_STORAGE_ROOT/media/messages — message attachments.
- TESTAGRAM_STORAGE_ROOT/media/attachments — general attachments.
- TESTAGRAM_STORAGE_ROOT/media/tmp — transient processing data.
- TESTAGRAM_STORAGE_ROOT/nodes — node-agent workload volumes.

The VPS is the filesystem authority. Applications never receive arbitrary host-path access; they use explicit mounts or the authenticated Supabase Storage API. Logical object keys map into this persistent local backing store.

The generated Supabase Compose override bind-mounts the VPS supabase directory
into the official storage and imgproxy services and forces Storage to use its
file backend. Supabase officially supports local filesystem storage through a
bind-mounted directory.

The VPS therefore owns the disk; Supabase Storage consumes that disk locally.
No public filesystem port is opened and Postgres remains private.

## Boundary

- prepare.sh downloads a pinned official supabase/supabase self-hosted release.
- The official docker/ directory is copied verbatim into the VPS data root.
- docker-compose.vps.yml is an external Compose override for storage/network integration.
- Secrets and runtime .env state live outside Git.
- healthcheck.sh verifies the public API surface.
- status.sh validates and reports the resolved Compose stack.

## Gadget/VPS communication

1. A gadget registers with the bootstrap token.
2. The control plane issues a per-node token.
3. The gadget sends resource/storage heartbeats.
4. The gadget polls for assigned workloads.
5. It executes workloads where its runtime supports them.
6. It reports success/failure.
7. The control plane persists node/workload state.

Android nodes use their persisted external-storage selection for their local
data volume. Arbitrary Linux container execution is intentionally not claimed
for Android.

## Security boundary

- Never expose Postgres directly to the Internet.
- Never commit database passwords, JWT secrets, service-role/secret keys, or private signing keys.
- Use HTTPS for a remotely reachable control plane and public Supabase API.
- The control plane supports native TLS with VPS_CONTROL_PLANE_TLS_CERT_FILE and VPS_CONTROL_PLANE_TLS_KEY_FILE.
- Keep service-role/secret credentials server-side.
- Bootstrap credentials are only for enrollment; steady-state gadget traffic uses the issued node token.
- Nodes that stop heartbeating are excluded from scheduling.
- Container workloads run read-only, with Linux capabilities dropped and no-new-privileges enabled; digest-pinned images are required.
- Do not expose the VPS storage directory through a generic file server. Supabase Storage remains the authenticated object API.

## Official components

The eight official backend projects are inventoried in UPSTREAMS.yaml.
The orchestration repository supabase/supabase is additionally used as the
official self-hosted Docker source. None of these upstream projects is edited
by this repository.
