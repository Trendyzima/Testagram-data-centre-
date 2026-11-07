#!/usr/bin/env bash
set -euo pipefail

# Both entry points deploy the Appwrite/Neon/B2/R2 fresh stack.
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
exec "$REPO_ROOT/scripts/install-local-docker.sh" "$@"
