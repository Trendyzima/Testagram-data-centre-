# Testagram-data-centre-

## Docker VPS quick install

On a Linux host with Docker Engine, Docker Compose v2, Git and OpenSSL:

bash scripts/install-local-docker.sh

The installer creates a local development configuration automatically when no .env exists. For production, provide your own domain and secrets.

Check the stack:

docker compose --env-file /opt/testagram/.env -f /opt/testagram/vps/docker-compose.yml ps

## Android phone install

Build the APK:

gradle -p android-node assembleDebug

Install over USB:

bash android-node/install-apk.sh

Then open Testagram VPS, select local storage, configure the control plane and turn the node on.

Android is the local media/storage node. The Linux Docker host runs Supabase and the backend/control-plane services.

## Forensic E2E gate

The required physical path remains:

real Android storage -> real MP4 upload -> Android local media server -> real FFmpeg HLS -> reachable Testagram frontend -> actual playback

That gate is not marked green until a physical device supplies evidence for every hop.
