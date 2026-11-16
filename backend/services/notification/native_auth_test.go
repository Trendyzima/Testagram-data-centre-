package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func signNotificationTestJWT(t *testing.T, claims nativeNotificationClaims, secret []byte) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg":"HS256","typ":"JWT"})
	if err != nil { t.Fatal(err) }
	payload, err := json.Marshal(claims)
	if err != nil { t.Fatal(err) }
	unsigned := base64.RawURLEncoding.EncodeToString(header)+"."+base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(unsigned))
	return unsigned+"."+base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestParseNativeNotificationAccessToken(t *testing.T) {
	now := time.Date(2026,11,16,20,0,0,0,time.UTC)
	secret := []byte(strings.Repeat("s",32))
	valid := nativeNotificationClaims{
		Subject:"123e4567-e89b-42d3-a456-426614174000",
		Email:"user@example.test",Role:"authenticated",Issuer:"testagram-auth",Audience:"testagram-api",
		IssuedAt:now.Unix(),ExpiresAt:now.Add(15*time.Minute).Unix(),TokenVersion:1,
		SessionID:"123e4567-e89b-42d3-a456-426614174001",
	}
	t.Run("accepts backend native token contract",func(t *testing.T){
		token:=signNotificationTestJWT(t,valid,secret)
		claims,ok:=parseNativeNotificationAccessToken(token,secret,now)
		if !ok || claims.Subject!=valid.Subject || claims.SessionID!=valid.SessionID { t.Fatalf("claims=%+v ok=%v",claims,ok) }
	})
	tests:=[]struct{name string; mutate func(*nativeNotificationClaims); secret []byte}{
		{"wrong issuer",func(c *nativeNotificationClaims){c.Issuer="other"},secret},
		{"wrong audience",func(c *nativeNotificationClaims){c.Audience="other"},secret},
		{"wrong role",func(c *nativeNotificationClaims){c.Role="admin"},secret},
		{"malformed subject",func(c *nativeNotificationClaims){c.Subject="not-a-uuid"},secret},
		{"expired",func(c *nativeNotificationClaims){c.ExpiresAt=now.Unix()-1},secret},
		{"excessive lifetime",func(c *nativeNotificationClaims){c.ExpiresAt=now.Add(17*time.Minute).Unix()},secret},
		{"future issued",func(c *nativeNotificationClaims){c.IssuedAt=now.Add(2*time.Minute).Unix()},secret},
		{"short signing secret",func(c *nativeNotificationClaims){},[]byte("short")},
	}
	for _,tc:=range tests{t.Run(tc.name,func(t *testing.T){
		claims:=valid;tc.mutate(&claims)
		token:=signNotificationTestJWT(t,claims,secret)
		if _,ok:=parseNativeNotificationAccessToken(token,tc.secret,now);ok{t.Fatal("invalid token was accepted")}
	})}
	t.Run("rejects signature tampering",func(t *testing.T){
		token:=signNotificationTestJWT(t,valid,secret)
		parts:=strings.Split(token,".")
		parts[1]=base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"attacker"}`))
		if _,ok:=parseNativeNotificationAccessToken(strings.Join(parts,"."),secret,now);ok{t.Fatal("tampered token was accepted")}
	})
}
