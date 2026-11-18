# Testagram single-VPS runtime

This repository assembles the Testagram-owned Data Centre runtime: the
canonical Go Backend API, private supporting services, storage/media lifecycle,
node control plane, maintenance workers and Caddy HTTPS edge.

## Runtime boundaries

- The canonical Backend owns native authentication, public API routing and
  product database access. It is published as
  `ghcr.io/trendyzima/testagram-backend` and deployed by digest.
- The Backend public listener is port 8080. Its private readiness/internal
  listener is port 8082 and is never published or routed by Caddy.
- The product database is configured with `DATABASE_URL`. The Data Centre
  control-plane database is separate and configured with
  `DATA_CENTRE_DATABASE_URL`.
- The optional Algorithm and Federation services remain private. Their
  credentials are separate for each trust direction.
- The optional Data Centre media service owns B2/R2 credentials and object
  lifecycle. Those provider credentials are not passed into the Backend API.
- Node volumes and storage metadata remain under the configured Testagram
  storage root. Preserve object checksums, metadata and replica inventory.
- Durable asynchronous work is recorded in owning services' PostgreSQL
  outboxes and processed idempotently.

## Deployment

Use the single canonical Compose manifest, `vps/docker-compose.yml`, and
`vps/fresh.env.example` as the environment template. Copy the template to
`vps/fresh.env` on the VPS, set its permissions to 0600, and provide the
canonical Backend image digest, product database URL, native-auth signing
secret, allowed origins, Caddy image digest, Data Centre database URL and
node-control credentials.

The deployment contract and acceptance gates are documented in
[`DEPLOYMENT.md`](FRESH-DEPLOYMENT.md).

Deploy with:

```sh
sudo bash vps/install.sh
```

Never expose PostgreSQL, object-storage administration, the private Backend
listener, or internal service ports directly to the Internet. Publish only
the approved Caddy HTTPS edge and explicitly configured node-agent operations
endpoints.

## TV health monitor

The canonical Backend serves the TV monitor catalogue and health-write routes.
The monitor authenticates using the signed GitHub Actions OIDC identity
restricted in Backend code to the authorized Testagram TV workflow. No
long-lived static monitor token is required. Keep the private Backend listener
on port 8082 inaccessible from public ingress.

## Federation

The Federation service publishes Testagram actors and objects on
`https://testagram.site` only after its release gates pass. The service
remains private on the Docker platform network; it does not publish port 8081.
The Backend Caddy edge is the only public ingress. Do not configure a separate
public Federation hostname or activate Federation workers until the dedicated
database, service credentials, inbox/outbox workers, recipient delivery and
interoperability checks are complete.

## Recovery

Back up PostgreSQL, object storage, node inventory and deployment secrets to an
off-host destination. Test restoring the exact backup into an isolated host,
including native identity/session continuity, private media access and video
playback. Do not perform a production cutover until backup/restore and rollback
have been demonstrated.
