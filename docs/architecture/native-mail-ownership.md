# Native mail ownership boundary

Testagram's transactional mail system is owned by the Testagram Backend and runs on the InstaBox VPS.

The Backend owns the PostgreSQL transactional outbox, encrypted message payloads, OTP and recovery templates, direct-to-recipient-MX SMTP transport, STARTTLS, DKIM signing, delivery retries, VERP return paths, bounce correlation and delivery state.

Cloudflare is used for Testagram DNS and the authenticated inbound bounce-feedback Worker. Cloudflare Email Sending is not an outbound transport for Testagram account verification.

The Data Centre owns infrastructure, storage, processing, backups and restore. It must not define, inject, or provision outbound email-provider credentials, SMTP relay endpoints or credentials, sender overrides, provider SDKs, or alternate mail workers. The only mail-related value it may pass to the Backend is the shared encryption key needed to read the Backend-owned outbox; it does not own or configure the transport.

Ordinary account verification is delivered by the Backend's native mail worker. A successful outbox insert or SMTP acceptance is not proof of inbox delivery. Production acceptance requires actual Gmail and Outlook receipt, aligned SPF/DKIM/DMARC, correct provider-managed PTR, outbound TCP/25 reachability, authenticated bounce correlation, retries, key persistence and monitoring.

Future changes must extend the existing Backend mail implementation and its architecture contract. A second mail subsystem or hosted outbound provider is not an approved fallback.
