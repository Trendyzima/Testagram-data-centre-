package nativeauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func signedToken(t *testing.T, claims Claims, secret []byte, algorithm string) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": algorithm, "typ": "JWT"})
	if err != nil { t.Fatal(err) }
	payload, err := json.Marshal(claims)
	if err != nil { t.Fatal(err) }
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func validClaims(now time.Time) Claims {
	return Claims{
		Subject: "2c6e2d3a-61c1-4e8f-8d41-92f1a0d13b62",
		Email: "person@example.test", Role: "authenticated",
		Issuer: "testagram-auth", Audience: "testagram-api",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(15*time.Minute).Unix(),
		TokenVersion: 1, SessionID: "5b7d8f90-0e31-4e4b-9c8a-2f3a4b5c6d7e",
	}
}

func TestParseAccessTokenAcceptsBackendToken(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	secret := []byte("testagram-native-auth-test-secret-32-bytes")
	token := signedToken(t, validClaims(now), secret, "HS256")
	got, ok := ParseAccessToken(token, secret, now)
	if !ok || got.Subject != validClaims(now).Subject || got.SessionID != validClaims(now).SessionID {
		t.Fatalf("valid token rejected: claims=%+v ok=%v", got, ok)
	}
}

func TestParseAccessTokenRejectsInvalidTokens(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	secret := []byte("testagram-native-auth-test-secret-32-bytes")
	tests := map[string]func(*Claims, string, []byte){
		"wrong issuer": func(c *Claims, _ string, _ []byte) { c.Issuer = "foreign" },
		"wrong audience": func(c *Claims, _ string, _ []byte) { c.Audience = "foreign" },
		"wrong role": func(c *Claims, _ string, _ []byte) { c.Role = "admin" },
		"invalid subject": func(c *Claims, _ string, _ []byte) { c.Subject = "not-a-uuid" },
		"invalid session": func(c *Claims, _ string, _ []byte) { c.SessionID = "not-a-uuid" },
		"expired": func(c *Claims, _ string, _ []byte) { c.ExpiresAt = now.Add(-time.Second).Unix() },
		"excessive lifetime": func(c *Claims, _ string, _ []byte) { c.ExpiresAt = now.Add(17*time.Minute).Unix() },
		"future issued": func(c *Claims, _ string, _ []byte) { c.IssuedAt = now.Add(2*time.Minute).Unix() },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			claims := validClaims(now)
			mutate(&claims, "", nil)
			if _, ok := ParseAccessToken(signedToken(t, claims, secret, "HS256"), secret, now); ok {
				t.Fatal("invalid claims accepted")
			}
		})
	}
	valid := signedToken(t, validClaims(now), secret, "HS256")
	if _, ok := ParseAccessToken(valid+"x", secret, now); ok { t.Fatal("tampered signature accepted") }
	if _, ok := ParseAccessToken(signedToken(t, validClaims(now), secret, "none"), secret, now); ok { t.Fatal("algorithm substitution accepted") }
	if _, ok := ParseAccessToken(valid, []byte("short"), now); ok { t.Fatal("short secret accepted") }
	if _, ok := ParseAccessToken(string(make([]byte, 8193)), secret, now); ok { t.Fatal("oversized token accepted") }
}
