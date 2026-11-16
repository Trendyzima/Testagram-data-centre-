package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type nativeVideoAuthClaims struct {
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

var nativeVideoUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

// parseNativeVideoAccessToken mirrors the Backend's narrow HS256 access-token
// contract. nativeUser additionally checks the live session in PostgreSQL.
func parseNativeVideoAccessToken(token string, secret []byte, now time.Time) (nativeVideoAuthClaims, bool) {
	var claims nativeVideoAuthClaims
	if len(secret) < 32 || len(token) == 0 || len(token) > 8192 {
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
		return nativeVideoAuthClaims{}, false
	}
	nowUnix := now.UTC().Unix()
	if claims.Issuer != "testagram-auth" || claims.Audience != "testagram-api" ||
		claims.Role != "authenticated" ||
		!nativeVideoUUIDPattern.MatchString(claims.Subject) ||
		!nativeVideoUUIDPattern.MatchString(claims.SessionID) ||
		claims.TokenVersion < 1 || claims.IssuedAt <= 0 ||
		claims.IssuedAt > nowUnix+60 || claims.ExpiresAt <= nowUnix ||
		claims.ExpiresAt <= claims.IssuedAt ||
		claims.ExpiresAt-claims.IssuedAt > int64((16*time.Minute)/time.Second) {
		return nativeVideoAuthClaims{}, false
	}
	return claims, true
}

func (s *server) nativeUser(r *http.Request, token string) (string, bool) {
	claims, ok := parseNativeVideoAccessToken(token, s.authJWTSecret, time.Now())
	if !ok {
		return "", false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	var active bool
	err := s.db.QueryRow(ctx, `
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
	return claims.Subject, err == nil && active
}
