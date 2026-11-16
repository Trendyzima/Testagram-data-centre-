# Notification service

The notification API owns device registrations, queued notifications and
server-sent event delivery. Authenticated client requests use Testagram-native
HS256 access tokens issued by the Go Backend. The service validates the token
signature with the shared server-side `TESTAGRAM_AUTH_JWT_SECRET` and checks
the active session, token version, account state and registration approval
against the shared PostgreSQL identity tables.

The service never accepts an external identity-provider token. The JWT signing
secret is a server-only secret and must be identical to the Backend's value.
The notification service requires read access to `testagram_auth` and
`testagram_identity.native_registration_provisioning`.

Internal notification creation uses a separate `NOTIFICATION_INTERNAL_TOKEN`;
it is not interchangeable with the user JWT signing secret.
