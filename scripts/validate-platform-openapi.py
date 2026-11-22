#!/usr/bin/env python3
"""Validate Testagram internal and hosting OpenAPI contracts without network access."""
from pathlib import Path

try:
    import yaml
except ImportError as exc:
    raise SystemExit("PyYAML is required: python -m pip install PyYAML") from exc

contracts = [
    (
        Path("docs/platform/openapi/internal-platform.yaml"),
        "Testagram Internal Platform API",
        {
            "/healthz", "/readyz", "/metrics",
            "/v1/config/set",
            "/v1/config/{key}", "/v1/events", "/v1/audit",
        },
    ),
    (
        Path("docs/platform/openapi/hosting-control-plane.yaml"),
        "Testagram Hosting Control Plane API",
        {
            "/healthz", "/readyz", "/v1/hosting/health",
            "/v1/hosting/nodes", "/v1/hosting/usage",
            "/v1/hosting/domains", "/v1/hosting/domains/{slug}/activate",
            "/v1/hosting/domains/{slug}/disable", "/v1/hosting/tls/authorize", "/v1/hosting/dns/records",
            "/v1/hosting/deployments", "/v1/hosting/deployments/{id}",
            "/v1/hosting/backups", "/v1/hosting/backups/{id}/verify",
            "/v1/hosting/recovery/drills",
        },
    ),
    (
        Path("docs/platform/openapi/identity-evidence-v1.yaml"),
        "Testagram Data Centre Identity Evidence API",
        {
            "/internal/v1/identity-evidence/readyz",
            "/internal/v1/identity-evidence/upload-url",
            "/internal/v1/identity-evidence/verify",
            "/internal/v1/identity-evidence/status",
            "/internal/v1/identity-evidence/download-url",
            "/internal/v1/identity-evidence/delete",
        },
    ),
]

for path, expected_title, required in contracts:
    doc = yaml.safe_load(path.read_text(encoding="utf-8"))
    assert doc.get("openapi", "").startswith("3.1."), f"{path}: OpenAPI 3.1 is required"
    assert doc.get("info", {}).get("title") == expected_title, f"{path}: unexpected title"
    paths = doc.get("paths", {})
    missing = sorted(required - set(paths))
    assert not missing, f"{path}: missing endpoints: {missing}"
    for route, operations in paths.items():
        assert isinstance(operations, dict) and operations, f"{path}: no operations for {route}"
        for method, operation in operations.items():
            assert method in {"get", "post", "put", "patch", "delete"}, f"{path}: invalid method {method} on {route}"
            assert isinstance(operation.get("responses"), dict) and operation["responses"], f"{path}: missing responses for {method.upper()} {route}"
            if route.startswith("/v1/") and route not in {"/v1/config/{key}", "/v1/hosting/tls/authorize"}:
                assert operation.get("security", doc.get("security")), f"{path}: internal route lacks declared auth: {method.upper()} {route}"
    print(f"OpenAPI structure validation passed: {path} ({len(paths)} paths)")

    if path.name == "identity-evidence-v1.yaml":
        for route, operations in paths.items():
            assert operations.get("security", doc.get("security")), f"{path}: identity route lacks dedicated service authentication: {route}"
        assert "IdentityEvidenceBearer" in doc.get("components", {}).get("securitySchemes", {}), f"{path}: dedicated identity evidence bearer scheme missing"
        assert "identity-evidence-private" in str(doc), f"{path}: isolated evidence namespace missing"

media_source = Path("backend/services/media/main.go").read_text(encoding="utf-8")
identity_handlers = {
    "/internal/v1/identity-evidence/readyz": "s.identityReady",
    "/internal/v1/identity-evidence/upload-url": "s.uploadURL",
    "/internal/v1/identity-evidence/verify": "s.verify",
    "/internal/v1/identity-evidence/status": "s.status",
    "/internal/v1/identity-evidence/download-url": "s.downloadURL",
    "/internal/v1/identity-evidence/delete": "s.delete",
}
for route, handler in identity_handlers.items():
    if f'mux.HandleFunc("{route}",{handler})' not in media_source:
        raise AssertionError(f"identity-evidence OpenAPI route is not registered by its provider: {route}")
identity_tests = Path("backend/services/media/main_test.go").read_text(encoding="utf-8")
for test in (
    "TestIdentityEvidenceNamespaceRequiresDedicatedRoute",
    "TestIdentityEvidenceReadinessRequiresDedicatedCredential",
    "TestIdentityEvidenceNamespaceIsPrivateAndBoundToApplication",
):
    if test not in identity_tests:
        raise AssertionError(f"identity-evidence security/integrity test missing: {test}")
