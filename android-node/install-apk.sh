#!/usr/bin/env bash
set -euo pipefail
APK="${1:-android-node/app/build/outputs/apk/debug/app-debug.apk}"
command -v adb >/dev/null || { echo "adb is required"; exit 1; }
test -f "$APK" || { echo "APK not found: $APK"; exit 1; }
adb start-server >/dev/null
adb wait-for-device
adb install -r "$APK"
echo "Testagram Android node installed. Open the app, select storage, configure the control plane and turn the VPS on."
