# Notification service

Private Testagram notification API and streaming service.

Authenticated user routes accept only short-lived Testagram-native HS256 access
tokens issued by the Backend. The service verifies the signature, fixed issuer
and audience, role, UUID subject/session, token lifetime and token version, then
checks the live PostgreSQL session, revocation state, account status, verified
email and approved-registration state before resolving the canonical Testagram
UUID. It shares the exact `TESTAGRAM_AUTH_JWT_SECRET` with Backend; there is no
Appwrite, third-party JWT, or cross-provider fallback.

Internal producer routes require the separate `NOTIFICATION_INTERNAL_TOKEN`.
Never ship that token or the native signing secret to Flutter or browsers.
