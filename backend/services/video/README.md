# Testagram Video Platform

This is the Testagram-owned video plane.

It provides creator channels, direct upload, asynchronous local FFmpeg processing, adaptive HLS output, posters, private/unlisted/public visibility, view de-duplication, likes, comments, subscriptions, publishing, and signed playback.

No managed video platform, CDN, or media SaaS is required.

Authentication is Testagram-native only. The service validates Testagram HS256 access tokens using the exact server-side `TESTAGRAM_AUTH_JWT_SECRET` shared with the Backend, then checks the live session, token version, verified/enabled account and approved-registration state in PostgreSQL before accepting the canonical user UUID. The Backend auth migrations must be applied and the video service database role must have read access to `testagram_auth` and `testagram_identity.native_registration_provisioning`. Invalid tokens and unavailable session state fail closed; there is no external identity-provider fallback.

PostgreSQL stores metadata and durable jobs. Video bytes stay on Testagram-owned VPS/node storage.
