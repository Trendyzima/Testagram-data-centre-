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
