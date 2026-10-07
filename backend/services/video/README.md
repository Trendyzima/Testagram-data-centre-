# Testagram Video Platform

This is the Testagram-owned video plane.

It provides creator channels, direct upload, asynchronous local FFmpeg processing, adaptive HLS output, posters, private/unlisted/public visibility, view de-duplication, likes, comments, subscriptions, publishing, and signed playback.

No managed video platform, CDN, or media SaaS is required.

Authentication accepts Supabase legacy HS256 session JWTs when VIDEO_JWT_SECRET matches the legacy JWT secret. For asymmetric Supabase signing keys, the edge/auth layer should verify the Supabase JWKS and pass authenticated traffic to this service; the service never stores a private signing key.

PostgreSQL stores metadata and durable jobs. Video bytes stay on Testagram-owned VPS/node storage.
