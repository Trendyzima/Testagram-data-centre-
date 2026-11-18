package nativeauth

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestResolveUserIDEnforcesLiveSessionAndAccountState(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for native auth session integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS testagram_auth;
		CREATE SCHEMA IF NOT EXISTS testagram_identity;
		CREATE TABLE IF NOT EXISTS testagram_auth.identities (
			user_id uuid PRIMARY KEY,
			token_version bigint NOT NULL,
			disabled_at timestamptz,
			email_verified_at timestamptz,
			password_reset_required boolean NOT NULL DEFAULT false
		);
		CREATE TABLE IF NOT EXISTS testagram_auth.sessions (
			session_id uuid PRIMARY KEY,
			user_id uuid NOT NULL,
			token_version bigint NOT NULL,
			revoked_at timestamptz,
			expires_at timestamptz NOT NULL
		);
		CREATE TABLE IF NOT EXISTS testagram_identity.native_registration_provisioning (
			user_id uuid NOT NULL,
			status text NOT NULL
		);
	`)
	if err != nil {
		t.Fatal(err)
	}

	secret := []byte("testagram-native-auth-integration-secret-32-bytes")
	now := time.Now().UTC()
	cases := []struct {
		name              string
		provisioningState string
		mutation          string
		mutationUsesUserID bool
		wantAllowed       bool
	}{
		{name: "existing native account without provisioning row", wantAllowed: true},
		{name: "approved registration", provisioningState: "active", wantAllowed: true},
		{name: "pending registration is denied", provisioningState: "pending", wantAllowed: false},
		{name: "revoked session is denied", mutation: "UPDATE testagram_auth.sessions SET revoked_at=now() WHERE session_id=$1::uuid", wantAllowed: false},
		{name: "expired session is denied", mutation: "UPDATE testagram_auth.sessions SET expires_at=now()-interval '1 minute' WHERE session_id=$1::uuid", wantAllowed: false},
		{name: "session token-version mismatch is denied", mutation: "UPDATE testagram_auth.sessions SET token_version=token_version+1 WHERE session_id=$1::uuid", wantAllowed: false},
		{name: "identity token-version mismatch is denied", mutation: "UPDATE testagram_auth.identities SET token_version=token_version+1 WHERE user_id=$1::uuid", mutationUsesUserID: true, wantAllowed: false},
		{name: "disabled account is denied", mutation: "UPDATE testagram_auth.identities SET disabled_at=now() WHERE user_id=$1::uuid", mutationUsesUserID: true, wantAllowed: false},
		{name: "unverified email is denied", mutation: "UPDATE testagram_auth.identities SET email_verified_at=NULL WHERE user_id=$1::uuid", mutationUsesUserID: true, wantAllowed: false},
		{name: "password reset required is denied", mutation: "UPDATE testagram_auth.identities SET password_reset_required=true WHERE user_id=$1::uuid", mutationUsesUserID: true, wantAllowed: false},
	}

	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userID := fmt.Sprintf("00000000-0000-4000-8000-%012x", index+1)
			sessionID := fmt.Sprintf("10000000-0000-4000-8000-%012x", index+1)
			_, err := db.Exec(ctx, `
				INSERT INTO testagram_auth.identities
					(user_id,token_version,email_verified_at,password_reset_required)
				VALUES ($1::uuid,1,now(),false)
			`, userID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(ctx, `
				INSERT INTO testagram_auth.sessions
					(session_id,user_id,token_version,expires_at)
				VALUES ($1::uuid,$2::uuid,1,now()+interval '10 minutes')
			`, sessionID, userID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.provisioningState != "" {
				if _, err := db.Exec(ctx, `
					INSERT INTO testagram_identity.native_registration_provisioning(user_id,status)
					VALUES ($1::uuid,$2)
				`, userID, tc.provisioningState); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				_, _ = db.Exec(context.Background(), "DELETE FROM testagram_identity.native_registration_provisioning WHERE user_id=$1::uuid", userID)
				_, _ = db.Exec(context.Background(), "DELETE FROM testagram_auth.sessions WHERE session_id=$1::uuid", sessionID)
				_, _ = db.Exec(context.Background(), "DELETE FROM testagram_auth.identities WHERE user_id=$1::uuid", userID)
			})

			claims := Claims{
				Subject: userID, Email: "person@example.test", Role: "authenticated",
				Issuer: "testagram-auth", Audience: "testagram-api",
				IssuedAt: now.Unix(), ExpiresAt: now.Add(10 * time.Minute).Unix(),
				TokenVersion: 1, SessionID: sessionID,
			}
			token := signedToken(t, claims, secret, "HS256")
			if tc.mutation != "" {
				arg := sessionID
				if tc.mutationUsesUserID {
					arg = userID
				}
				if _, err := db.Exec(ctx, tc.mutation, arg); err != nil {
					t.Fatal(err)
				}
			}

			got, err := ResolveUserID(ctx, db, token, secret)
			if tc.wantAllowed {
				if err != nil || got != userID {
					t.Fatalf("valid native session rejected: user=%q err=%v", got, err)
				}
			} else if err == nil || got != "" {
				t.Fatalf("invalid native session accepted: user=%q", got)
			}
		})
	}
}
