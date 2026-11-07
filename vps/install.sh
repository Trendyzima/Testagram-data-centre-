#!/usr/bin/env bash
set -euo pipefail
ROOT="${TESTAGRAM_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
exec "$ROOT/scripts/install-local-docker.sh" "$@"
