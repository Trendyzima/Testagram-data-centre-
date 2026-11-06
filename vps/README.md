# Testagram single-VPS runtime

The VPS is the runtime authority for the complete Testagram backend.

It runs the official self-hosted Supabase Docker stack, Testagram control
plane, video API and FFmpeg worker, notification service, maintenance worker,
local Valkey, and the Testagram Caddy edge. The node-agent can run as a host
systemd service and uses the VPS storage tree for node volumes.

The current self-hosted Supabase Docker distribution uses Envoy as the default
API gateway. The gateway remains private; Testagram Caddy is the public HTTPS edge.

Persistent storage is VPS-local:
- /var/lib/testagram/storage/supabase
- /var/lib/testagram/storage/media
- /var/lib/testagram/storage/nodes
- /var/lib/testagram/supabase for the official Supabase Postgres data/config

Deploy with:
sudo bash vps/install.sh

Never expose Postgres, Supavisor, Redis/Valkey, control-plane, or internal
service ports directly to the Internet.

## Five-repository application runtime

The VPS stack includes the separately released Testagram Backend API and the
private Algorithm ranker on the internal Docker network. Caddy serves the
Backend API at TESTAGRAM_API_DOMAIN (default api.testagram.site); XClone remains
the only product frontend and should set VITE_TESTAGRAM_API_URL to that origin.
The ranker has no public port and accepts only the server-side
ALGORITHM_INTERNAL_TOKEN.

Set TESTAGRAM_BACKEND_IMAGE, TESTAGRAM_ALGORITHM_IMAGE and
ALGORITHM_INTERNAL_TOKEN in the protected VPS environment before starting the
stack. Image tags must refer to successfully published GHCR images. If packages
are private, authenticate Docker to ghcr.io on the VPS using a read-only package
token; never place that token in the repository.

The federation container is available only under the federation-preview Compose
profile and is not routed through Caddy. Its current inbox deliberately returns
501 until HTTP signature verification, durable inbox/outbox and dynamic actor
resolution are complete. Do not expose it publicly or claim production
federation until those gates pass.

The backend uses the same PostgreSQL cluster and validates the configured
identity-provider session. The algorithm has no database credentials and
cannot mutate accounts or posts. Media object credentials remain server-only.
