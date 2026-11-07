# Testagram private-media Cloudflare Worker

This Worker is the private media delivery path in front of the Cloudflare R2 replica. The Go Backend signs short-lived GET URLs with HMAC-SHA256. The Worker validates the same canonical message (GET, newline, object key, newline, expiry) and reads only opaque v1/<32-hex> object keys from the bound R2 bucket.

## Configure

1. Replace the R2 bucket placeholders in wrangler.toml with the exact private production and preview bucket names.
2. Set FRONTEND_ORIGINS to exact comma-separated HTTPS frontend origins. Do not use wildcard origins.
3. Create a random signing secret with at least 32 characters and store the same value in both places:
   - Worker secret: wrangler secret put MEDIA_URL_SECRET
   - Backend environment: CLOUDFLARE_MEDIA_SIGNING_SECRET
4. Deploy with Wrangler and attach a proxied Cloudflare hostname, for example media.<your-domain>. Set that exact origin in the Backend's CLOUDFLARE_MEDIA_BASE_URL.
5. Keep the R2 bucket private. Do not enable public bucket access.

## Security and cache policy

- Only GET and CORS preflight are accepted.
- URLs expire after at most 120 seconds and are bearer links; do not log query strings.
- Keys are strictly constrained to the backend-generated opaque key format.
- Private responses always use Cache-Control: private, no-store; never cache private media at shared Cloudflare edges.
- Range requests are supported for audio/video seeking.
- This Worker does not make private content public. Public media needs a separate authorization and cache policy.
- Rotate the shared signing secret in the Worker and Backend together. Existing signed links stop working immediately after rotation.

## Status

The Worker source and matching Go signer are implemented. It is not deployed or connected to a live Cloudflare zone until the R2 bucket, hostname, frontend origins and secrets are configured.
