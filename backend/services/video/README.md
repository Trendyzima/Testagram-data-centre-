# Testagram Video Platform

This is the Testagram-owned video plane.

It provides creator channels, direct upload, asynchronous local FFmpeg processing, adaptive HLS output, posters, private/unlisted/public visibility, view de-duplication, likes, comments, subscriptions, publishing, and signed playback.

No managed video platform, CDN, or media SaaS is required.

Authentication accepts only short-lived Testagram-native HS256 access tokens.
The service verifies signature, fixed issuer/audience, role, UUID subject/session,
token lifetime and version, then checks the live PostgreSQL session, revocation,
account status, verified email and approved-registration state. It uses the
exact `TESTAGRAM_AUTH_JWT_SECRET` shared with Backend. Foreign issuers and
external identity-provider fallback paths are rejected.

PostgreSQL stores metadata and durable jobs. Video bytes stay on Testagram-owned VPS/node storage.
