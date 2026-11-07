#!/usr/bin/env bash
set -euo pipefail
required_images=(TESTAGRAM_FRESH_BACKEND_IMAGE TESTAGRAM_ALGORITHM_IMAGE TESTAGRAM_FEDERATION_IMAGE CADDY_IMAGE POSTGRES_CLIENT_IMAGE FEDERATION_POSTGRES_IMAGE)
for name in "${required_images[@]}"; do
  value="${!name:-}"
  if [[ ! "$value" =~ ^[^[:space:]@]+@sha256:[a-fA-F0-9]{64}$ ]]; then
    printf '%s must be an immutable image reference ending in @sha256:<64 hex characters>\n' "$name" >&2
    exit 1
  fi
done
echo "All fresh runtime images are pinned to immutable SHA-256 digests."
