package appwriteauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUnlinked = errors.New("Appwrite identity is not linked to a Testagram account")

const maxAccountResponse = 64 << 10

type account struct {
	ID     string `json:"$id"`
	Status bool   `json:"status"`
}

// ResolveUserID validates the supplied Appwrite session with Appwrite itself,
// then resolves the Appwrite ID to the canonical Testagram UUID. It never
// decodes or trusts client JWT claims and never creates identity mappings.
func ResolveUserID(ctx context.Context, db *pgxpool.Pool, endpoint, projectID, jwt string) (string, error) {
	if db == nil {
		return "", errors.New("identity database is unavailable")
	}
	identity, err := fetchAccount(ctx, endpoint, projectID, jwt)
	if err != nil {
		return "", err
	}
	var canonicalID string
	err = db.QueryRow(ctx, `SELECT canonical_user_id::text
		FROM testagram_identity.appwrite_accounts WHERE appwrite_user_id=$1`, identity.ID).Scan(&canonicalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnlinked
	}
	if err != nil {
		return "", errors.New("Appwrite identity mapping is unavailable")
	}
	return canonicalID, nil
}

func fetchAccount(ctx context.Context, endpoint, projectID, jwt string) (account, error) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	projectID = strings.TrimSpace(projectID)
	jwt = strings.TrimSpace(jwt)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1"))) {
		return account{}, errors.New("Appwrite endpoint must use HTTPS (HTTP is allowed only for localhost)")
	}
	if projectID == "" || len(projectID) > 128 || strings.ContainsAny(projectID, "\r\n") ||
		jwt == "" || len(jwt) > 8192 || strings.ContainsAny(jwt, "\r\n") {
		return account{}, errors.New("Appwrite project or session configuration is invalid")
	}
	if !strings.HasSuffix(parsed.Path, "/v1") {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1"
	}
	parsed.Path += "/account"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return account{}, errors.New("could not construct Appwrite account request")
	}
	req.Header.Set("X-Appwrite-Project", projectID)
	req.Header.Set("X-Appwrite-JWT", jwt)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		return account{}, errors.New("Appwrite authentication service is unavailable")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return account{}, errors.New("Appwrite session was rejected")
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return account{}, errors.New("Appwrite authentication service is temporarily unavailable")
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return account{}, fmt.Errorf("Appwrite account endpoint returned HTTP %d", resp.StatusCode)
	}
	var identity account
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAccountResponse)).Decode(&identity); err != nil ||
		!validAppwriteID(identity.ID) || !identity.Status {
		return account{}, errors.New("Appwrite returned an invalid or disabled account")
	}
	return identity, nil
}

func validAppwriteID(value string) bool {
	if value == "" || len(value) > 36 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r == 0x7f {
			return false
		}
	}
	return true
}
