# Testagram Video Platform

This is the Testagram-owned video plane.

It provides creator channels, direct upload, asynchronous local FFmpeg processing, adaptive HLS output, posters, private/unlisted/public visibility, view de-duplication, likes, comments, subscriptions, publishing, and signed playback.

No managed video platform, CDN, or media SaaS is required.

Authentication accepts only short-lived Testagram-native HS256 access tokens.
The service verifies signature, fixed issuer/audience, role, UUID subject/session,
token lifetime and version, then checks the live PostgreSQL session, revocation,
account status, verified email and approved-registration state. It uses the
exact `TESTAGRAM_AUTH_JWT_SECRET` shared with Backend. Foreign issuers and
external identity-provider fallback paths are rejected.

PostgreSQL stores metadata and durable jobs. Video bytes stay on Testagram-owned VPS/node storage.


## Creator video deletion and media cleanup

A successful creator DELETE commits the product-row deletion and a
`video_media_cleanup_jobs` outbox row in the same PostgreSQL transaction.
The response does not wait for filesystem removal. The private video worker
claims cleanup intents with a five-minute lease, removes the original, HLS and
poster trees idempotently, and marks the outbox row complete.

Transient failures are retried up to five attempts. A terminal `failed` row is
retained for operator inspection; it is not silently discarded. To inspect
pending or failed cleanup work:

```sql
select video_id, status, attempts, available_at, lease_until, last_error,
       created_at, updated_at
from testagram_video.video_media_cleanup_jobs
where status <> 'done'
order by created_at;
```

After correcting the underlying storage-permission or filesystem issue, an
operator can explicitly requeue terminal failures:

```sql
update testagram_video.video_media_cleanup_jobs
set status = 'pending', attempts = 0, available_at = now(),
    lease_until = null, last_error = null, updated_at = now()
where status = 'failed';
```

Do not delete failed outbox rows to hide cleanup debt. The row is the durable
record that media removal still requires attention.
