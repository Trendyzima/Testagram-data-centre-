# Testagram video worker

Consumes durable PostgreSQL jobs and runs local FFmpeg processing. Jobs are leased and retried; a failed transcode is marked failed after three attempts.

The worker writes only to Testagram-owned media storage. No managed video/CDN service is required.
