# Testagram Android Node B

Node B is a **storage-only Android node**. The removable SD card is the authoritative media volume. Internal phone storage is not a media fallback.

## First boot

1. Insert and mount a removable microSD card in the phone.
2. Open **Testagram Node B**.
3. Choose **SELECT REMOVABLE SD CARD**.
4. In Android's folder picker, select the SD-card volume. Selecting internal storage or a cloud-provider folder is rejected.
5. The app creates a `TestagramNode/` directory on that card.
6. The app performs a small write/read/delete verification test.
7. Enter the control-plane URL, bootstrap token, node name and media-signing secret.
8. Save the connection and turn Node B ON.

## Storage behavior

- The app identifies removable volumes using Android's StorageManager and matches the selected SAF tree to the removable volume UUID.
- Capacity and free space are read from the selected SD card; the dashboard never reports internal phone storage as node capacity.
- Card capacity is dynamic. No 32/64/128/256/512 GB size is hardcoded.
- Media is stored under `SD card/TestagramNode/media/`.
- A removed, unmounted, read-only or replaced card makes the node unavailable until the selected card is verified again.
- A new card is treated as a new storage identity. The app does not claim that files from the previous card exist on it.
- The app sends the selected card's total and free bytes plus its UUID to the control plane on registration and heartbeat.
- The node advertises `storageOnly=true`; the control plane must exclude it from compute workload scheduling.

## Phone workload policy

- On-device FFmpeg transcoding is disabled and its bundled dependency is removed.
- Node B performs storage I/O, signed media transfer, small checks and heartbeats only.
- It does not host the Testagram database, generate feeds or run video conversion.
- Android may suspend background work or stop network access. Treat the node as intermittently available.
- Keep the phone cool and powered. The app must stop accepting new writes if the card becomes unavailable.

## Important deployment boundary

The Android media server must not be exposed directly to the public internet. Use an authenticated, encrypted, outbound/reverse-tunnel path in front of it. Do not configure public router port forwarding to the phone.

## Build and verification

GitHub Actions builds the debug APK on relevant pushes and pull requests. A successful CI build proves compilation only. Before production use, test on a physical phone:

1. Select a real removable SD card and verify its capacity matches Android's storage settings.
2. Confirm selecting internal storage is rejected.
3. Upload and retrieve a test file from the SD card.
4. Remove the card during an upload and confirm the write fails safely.
5. Reinsert the same card and confirm it is recognized.
6. Replace it with a different card and confirm it receives a new storage identity.
7. Confirm heartbeats report the selected card's total/free bytes.
8. Confirm the control plane does not schedule compute workloads onto Node B.
9. Confirm the backend can retrieve media through the secured media edge.

Do not count this phone's storage toward production capacity until these physical-device and backend integration tests pass.
