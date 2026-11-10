# Testagram Video Platform

This is the Testagram-owned video plane.

It provides creator channels, direct upload, asynchronous local FFmpeg processing, adaptive HLS output, posters, private/unlisted/public visibility, view de-duplication, likes, comments, subscriptions, publishing, and signed playback.

No managed video platform, CDN, or media SaaS is required.

Authentication accepts Appwrite session JWTs only. The service validates each JWT against the configured Appwrite Account endpoint and resolves the Appwrite user ID through `testagram_identity.appwrite_accounts` to the canonical Testagram UUID. Unknown or unlinked identities fail closed. No Supabase JWT secret, local JWT decoding, or legacy authentication fallback is supported.

PostgreSQL stores metadata and durable jobs. Video bytes stay on Testagram-owned VPS/node storage.
