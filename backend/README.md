# Testagram locally owned backend

This layer is the ownership boundary around the official self-hosted Supabase
core. It does not modify Supabase internals.

## Provider-free core

The following capabilities are implemented as Testagram-owned infrastructure,
not as calls to managed media, push, CDN, cache, KYC, GIF, news, notification,
or hosted AI platforms:

- creator video channels, uploads, local storage, FFmpeg processing, HLS playback and signed delivery
- notification broker with device registration, preferences, retries and an internal delivery protocol
- local object/media storage with processing workers and signed asset URLs
- Redis-compatible cache/rate-limit primitives backed by a local service
- local edge gateway policy for TLS, routing, request limits and caching
- RSS/GIF/news ingestion and normalization
- federation through ActivityPub and Matrix protocols
- transactional email queue and provider-independent SMTP delivery interface
- local geospatial/distance primitives and cache
- local AI gateway contracts for moderation, transcription, embeddings and support agents
- payment ledger, idempotency, reconciliation and provider adapters
- KYC orchestration and evidence/audit state

## External networks

Some capabilities inherently terminate at a third-party network. Testagram
owns the integration, credentials, ledger, retries and audit trail, but cannot
turn those external networks into local infrastructure:

- Safaricom M-Pesa
- PayPal
- Pesapal
- remote federation peers
- email recipient domains

These are adapters, not dependencies of the core business logic.

Firebase/FCM is deliberately not part of this architecture.
