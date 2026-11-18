#!/usr/bin/env python3
"""Check the private service edges across the canonical Backend and Data Centre Compose files."""

from pathlib import Path
import os
import sys
import yaml


def fail(message: str) -> None:
    print(f"ERROR: {message}", file=sys.stderr)
    raise SystemExit(1)


def load(path: str) -> dict:
    try:
        return yaml.safe_load(Path(path).read_text())
    except Exception as exc:
        fail(f"cannot parse {path}: {exc}")


def service(compose: dict, name: str) -> dict:
    value = compose.get("services", {}).get(name)
    if not isinstance(value, dict):
        fail(f"Compose service {name!r} is missing")
    return value


def env(compose: dict, name: str, key: str) -> str:
    value = service(compose, name).get("environment", {}).get(key)
    if not isinstance(value, str) or not value:
        fail(f"{name}.{key} must be an explicit Compose environment mapping")
    return value


def require(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


backend = load("backend-source/deploy/self-hosted/docker-compose.yml")
dc = load("backend/docker-compose.yml")

api = service(backend, "api")
algorithm = service(dc, "algorithm")
federation = service(dc, "federation-api")
media = service(dc, "media")
media_migrate = service(dc, "media-migrate")

# Backend -> Algorithm: the caller and ranker consume the same dedicated secret.
require(env(backend, "api", "ALGORITHM_URL") == "${ALGORITHM_URL:-http://algorithm:8092}",
        "Backend ranking URL must default to the private algorithm service")
require(env(backend, "api", "ALGORITHM_INTERNAL_TOKEN") == "${ALGORITHM_INTERNAL_TOKEN:-}",
        "Backend must receive only its dedicated Algorithm service token")
require(env(dc, "algorithm", "INTERNAL_TOKEN") == "${ALGORITHM_INTERNAL_TOKEN:?Set ALGORITHM_INTERNAL_TOKEN in backend/.env}",
        "Algorithm must validate the same ALGORITHM_INTERNAL_TOKEN")
require("platform" in api.get("networks", []), "Backend API must join the private platform network")
require("platform" in algorithm.get("networks", []), "Algorithm must join the private platform network")
require("8092" in algorithm.get("expose", []), "Algorithm must expose its private listener")
require("ports" not in algorithm, "Algorithm must not publish a host port")
require(os.environ.get("ALGORITHM_INTERNAL_TOKEN", "").strip() and
        len(os.environ["ALGORITHM_INTERNAL_TOKEN"].strip()) >= 32,
        "CI must provide a non-empty, sufficiently long Algorithm service token")

# Backend <-> Federation uses two independent directions and two distinct tokens.
require(env(backend, "api", "FEDERATION_INTERNAL_URL") == "${FEDERATION_INTERNAL_URL:-http://federation-api:8081}",
        "Backend must call the private federation-api service")
require(env(backend, "api", "FEDERATION_INTERNAL_TOKEN") == "${FEDERATION_INGRESS_TOKEN:?Set FEDERATION_INGRESS_TOKEN in .env}",
        "Backend must use its dedicated Federation ingress credential")
require(env(dc, "federation-api", "FEDERATION_INGRESS_TOKEN") == "${FEDERATION_INTERNAL_TOKEN:?Set FEDERATION_INTERNAL_TOKEN in backend/.env}",
        "Federation must receive its ingress credential from the matching Data Centre variable")
require(env(dc, "federation-api", "FEDERATION_INBOX_ACCEPT_ENABLED") == "${FEDERATION_INBOX_ACCEPT_ENABLED:-false}",
        "Public inbound ActivityPub acceptance must be an explicit opt-in")
require(env(dc, "federation-api", "FEDERATION_INBOX_WORKER_ENABLED") == "${FEDERATION_INBOX_WORKER_ENABLED:-false}" and
        env(dc, "federation-api", "FEDERATION_DELIVERY_WORKER_ENABLED") == "${FEDERATION_DELIVERY_WORKER_ENABLED:-false}",
        "Federation inbox and delivery workers must remain opt-in")
require(os.environ.get("FEDERATION_INBOX_ACCEPT_ENABLED", "false").lower() == "false",
        "CI must keep public inbound ActivityPub acceptance disabled")
require(env(backend, "api", "FEDERATION_BACKEND_INTERNAL_TOKEN") == "${FEDERATION_BACKEND_INTERNAL_TOKEN:?Set FEDERATION_BACKEND_INTERNAL_TOKEN in .env}",
        "Backend must receive its separate Federation-to-Backend credential")
require(env(dc, "federation-api", "BACKEND_INTERNAL_TOKEN") == "${FEDERATION_BACKEND_INTERNAL_TOKEN:?Set FEDERATION_BACKEND_INTERNAL_TOKEN in backend/.env}",
        "Federation must use the separate Backend credential")
require(os.environ.get("FEDERATION_INGRESS_TOKEN") == os.environ.get("FEDERATION_INTERNAL_TOKEN") and
        len(os.environ.get("FEDERATION_INGRESS_TOKEN", "")) >= 32,
        "CI fixtures must prove Backend FEDERATION_INGRESS_TOKEN equals Data Centre FEDERATION_INTERNAL_TOKEN")
require(os.environ.get("FEDERATION_BACKEND_INTERNAL_TOKEN", "").strip() and
        len(os.environ["FEDERATION_BACKEND_INTERNAL_TOKEN"].strip()) >= 32,
        "CI must provide the independent Federation-to-Backend credential")
require(env(dc, "federation-api", "BACKEND_INTERNAL_URL") == "http://api:8082",
        "Federation must call the Backend private listener")
require(env(backend, "api", "INTERNAL_LISTEN_ADDR") == ":8082",
        "Backend must bind the private service listener on :8082")
require("platform" in federation.get("networks", {}), "Federation must join the private platform network")
require("8081" in federation.get("expose", []), "Federation must expose only its private listener")
require("ports" not in federation, "Federation must not publish a host port")

# Backend -> Data Centre media: service token, API URL and independent media ledger.
require(env(backend, "api", "DATA_CENTRE_MEDIA_API_URL") == "${DATA_CENTRE_MEDIA_API_URL:-}",
        "Backend must expose an explicit Data Centre media API URL setting")
require(env(backend, "api", "FRESH_DATA_CENTRE_INTERNAL_TOKEN") == "${FRESH_DATA_CENTRE_INTERNAL_TOKEN:-}",
        "Backend must use the dedicated Data Centre media token")
require(env(dc, "media", "MEDIA_INTERNAL_TOKEN") == "${FRESH_DATA_CENTRE_INTERNAL_TOKEN:-}",
        "Data Centre media must receive the same dedicated service token")
require(env(dc, "media", "MEDIA_DATABASE_URL") == "${MEDIA_DATABASE_URL:-}",
        "Data Centre media must use its separate media database")
require(env(dc, "media-migrate", "MEDIA_DATABASE_URL") == "${MEDIA_DATABASE_URL:-}",
        "Media migrations and media runtime must use the same media database")
require(os.environ.get("FRESH_DATA_CENTRE_INTERNAL_TOKEN", "").strip() and
        len(os.environ["FRESH_DATA_CENTRE_INTERNAL_TOKEN"].strip()) >= 32,
        "CI must provide the dedicated Backend/Data Centre media token")

service_tokens = [
    os.environ["ALGORITHM_INTERNAL_TOKEN"],
    os.environ["FEDERATION_INGRESS_TOKEN"],
    os.environ["FEDERATION_BACKEND_INTERNAL_TOKEN"],
    os.environ["FRESH_DATA_CENTRE_INTERNAL_TOKEN"],
]
require(len(set(service_tokens)) == len(service_tokens),
        "Algorithm, Federation ingress, Federation backend and Data Centre media service tokens must all be distinct")
require(os.environ.get("TESTAGRAM_AUTH_JWT_SECRET", "") not in set(service_tokens),
        "End-user JWT signing secret must not be reused as a service-to-service credential")
require(os.environ.get("DATA_CENTRE_MEDIA_API_URL") == "http://media:8096",
        "Backend must target the private Data Centre media service URL")
require(env(backend, "api", "FRESH_MEDIA_ENABLED") == "${FRESH_MEDIA_ENABLED:-false}" and
        os.environ.get("FRESH_MEDIA_ENABLED", "false").lower() == "false",
        "CI must preserve the Data Centre media feature as disabled by default")
require(env(backend, "api", "MEDIA_REPLICATION_ENABLED") == "${MEDIA_REPLICATION_ENABLED:-false}" and
        os.environ.get("MEDIA_REPLICATION_ENABLED", "false").lower() == "false",
        "CI must preserve Backend three-provider media replication as disabled by default")
require(os.environ.get("FEDERATION_INTERNAL_URL") == "http://federation-api:8081",
        "Backend must target the private Federation service URL")
require("media" in media.get("profiles", []), "Data Centre media must remain opt-in under the media profile")
require("platform" in media.get("networks", []), "Data Centre media must join the private platform network")
require("8096" in media.get("expose", []), "Data Centre media must expose its private listener")
require("ports" not in media, "Data Centre media must not publish a host port")
require("B2_S3_ACCESS_KEY_ID" in media.get("environment", {}) and
        "CLOUDFLARE_R2_ACCESS_KEY_ID" in media.get("environment", {}),
        "Only the Data Centre media service should receive its B2/R2 provider credentials")
for key in (
    "VPS_MINIO_ENDPOINT",
    "VPS_MINIO_REGION",
    "VPS_MINIO_METADATA_BUCKET",
    "VPS_MINIO_METADATA_ACCESS_KEY_ID",
    "VPS_MINIO_METADATA_SECRET_ACCESS_KEY",
    "CLOUDFLARE_R2_METADATA_BUCKET",
):
    require(key in media.get("environment", {}), f"Data Centre media must receive required metadata setting {key}")
require(env(dc, "media", "VPS_MINIO_ENDPOINT") == "${VPS_MINIO_ENDPOINT:-http://object-storage:9000}",
        "Data Centre media must use the Backend-owned private MinIO endpoint")
require(env(dc, "media", "VPS_MINIO_METADATA_ACCESS_KEY_ID") == "${VPS_MINIO_METADATA_ACCESS_KEY_ID:-}" and
        env(dc, "media", "VPS_MINIO_METADATA_SECRET_ACCESS_KEY") == "${VPS_MINIO_METADATA_SECRET_ACCESS_KEY:-}",
        "Data Centre media must receive its restricted MinIO metadata identity")
require(env(dc, "media", "CLOUDFLARE_R2_METADATA_BUCKET") == "${CLOUDFLARE_R2_METADATA_BUCKET:-}",
        "Data Centre media must receive its separate R2 metadata bucket")
require("VPS_MINIO_METADATA_ACCESS_KEY_ID" not in api.get("environment", {}) and
        "VPS_MINIO_METADATA_SECRET_ACCESS_KEY" not in api.get("environment", {}),
        "Data Centre's MinIO metadata-only credentials must never be passed to the Backend API")

for name, target in (("algorithm", algorithm), ("federation-api", federation), ("media", media)):
    require("platform" in target.get("networks", []) or "platform" in target.get("networks", {}),
            f"{name} must share only the private platform network with Backend")
    require("ports" not in target, f"{name} must not expose a public host port")

print("Backend ↔ Algorithm, Backend ↔ Federation and Backend ↔ Data Centre media private service contracts: PASS")
