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
2. Select/remember the phone storage directory.
3. Configure the Testagram control-plane URL and bootstrap token.
4. Turn the node **ON**.
5. Confirm the node heartbeat and storage status.
6. Use the local media workflow for upload → FFmpeg HLS generation → playback.

Android is the local media/storage node. The Linux Docker host runs Supabase and the backend/control-plane services.

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

## Forensic E2E gate

The required physical path remains:

**real Android storage → real MP4 upload → Android local media server → real FFmpeg HLS → reachable Testagram frontend → actual playback**

That gate is not marked green until a physical device supplies evidence for every hop.
