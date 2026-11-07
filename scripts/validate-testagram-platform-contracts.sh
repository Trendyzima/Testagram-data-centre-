#!/usr/bin/env bash
set -euo pipefail

for script in vps/backup.sh vps/restore-drill.sh vps/install.sh scripts/install-local-docker.sh; do
  bash -n "$script"
done

grep -qi 'Testagram' docs/platform/service-catalog.md
grep -qi 'self-sustaining' docs/platform/self-sustaining-operations.md
grep -q '/readyz' docs/platform/openapi/internal-platform.yaml
grep -q '/v1/cache/set' docs/platform/openapi/internal-platform.yaml
grep -q '/v1/queue/claim' docs/platform/openapi/internal-platform.yaml
grep -q '/v1/audit' docs/platform/openapi/internal-platform.yaml

echo "Fresh runtime shell and API contract checks: PASS"
