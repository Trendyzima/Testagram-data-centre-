#!/usr/bin/env bash
set -euo pipefail

# The retired Supabase installer is intentionally not retained as a fallback.
# Both entry points now deploy the same Appwrite/Neon/B2/R2 fresh stack.
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
exec "$REPO_ROOT/scripts/install-local-docker.sh" "$@"
