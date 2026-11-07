# Testagram Cloudflare media Worker

The Worker fronts two separate R2 buckets:
- `MEDIA_BUCKET`: private objects served only through short-lived HMAC-signed `/private/` URLs.
- `PUBLIC_MEDIA_BUCKET`: public immutable media served at `/v1/tenants/{owner}/users/{owner}/{32-hex-id}`, matching Backend's tenant-scoped object-key contract.

## Configure

1. Bind `MEDIA_BUCKET` to the private R2 bucket and `PUBLIC_MEDIA_BUCKET` to the public R2 bucket in `wrangler.toml`. Keep their bucket names distinct.
2. Set `FRONTEND_ORIGINS` to exact comma-separated HTTPS frontend origins. Do not use wildcard origins.
3. Create a random signing secret with at least 32 characters and store the same value in both places:
   - Worker secret: `wrangler secret put MEDIA_URL_SECRET`
   - Backend environment: `CLOUDFLARE_MEDIA_SIGNING_SECRET`
4. Attach a proxied Cloudflare hostname, for example `media.<your-domain>`, to this Worker. Set Backend `MEDIA_PUBLIC_BASE_URL` to `https://media.<your-domain>/v1` and its private-media base URL to the configured `/private` route.
5. Keep the private R2 bucket private. The public bucket is exposed only through the constrained public path in this Worker; do not enable unrestricted bucket access.

## Request and cache policy

- Only GET and CORS preflight are accepted.
- Private URLs expire after at most 120 seconds and are bearer links; do not log query strings.
- Private keys remain constrained to the legacy opaque `v1/<32-hex>` format and private responses use `Cache-Control: private, no-store`.
- Public keys must contain the same owner ID in both tenant path segments and a 32-hex object ID. Public responses use a short shared cache lifetime (5 minutes) and support byte ranges.
- Public and private paths use different R2 bindings. A private object cannot be fetched through the public route.
- Range requests are supported for audio/video seeking.
- Rotate the shared signing secret in the Worker and Backend together. Existing signed links stop working immediately after rotation.

## Status

The Worker source now supports both private signed delivery and tenant-scoped public delivery. It is not deployed or connected to a live Cloudflare zone until the two R2 buckets, hostname, frontend origins, route and secret are configured. Backend must use the Data Centre lifecycle API for verified R2 replication before public media URLs are exposed.
