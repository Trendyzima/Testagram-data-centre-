#!/usr/bin/env bash
set -euo pipefail
for script in \
  testagram-platform/bootstrap.sh \
  testagram-platform/prepare.sh \
  testagram-platform/status.sh \
  testagram-platform/healthcheck.sh \
  testagram-platform/storage.sh; do
  bash -n "$script"
done
grep -q 'STAGE=.*BASHPID' testagram-platform/prepare.sh
grep -q '^name: testagram-supabase' testagram-platform/docker-compose.vps.yml
grep -q 'name: testagram-supabase$' testagram-platform/docker-compose.vps.yml
grep -q 'testagram-platform' docs/platform/service-catalog.md
grep -q '/readyz' docs/platform/openapi/internal-platform.yaml
grep -q '/v1/cache/set' docs/platform/openapi/internal-platform.yaml
grep -q '/v1/queue/claim' docs/platform/openapi/internal-platform.yaml
grep -q '/v1/audit' docs/platform/openapi/internal-platform.yaml
echo "Testagram platform shell and contract smoke checks: PASS"
