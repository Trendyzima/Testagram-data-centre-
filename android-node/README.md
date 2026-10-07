# Testagram Android Node

Install the debug APK over USB with:

adb install -r app-debug.apk

Or from this repository:

bash android-node/install-apk.sh

First boot:
1. Open Testagram VPS.
2. Select the phone/SD-card storage directory.
3. Enter the control-plane URL, bootstrap token and node name.
4. Set the media signing secret.
5. Save the connection.
6. Turn VPS ON.

The selected SAF tree becomes the authoritative local media volume. The node creates media/uploads, media/videos/originals, media/videos/hls, media/videos/posters and the other Testagram media directories.

A video placed/uploaded under media/videos/originals is queued for on-device HLS processing. The runtime creates 240p, 360p and 720p variants, a master playlist and a poster.

CI proves the APK compiles. A physical-device test is still required to prove real upload, FFmpeg execution, HLS serving and frontend playback.

The Android node must not expose its local port directly to the public internet. External playback requires a secure reachable endpoint in front of the phone.
