# Legacy Testagram R2 media Worker (not a production delivery path)

This Worker is retained as a legacy implementation and CI fixture only. It is **not deployed** and must not claim `media.testagram.site`. That hostname belongs exclusively to the Testagram Go CDN, which owns viewer-facing media delivery, authorization enforcement, caching, range/HLS/DASH handling and origin failover.

The production architecture uses Backend-issued authorization and the Go CDN's direct, private S3-compatible origin adapters for Instabox VPS, Cloudflare R2 and Backblaze B2. R2 remains private; no public bucket access or Worker route is required for the Go CDN to use the R2 S3 API.

## Deployment guard

- Keep `workers_dev = false`.
- Do not add a custom domain or route to this legacy Worker.
- Do not attach `media.testagram.site`; it is reserved for the Go CDN.
- Do not set a separate query-string media signing secret for this legacy Worker.
- Any future Worker origin must have a separately approved hostname and implement the canonical Backend assertion contract, not the legacy query-HMAC contract.

This repository file does not modify Cloudflare DNS, R2 bucket access, secrets, or production deployments.
