# Testagram-data-centre-

## Android APK — build, verify, download

GitHub Actions builds the Android node on every relevant push/PR and stores the APK as the **testagram-android-node** artifact.

**Download the latest APK:**  
https://github.com/Trendyzima/Testagram-data-centre-/actions/workflows/vps-ci.yml

Open the latest successful run, scroll to **Artifacts**, and download **testagram-android-node**. GitHub requires read access to the repository to download workflow artifacts.

The package contains:
- `TestagramNode-debug.apk` — CI/debug build for development and physical-device testing.
- `TestagramNode-release.apk` — signed release build when the repository's Android signing secrets are configured.
- SHA-256 checksum files for APK integrity verification.
- `install-apk.sh` — optional USB/ADB installer.

### Play Protect / Android security hardening

The Android node is built as a normal Android application with a stable package ID, current target SDK, minimal permissions, non-exported background services, no backup of app data, and cleartext network traffic disabled.

The release pipeline supports a **stable release signing key** through GitHub Actions secrets:

- `ANDROID_KEYSTORE_BASE64`
- `ANDROID_KEYSTORE_PASSWORD`
- `ANDROID_KEY_ALIAS`
- `ANDROID_KEY_PASSWORD`

Do **not** commit the keystore or passwords to the repository. The same signing key must be retained for future updates so Android can verify that updates come from the same signer.

A signed APK reduces distribution/integrity problems, but **no APK can truthfully be guaranteed to bypass Google Play Protect**. Play Protect independently scans apps from outside Google Play and can block apps it considers harmful. For official distribution, complete Google's developer verification and, where appropriate, distribute through Google Play/Play App Signing.

### Verify an APK before installing

After downloading the artifact, verify its SHA-256 checksum against the included `.sha256` file.

For an ADB installation:

```bash
adb install -r TestagramNode-release.apk
```

Or use the bundled installer:

```bash
bash install-apk.sh TestagramNode-release.apk
```

### Android phone setup

After installation:

1. Open **Testagram Node**.
2. Select a mounted **removable SD card** in the Android folder picker. Internal phone storage and cloud-provider folders are rejected.
3. Confirm the card's detected capacity and complete the write/read/delete verification test.
4. Configure the Testagram control-plane URL, bootstrap token, node name and media signing secret.
5. Turn Node B **ON**.
6. Confirm heartbeats report the selected SD card's total/free bytes and removable-card identity.
7. Node B runs in storage-only mode. On-device video transcoding is disabled; video processing must run on a separate server/worker.

Android is the local media/storage node. The Linux Docker host runs the Testagram Backend and Data Centre control-plane services. Supabase is retired and is not part of the fresh runtime.

## Docker VPS quick install

On a Linux host with Docker Engine, Docker Compose v2, Git and OpenSSL:

```bash
bash scripts/install-local-docker.sh
```

The installer creates a local development configuration automatically when no `.env` exists. For production, provide your own domain and secrets.

Check the stack:

```bash
docker compose --env-file /opt/testagram/.env -f /opt/testagram/vps/docker-compose.yml ps
```

## Encrypted off-host backups and restore drills

The VPS now includes operator-run scripts for an encrypted database + storage backup and a non-production restore drill.

Before using them:
- Install Docker Compose v2, `age`, `rsync`, `tar`, `flock`, and `sha256sum`.
- Configure SSH key authentication and verify the backup host key in `known_hosts`; the script intentionally does not disable SSH host-key checking.
- Set `TESTAGRAM_BACKUP_REMOTE=user@backup-host:/absolute/path` and `TESTAGRAM_BACKUP_AGE_RECIPIENT=age1...` in the operator's environment.
- Ensure `TESTAGRAM_ENV_FILE` points to the deployment environment file and `TESTAGRAM_STORAGE_ROOT` points to the actual persistent storage root.
- Schedule a maintenance window. The backup script stops application writers while capturing the database and files, then starts services before transferring the encrypted archive. Users may experience downtime during capture.
- Store the age private identity separately from the VPS and backup host.

Run the backup from the checked-out deployment repository:

```bash
sudo --preserve-env=TESTAGRAM_BACKUP_REMOTE,TESTAGRAM_BACKUP_AGE_RECIPIENT,TESTAGRAM_ENV_FILE,TESTAGRAM_STORAGE_ROOT \
  bash vps/backup.sh
```

