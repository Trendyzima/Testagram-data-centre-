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

type Claims struct {
	Subject      string `json:"sub"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	Issuer       string `json:"iss"`
	Audience     string `json:"aud"`
	IssuedAt     int64  `json:"iat"`
	ExpiresAt    int64  `json:"exp"`
	TokenVersion int64  `json:"ver"`
	SessionID    string `json:"sid"`
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

// ParseAccessToken accepts only the Backend's short-lived Testagram HS256 token.
// It validates cryptographic integrity and fixed issuer/audience/identity claims;
// callers must still check the live session and account state in PostgreSQL.
func ParseAccessToken(token string, secret []byte, now time.Time) (Claims, bool) {
	var claims Claims
	if len(secret) < 32 || token == "" || len(token) > 8192 {
		return claims, false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims, false
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, false
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.Algorithm != "HS256" || header.Type != "JWT" {
		return claims, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return claims, false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return claims, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		return Claims{}, false
	}
	nowUnix := now.UTC().Unix()
	if claims.Issuer != "testagram-auth" || claims.Audience != "testagram-api" ||
		claims.Role != "authenticated" ||
		!uuidPattern.MatchString(claims.Subject) ||
		!uuidPattern.MatchString(claims.SessionID) ||
		claims.TokenVersion < 1 || claims.IssuedAt <= 0 ||
		claims.IssuedAt > nowUnix+60 || claims.ExpiresAt <= nowUnix ||
		claims.ExpiresAt <= claims.IssuedAt ||
		claims.ExpiresAt-claims.IssuedAt > int64((16*time.Minute)/time.Second) {
		return Claims{}, false
	}
	return claims, true
}

// ResolveUserID additionally checks revocation, expiry, token-version rotation,
// account disablement, verified email, and approved native-registration state.
// A cryptographically valid but revoked/stale token is never accepted.
func ResolveUserID(ctx context.Context, db *pgxpool.Pool, token string, secret []byte) (string, error) {
	if db == nil {
		return "", errors.New("native identity database unavailable")
	}
	claims, ok := ParseAccessToken(token, secret, time.Now())
	if !ok {
		return "", errors.New("invalid native access token")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var active bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM testagram_auth.sessions s
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
	`, claims.SessionID, claims.Subject, claims.TokenVersion).Scan(&active)
	if err != nil {
		return "", errors.New("native session state unavailable")
	}
	if !active {
		return "", errors.New("native session revoked, expired, or account not active")
	}
	return claims.Subject, nil
}
