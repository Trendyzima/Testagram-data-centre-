# Testagram Video Platform

This is the Testagram-owned video plane.

It provides creator channels, direct upload, asynchronous local FFmpeg processing, adaptive HLS output, posters, private/unlisted/public visibility, view de-duplication, likes, comments, subscriptions, publishing, and signed playback.

No managed video platform, CDN, or media SaaS is required.

Authentication is provider-explicit and fail-closed. `TESTAGRAM_AUTH_PROVIDER=appwrite` (the migration-safe default) validates Appwrite session JWTs against the configured Account endpoint and resolves the linked canonical Testagram UUID. `TESTAGRAM_AUTH_PROVIDER=native` validates Testagram HS256 access tokens with the exact `TESTAGRAM_AUTH_JWT_SECRET` used by Backend, then checks the session, token version, verified/enabled account and approved-registration state in PostgreSQL before accepting the canonical user UUID. Native mode requires the Backend auth migrations to have been applied and the video service to use a database role with read access to `testagram_auth` and `testagram_identity.native_registration_provisioning`. Invalid provider values, invalid tokens and unavailable session state fail closed; there is no cross-provider fallback.

PostgreSQL stores metadata and durable jobs. Video bytes stay on Testagram-owned VPS/node storage.
