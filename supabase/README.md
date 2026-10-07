# Testagram VPS — Official Supabase Backend

This directory makes the Testagram data-centre VPS ready to host a self-managed Supabase backend.

## Official backend set

The VPS tracks these eight upstream projects:

1. `supabase/postgres`
2. `supabase/auth`
3. `PostgREST/postgrest`
4. `supabase/realtime`
5. `supabase/storage`
6. `supabase/edge-runtime`
7. `supabase/postgres-meta`
8. `supabase/supavisor`

They remain upstream projects; this repository does not pretend to be an official Supabase fork. The authoritative list and intended role of each service are in `UPSTREAMS.yaml`.

Supabase's official self-hosting architecture places Auth, PostgREST, Realtime, Storage, Edge Runtime, postgres-meta, Studio and the other services around a single Postgres database, with Supavisor providing connection pooling. The Testagram VPS uses the same backend principle while keeping the VPS control plane separate. See the upstream architecture documentation before changing service wiring.

## Files

- `UPSTREAMS.yaml` — exact upstream repository inventory.
- `bootstrap.sh` — validates Git availability, clones/updates all eight upstream repositories into the VPS data root, and records the checked-out refs.
- `healthcheck.sh` — verifies the expected local Supabase endpoints when the stack is running.
- `docker-compose.vps.yml` — VPS-side integration layer; secrets are supplied through the environment and never committed.

## Deployment model

The VPS is the host. Supabase is a managed stack inside it.

- Postgres owns durable relational data.
- Auth owns users, sessions and JWT issuance.
- PostgREST exposes database APIs.
- Realtime handles WebSockets/change streams.
- Storage handles files/objects.
- Edge Runtime executes Edge Functions.
- postgres-meta supplies database metadata operations.
- Supavisor pools database connections.

The application repos should consume this backend through the configured Supabase URL and keys; they should not each carry an independent database.

## Security rules

- Do not expose Postgres directly to the public Internet.
- Do not commit `JWT_SECRET`, database passwords, service-role keys or private signing keys.
- Put TLS in front of public Supabase APIs.
- Keep service-role credentials server-side.
- Treat Edge Functions and database migrations as deployable code and audit them before production.
