package main

import (
	"context"
	"net/http"
	"time"

	"github.com/Trendyzima/Testagram-data-centre-/backend/internal/nativeauth"
)

type nativeVideoAuthClaims = nativeauth.Claims

func parseNativeVideoAccessToken(token string, secret []byte, now time.Time) (nativeVideoAuthClaims, bool) {
	return nativeauth.ParseAccessToken(token, secret, now)
}

func (s *server) nativeUser(r *http.Request, token string) (string, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	userID, err := nativeauth.ResolveUserID(ctx, s.db, token, s.authJWTSecret)
	return userID, err == nil
}
