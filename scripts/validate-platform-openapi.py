#!/usr/bin/env python3
"""Validate the OpenAPI document structure without making network calls."""
from pathlib import Path
import sys

try:
    import yaml
except ImportError as exc:
    raise SystemExit("PyYAML is required: python -m pip install PyYAML") from exc

path = Path("docs/platform/openapi/internal-platform.yaml")
doc = yaml.safe_load(path.read_text(encoding="utf-8"))
assert doc.get("openapi", "").startswith("3.1."), "OpenAPI 3.1 is required"
assert doc.get("info", {}).get("title") == "Testagram Internal Platform API"
paths = doc.get("paths", {})
required = {
    "/healthz", "/readyz", "/metrics",
    "/v1/cache/get", "/v1/cache/set", "/v1/cache/delete",
    "/v1/rate-limit/check", "/v1/queue/enqueue", "/v1/queue/claim",
    "/v1/queue/ack", "/v1/queue/reclaim", "/v1/config/set",
    "/v1/config/{key}", "/v1/events", "/v1/audit",
}
missing = sorted(required - set(paths))
assert not missing, f"Missing documented endpoints: {missing}"
for route, operations in paths.items():
    assert isinstance(operations, dict) and operations, f"No operations for {route}"
    for method, operation in operations.items():
        assert method in {"get", "post", "put", "patch", "delete"}, f"Invalid method {method} on {route}"
        assert isinstance(operation.get("responses"), dict) and operation["responses"], f"Missing responses for {method.upper()} {route}"
        if route.startswith("/v1/") and route != "/v1/config/{key}":
            assert operation.get("security"), f"Internal route lacks declared auth: {method.upper()} {route}"
print(f"OpenAPI structure validation passed: {len(paths)} paths")
