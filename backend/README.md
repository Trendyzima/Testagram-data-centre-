# Testagram locally owned backend

This layer is the ownership boundary around the official self-hosted Supabase
core. It does not modify Supabase internals.

## Ownership model

Testagram owns the operational implementations for:

- notification broker and device delivery adapters
- media/object pipeline and signed asset delivery
- Redis-compatible cache, rate limiting and queues
- edge gateway policy
- OAuth/Google integration credentials and token lifecycle
- KYC orchestration and audit records
- payment ledger, idempotency, reconciliation and provider adapters
- RSS/GIF/news ingestion and normalization
- ActivityPub federation and Matrix bridging
- email queue/templates/provider adapters
- geospatial primitives
- AI gateway, moderation, transcription and support runtime

External providers remain protocol/provider integrations where the capability
cannot physically be local (for example Safaricom M-Pesa, PayPal, Pesapal and
Google/YouTube).

The goal is provider independence: removing an external provider must not
remove Testagram's core data model, queues, audit trail, ledger, media metadata,
notification state or business logic.
