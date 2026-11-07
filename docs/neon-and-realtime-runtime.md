# Neon and Cloudflare realtime runtime contract

## Database placement

The fresh Compose deployment accepts FEDERATION_DATABASE_URL. In production, set it to a dedicated Neon PostgreSQL database using the Neon pooled endpoint and sslmode=require (or verify-full). The local federation-db container URL remains only as a local-development default. Do not point Federation at the Data Centre control-plane database or reuse the Backend database role.

Use separate Neon databases/roles for Backend product data and Federation protocol state, preferably within a deliberately budgeted Neon project if a shared compute budget is desired. Give each service only its own database/schema privileges. Keep credentials in the deployment secret manager; never commit them to vps/fresh.env or CI logs. Neon Free has finite CU-hour, storage and network-transfer limits and is not a production availability guarantee.

## Realtime transport

The realtime Worker under realtime/ uses Cloudflare Durable Objects to coordinate WebSocket sessions and per-conversation fan-out. It is transport-only:
- Backend issues short-lived HMAC-signed conversation tickets after authenticating the Appwrite user and checking conversation membership.
- The Worker verifies ticket issuer, audience, scope, signature, expiry and conversation binding.
- Browser WebSockets are receive-only except ping/pong. Writes must go through Backend.
- Backend commits the message to Neon first, then publishes a signed event to the Worker.
- Durable Objects do not become the source of truth for message history.
- XClone never receives database, Durable Object binding or publish secrets.

The Worker is deliberately not deployed or publicly routed by this branch. Backend ticket issuance, message authorization/write APIs, signed event publisher, Cloudflare secrets/bindings, abuse controls, monitoring and end-to-end tests are required before enabling it.

## Cost and quota safety

Cloudflare Durable Objects Free has a 100,000 requests/day allowance and 100,000 SQL row writes/day. A WebSocket connection requires one request; outgoing WebSocket messages and protocol pings are not billed as requests, while incoming application messages are counted at a 20:1 ratio. The Backend-to-DO publish call consumes a DO request per event. Do not proxy all 15 million monthly application API requests through Durable Objects. Route normal REST traffic to Backend; use Durable Objects only for realtime connection coordination and event fan-out. WebSocket event volume, active connection count, CPU duration and fan-out must be measured under realistic load. The current Worker caps each user at five concurrent sockets per conversation; edge-level connection/rate controls are still required.

Keep the local federation-db service available for development until a Neon staging database has been provisioned, the schema has been applied and a verified dump/restore migration has passed. Switching FEDERATION_DATABASE_URL does not copy existing Federation rows; migrate actors, remote keys, inbox/outbox activities, follower records and delivery leases before any production switch. Never enable the delivery worker solely because the database URL has changed.
