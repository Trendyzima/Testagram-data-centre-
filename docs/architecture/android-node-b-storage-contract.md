# Android Node B — Removable SD Storage Contract

## Purpose

Node B contributes optional media capacity from a removable microSD card. It is not a phone compute worker, database host, CDN origin exposed to the public Internet, or a replacement for durable off-host storage.

## Required invariants

1. **Removable card only:** the user explicitly selects a mounted removable SD-card tree using Android's Storage Access Framework.
2. **No internal fallback:** if the card is absent, unmounted, read-only, replaced or inaccessible, Node B becomes unavailable. Never silently write media to internal phone storage.
3. **Dynamic capacity:** detect actual total/free bytes at runtime. Do not hardcode supported card sizes.
4. **Stable volume identity:** persist the selected volume identity and revalidate it. A replacement card is a new node volume and must not inherit the old card's object inventory.
5. **Storage-only capability:** registration and heartbeat include `storageOnly=true`, removable-card identity, total bytes and free bytes. The scheduler rejects storage-only nodes for all compute jobs.
6. **Bounded work:** keep storage workers and queues small, stop new work when limits are reached, and cancel safely on shutdown or card removal.
7. **No on-device media processing:** no FFmpeg, HLS generation, thumbnailing, feed ranking, database, AI inference or arbitrary container workloads.
8. **Private transport:** the phone initiates authenticated encrypted connections. Do not instruct users to expose its local HTTP server with router port forwarding.

## Object write protocol

1. Backend authorizes an upload and issues a short-lived, object-scoped capability.
2. Data-centre selects a healthy eligible storage target and reserves capacity.
3. Node receives a bounded upload request, writes to a temporary file on the selected SD card, flushes/closes it, and computes SHA-256.
4. Data-centre compares the checksum and expected byte count, then atomically promotes the temporary object into its final object path.
5. Node reports durable acknowledgement with object ID, volume ID, checksum, size and timestamp.
6. Backend marks the media rendition available only after the required durability policy is met.

A node acknowledgement means the object is present on that card; it does not mean the object is backed up. For critical media, require an independent second copy before claiming replicated durability.

## Removal and failure handling

- Card removal interrupts writes and fails the active operation; never report partial data as successful.
- A node that misses heartbeats or reports insufficient free space receives no new placements.
- Reads may use another verified replica or object-store copy. If no copy exists, report unavailable; do not fabricate a successful read.
- Reinserted card must pass identity and filesystem verification before returning to service.
- Replaced card must be treated as empty until inventory reconciliation completes.
- Repair jobs must be idempotent, rate-limited and scheduled on server-side compute, never on the phone.

## Physical-device acceptance checklist

- [ ] Internal storage selection is rejected.
- [ ] Removable SD card selection and volume identity are correct.
- [ ] Reported capacity/free space matches the selected card.
- [ ] Write/read/checksum/delete test succeeds.
- [ ] Removing card during write fails safely and leaves no published partial object.
- [ ] Reinserted original card is recognized.
- [ ] Replacement card gets a new volume identity and empty inventory.
- [ ] Heartbeat reports selected card capacity only.
- [ ] Control-plane scheduler never assigns compute workload to Node B.
- [ ] Phone remains responsive under sustained bounded upload/read activity.
- [ ] Thermal, battery and Android background restrictions are measured on physical hardware.
