# Testagram realtime edge (Cloudflare Durable Objects)

This Worker is a transport layer only. Appwrite remains the identity provider; Backend remains the authorization and canonical data authority; Neon stores durable conversations and messages. Durable Objects coordinate live WebSocket sessions and fan-out, not canonical message history.

## Security contract

- Browser connects to /v1/realtime/conversations/{conversation_id} using the `testagram.realtime.v1` WebSocket subprotocol plus exactly one `ticket.<signed-ticket>` subprotocol. The Worker negotiates only `testagram.realtime.v1`; it never echoes the ticket. Native browser WebSockets cannot set an Authorization header, and tickets must never be placed in query strings.
- Ticket is HMAC-SHA256 signed by Backend and contains issuer, audience, subject, conversation ID, scope, issued-at, expiry and random token ID.
- Tickets are conversation-scoped, expire within five minutes, and are never placed in query strings.
- The Worker accepts only exact configured browser origins.
- Browser WebSockets are receive-only except for ping frames. Message creation/edit/delete must use Backend APIs, where membership, blocks, visibility, idempotency and durable Neon writes are enforced.
- Backend publishes only after its Neon transaction commits, using an HMAC signature over the exact raw event body.
- The Worker rejects unsigned publish requests, streams request bodies with a 64 KiB cap, and limits each user to five concurrent sockets per conversation.
- Events carry stable event_id values. Clients must deduplicate event IDs because Backend retries can result in repeated delivery.
- Do not log tickets, signatures, private message payloads or secrets.

## Secrets

Configure these as Cloudflare Worker secrets, never in wrangler.toml:
- REALTIME_TICKET_SECRET: same random 32+ character secret used by Backend ticket issuance.
- REALTIME_PUBLISH_SECRET: separate random 32+ character secret used by Backend event signing.

Set REALTIME_ALLOWED_ORIGINS to the exact production XClone origins. The committed values are development placeholders and must be reviewed before deployment.

## Deployment status

This Worker is not deployed or routed publicly by this branch. It requires Backend endpoints for ticket issuance, conversation membership authorization, durable message writes and signed event publication. It also requires Cloudflare account binding, secrets, custom-domain routing, abuse controls, metrics and load tests. Do not expose the Worker or claim production realtime readiness until those dependencies are implemented and tested.
