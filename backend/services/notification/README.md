# Notification service

Private Testagram notification API and streaming service.

Client requests authenticate with an Appwrite session JWT. The service validates
the JWT against the configured Appwrite Account endpoint, then resolves the
Appwrite user ID through `testagram_identity.appwrite_accounts` to the canonical
Testagram UUID before reading or changing user-owned notification data.

Internal producer routes require the separate `NOTIFICATION_INTERNAL_TOKEN`.
Never ship that token to Flutter or browsers. There is no Supabase JWT secret,
local JWT decoding, or legacy authentication fallback.