For a restore drill, obtain the encrypted `.tar.gz.age` artifact and its `.sha256` sidecar, then use the separately held age identity and the exact approved PostgreSQL image digest used by the deployment:

```bash
TESTAGRAM_BACKUP_AGE_IDENTITY_FILE=/secure/off-host-age-identity.txt \
TESTAGRAM_RESTORE_POSTGRES_IMAGE='postgres:17-alpine@sha256:REPLACE_WITH_APPROVED_DIGEST' \
  bash vps/restore-drill.sh /secure/backup/testagram-YYYYMMDDTHHMMSSZ.tar.gz.age
```

The restore drill verifies the ciphertext checksum, decrypts the archive, validates the storage tar, restores PostgreSQL into a network-isolated temporary container, and checks for expected Testagram tables. It does **not** prove full login, upload, private-media, or playback recovery, and it does not by itself measure RPO/RTO. Record the drill date, artifact ID, elapsed restore time, and application-level E2E results before declaring disaster recovery ready.

## Forensic E2E gate

The required physical path remains:

**real Android storage → real MP4 upload → Android local media server → real FFmpeg HLS → reachable Testagram frontend → actual playback**

That gate is not marked green until a physical device supplies evidence for every hop.


## Testagram platform architecture

- [Service catalogue, APIs, health checks, and migration gates](docs/platform/service-catalog.md)
- [Self-sustaining domains, deployment, compute, performance, and recovery plan](docs/platform/self-sustaining-operations.md)
- [Internal platform OpenAPI contract](docs/platform/openapi/internal-platform.yaml)
- [Testagram-branded self-hosted runtime and safe preparation](testagram-platform/README.md)
- [Read-only deployment preflight](testagram-platform/preflight.sh)
- [Authenticated hosting control plane API](docs/platform/openapi/hosting-control-plane.yaml)
- [Hosting control plane implementation boundaries](docs/platform/service-catalog.md#hosting-control-plane-implementation-boundary)
- [Bounded deployment command with health gates](testagram-platform/deploy.sh)
- [Provider-independent internal platform client](internal/platformclient/client.go)

The platform rebrand preserves compatibility with the existing runtime paths and Compose identity. It does not itself migrate production authentication, database policies, media, or traffic.


## Fresh Appwrite runtime

The clean runtime is defined separately in `vps/docker-compose.fresh.yml` and
`vps/FRESH-DEPLOYMENT.md`. It runs the dedicated fresh Backend API, private
Algorithm ranker, private Federation service, Data Centre infrastructure
control plane and Caddy edge without attaching the legacy Supabase network or
starting the legacy Backend API.

The five-repository ownership boundary is:

- [XClone](https://github.com/Trendyzima/XClone-9b4dz1-9b4dz1-8936-20260809tr): web/mobile UI and presentation only.
- [Testagram Backend](https://github.com/Trendyzima/Testagram-backend-): canonical Appwrite identity, accounts, posts, social graph, permissions, visibility and API orchestration.
- [Federated Content](https://github.com/Trendyzima/federated-content): ActivityPub protocol state, signed ingress, actor discovery and asynchronous federation processing.
- [the-algorithm](https://github.com/Trendyzima/the-algorithm): private ranking and recommendation scoring; Backend remains the authorization authority.
- **Data Centre (this repository)**: deployment, private networking, B2/R2 media durability, storage nodes, backups and recovery.

Federation now has PostgreSQL-backed actor/inbox/outbox state, signed inbox verification,
safe remote-key resolution and a private Backend identity/readiness contract. It is
still not a complete federation product: inbox activity application, Backend outbox
polling, outbound delivery workers, follower fan-out and end-to-end interoperability
tests remain unfinished. Keep it private and do not enable public ActivityPub
routing until those gates pass.

Keep `FRESH_MEDIA_ENABLED=false` until B2/R2 and the Appwrite media collections
are staged and independently verified. Retention remains disabled and dry-run.
The fresh Compose file is a deployment foundation, not a production-readiness
claim; media placement, replica recovery, federation workers and full
cross-repository end-to-end tests remain release gates.
