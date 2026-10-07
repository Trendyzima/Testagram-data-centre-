#!/usr/bin/env bash
set -euo pipefail
BASE="${SUPABASE_PUBLIC_URL:-http://127.0.0.1:8000}"
fail=0
check() {
  name="$1"; url="$2"
  if curl -fsS --max-time 8 "$url" >/dev/null; then echo "OK   $name $url"; else echo "FAIL $name $url"; fail=1; fi
}
check "gateway" "$BASE/"
check "auth" "$BASE/auth/v1/health"
check "rest" "$BASE/rest/v1/"
check "storage" "$BASE/storage/v1/status"
check "realtime" "$BASE/realtime/v1/"
if [ "$fail" -ne 0 ]; then echo "Supabase healthcheck failed"; exit 1; fi
echo "Supabase public healthchecks passed"
