package nativeauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type claims struct {
	Subject string `json:"sub"`
	Email string `json:"email"`
	Role string `json:"role"`
	Issuer string `json:"iss"`
	Audience string `json:"aud"`
	IssuedAt int64 `json:"iat"`
	ExpiresAt int64 `json:"exp"`
	TokenVersion int64 `json:"ver"`
	SessionID string `json:"sid"`
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

// ResolveUserID accepts only Testagram-issued HS256 access tokens and verifies
// the live PostgreSQL session, token version, account status and provisioning
// state. It never calls an external identity provider and fails closed.
func ResolveUserID(ctx context.Context, db *pgxpool.Pool, secret []byte, token string) (string, error) {
	if db == nil || len(secret) < 32 || token == "" || len(token) > 8192 {
		return "", errors.New("native authentication is unavailable")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 { return "", errors.New("invalid access token") }
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil { return "", errors.New("invalid access token") }
	var header struct { Algorithm string `json:"alg"`; Type string `json:"typ"` }
	if json.Unmarshal(headerBytes, &header) != nil || header.Algorithm != "HS256" || header.Type != "JWT" {
		return "", errors.New("invalid access token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil { return "", errors.New("invalid access token") }
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0]+"."+parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) { return "", errors.New("invalid access token") }
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil { return "", errors.New("invalid access token") }
	var c claims
	if json.Unmarshal(payload, &c) != nil { return "", errors.New("invalid access token") }
	now := time.Now().UTC().Unix()
	if c.Issuer != "testagram-auth" || c.Audience != "testagram-api" || c.Role != "authenticated" ||
		!uuidPattern.MatchString(c.Subject) || !uuidPattern.MatchString(c.SessionID) ||
		c.TokenVersion < 1 || c.IssuedAt <= 0 || c.IssuedAt > now+60 ||
		c.ExpiresAt <= now || c.ExpiresAt <= c.IssuedAt ||
		c.ExpiresAt-c.IssuedAt > int64((16*time.Minute)/time.Second) {
		return "", errors.New("invalid access token")
	}
	var active bool
	err = db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM testagram_auth.sessions s
			JOIN testagram_auth.identities i ON i.user_id=s.user_id
			WHERE s.session_id=$1::uuid AND s.user_id=$2::uuid
			  AND s.token_version=$3 AND s.revoked_at IS NULL AND s.expires_at>now()
			  AND i.token_version=$3 AND i.disabled_at IS NULL
			  AND i.email_verified_at IS NOT NULL AND NOT i.password_reset_required
			  AND NOT EXISTS(
				SELECT 1 FROM testagram_identity.native_registration_provisioning p
				WHERE p.user_id=i.user_id AND p.status<>'active'
			  )
		)
	`, c.SessionID, c.Subject, c.TokenVersion).Scan(&active)
	if err != nil || !active { return "", errors.New("session is invalid or revoked") }
	return c.Subject, nil
}
