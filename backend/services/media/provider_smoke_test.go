package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestLiveProviderRoundTrip is intentionally opt-in. It writes only unique
// ci-smoke objects and deletes them after verifying each provider independently.
func TestLiveProviderRoundTrip(t *testing.T) {
	if os.Getenv("TESTAGRAM_LIVE_MEDIA_SMOKE") != "true" {
		t.Skip("set TESTAGRAM_LIVE_MEDIA_SMOKE=true only in the manual provider-smoke workflow")
	}
	readStore := func(prefix, name string) s3Store {
		store := s3Store{
			name: name,
			endpoint: os.Getenv(prefix + "_ENDPOINT"),
			region: os.Getenv(prefix + "_REGION"),
			bucket: os.Getenv(prefix + "_BUCKET"),
			access: os.Getenv(prefix + "_ACCESS_KEY_ID"),
			secret: os.Getenv(prefix + "_SECRET_ACCESS_KEY"),
		}
		if err := validateStore(store); err != nil {
			t.Fatalf("%s smoke credentials/configuration invalid: %v", name, err)
		}
		return store
	}
	stores := []s3Store{
		readStore("MEDIA_SMOKE_B2", "backblaze_b2"),
		readStore("MEDIA_SMOKE_R2", "cloudflare_r2"),
	}
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	s := &server{cfg: config{primary: stores[0], replica: stores[1]}, http: client}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	payload := []byte("Testagram live media provider smoke " + time.Now().UTC().Format(time.RFC3339Nano))
	for _, store := range stores {
		store := store
		t.Run(store.name, func(t *testing.T) {
			id, err := randomID()
			if err != nil {
				t.Fatal(err)
			}
			key := "ci-smoke/" + id
			defer func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cleanupCancel()
				resp, cleanupErr := s.signedRequest(cleanupCtx, store, http.MethodDelete, key, nil, "")
				if cleanupErr != nil {
					t.Errorf("cleanup request failed: %v", cleanupErr)
					return
				}
				io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
					t.Errorf("cleanup returned HTTP %d", resp.StatusCode)
				}
			}()

			body := &lengthReader{Reader: bytes.NewReader(payload), n: int64(len(payload))}
			put, err := s.signedRequest(ctx, store, http.MethodPut, key, body, "application/octet-stream")
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, io.LimitReader(put.Body, 4096))
			put.Body.Close()
			if put.StatusCode < 200 || put.StatusCode >= 300 {
				t.Fatalf("PUT returned HTTP %d", put.StatusCode)
			}

			get, err := s.signedRequest(ctx, store, http.MethodGet, key, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if get.StatusCode != http.StatusOK {
				get.Body.Close()
				t.Fatalf("GET returned HTTP %d", get.StatusCode)
			}
			if gotType := strings.TrimSpace(strings.Split(get.Header.Get("Content-Type"), ";")[0]); gotType != "application/octet-stream" {
				get.Body.Close()
				t.Fatalf("GET returned unexpected content type %q", gotType)
			}
			if !digestMatches(get.Body, int64(len(payload)), hashHex(payload)) {
				get.Body.Close()
				t.Fatal("provider GET read-back did not match the exact payload size and SHA-256")
			}
			get.Body.Close()

			head, err := s.signedRequest(ctx, store, http.MethodHead, key, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			head.Body.Close()
			if head.StatusCode != http.StatusOK || head.ContentLength != int64(len(payload)) {
				t.Fatalf("HEAD returned status=%d content-length=%d", head.StatusCode, head.ContentLength)
			}
			if gotType := strings.TrimSpace(strings.Split(head.Header.Get("Content-Type"), ";")[0]); gotType != "application/octet-stream" {
				t.Fatalf("HEAD returned unexpected content type %q", gotType)
			}

			// Prove deletion is observable at the provider, not merely accepted by
			// the DELETE endpoint. The deferred cleanup remains as a retry guard.
			del, err := s.signedRequest(ctx, store, http.MethodDelete, key, nil, "")
			if err != nil {
				t.Fatalf("DELETE returned transport error: %v", err)
			}
			io.Copy(io.Discard, io.LimitReader(del.Body, 4096))
			del.Body.Close()
			if del.StatusCode != http.StatusOK && del.StatusCode != http.StatusNoContent {
				t.Fatalf("DELETE returned HTTP %d", del.StatusCode)
			}
			afterDelete, err := s.signedRequest(ctx, store, http.MethodHead, key, nil, "")
			if err != nil {
				t.Fatalf("HEAD after DELETE returned transport error: %v", err)
			}
			io.Copy(io.Discard, io.LimitReader(afterDelete.Body, 4096))
			afterDelete.Body.Close()
			if afterDelete.StatusCode != http.StatusNotFound {
				t.Fatalf("HEAD after DELETE returned HTTP %d, want 404", afterDelete.StatusCode)
			}
		})
	}
}


// TestLiveMediaReplicationRestoreDelete exercises the real media outbox against
// isolated staging B2/R2 buckets and a disposable PostgreSQL database. It proves
// B2 -> R2 replication, R2 -> B2 primary restoration, and durable two-origin deletion.
func TestLiveMediaReplicationRestoreDelete(t *testing.T) {
	if os.Getenv("TESTAGRAM_LIVE_MEDIA_SMOKE") != "true" {
		t.Skip("set TESTAGRAM_LIVE_MEDIA_SMOKE=true only in the manual provider-smoke workflow")
	}
	databaseURL := strings.TrimSpace(os.Getenv("MEDIA_DATABASE_URL"))
	token := strings.TrimSpace(os.Getenv("MEDIA_INTERNAL_TOKEN"))
	if databaseURL == "" || len(token) < 32 {
		t.Fatal("live lifecycle smoke requires MEDIA_DATABASE_URL and a 32+ character MEDIA_INTERNAL_TOKEN")
	}
	readStore := func(prefix, name string) s3Store {
		store := s3Store{
			name: name,
			endpoint: os.Getenv(prefix + "_ENDPOINT"),
			region: os.Getenv(prefix + "_REGION"),
			bucket: os.Getenv(prefix + "_BUCKET"),
			access: os.Getenv(prefix + "_ACCESS_KEY_ID"),
			secret: os.Getenv(prefix + "_SECRET_ACCESS_KEY"),
		}
		if err := validateStore(store); err != nil {
			t.Fatalf("%s smoke credentials/configuration invalid: %v", name, err)
		}
		return store
	}
	primary := readStore("MEDIA_SMOKE_B2", "backblaze_b2")
	replica := readStore("MEDIA_SMOKE_R2", "cloudflare_r2")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open isolated media smoke database: %v", err)
	}
	defer db.Close()
	if err := db.Ping(ctx); err != nil {
		t.Fatalf("media smoke database unavailable: %v", err)
	}
	s := &server{
		cfg: config{token: token, primary: primary, replica: replica},
		db: db,
		http: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
	id, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	owner := "ci-smoke"
	key := "users/" + owner + "/" + id
	payload := []byte("Testagram live B2-R2 replication restore smoke " + time.Now().UTC().Format(time.RFC3339Nano))
	item := reconcileItem{owner: owner, namespace: "media-private", key: key, contentType: "application/octet-stream", size: int64(len(payload)), expectedSHA: hashHex(payload)}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		for _, store := range []s3Store{primary, replica} {
			resp, cleanupErr := s.signedRequest(cleanupCtx, store, http.MethodDelete, key, nil, "")
			if cleanupErr == nil {
				io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
			}
		}
		_, _ = db.Exec(cleanupCtx, `delete from media_operations where owner_id=$1 and namespace=$2 and object_key=$3`, owner, item.namespace, key)
		_, _ = db.Exec(cleanupCtx, `delete from media_outbox where owner_id=$1 and namespace=$2 and object_key=$3`, owner, item.namespace, key)
		_, _ = db.Exec(cleanupCtx, `delete from media_upload_grants where owner_id=$1 and namespace=$2 and object_key=$3`, owner, item.namespace, key)
		_, _ = db.Exec(cleanupCtx, `delete from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`, owner, item.namespace, key)
	}()

	// Create the primary B2 object and its durable replication job.
	put, err := s.signedRequest(ctx, primary, http.MethodPut, key, &lengthReader{Reader: bytes.NewReader(payload), n: int64(len(payload))}, item.contentType)
	if err != nil {
		t.Fatalf("write staging object to B2: %v", err)
	}
	io.Copy(io.Discard, io.LimitReader(put.Body, 4096))
	put.Body.Close()
	if put.StatusCode < 200 || put.StatusCode >= 300 {
		t.Fatalf("B2 staging PUT returned HTTP %d", put.StatusCode)
	}
	_, err = db.Exec(ctx, `insert into media_objects(owner_id,namespace,object_key,content_type,size_bytes,sha256,status,primary_verified_at)
		values($1,$2,$3,$4,$5,$6,'replication_pending',now())`, owner, item.namespace, key, item.contentType, item.size, item.expectedSHA)
	if err != nil {
		t.Fatalf("record durable media object: %v", err)
	}
	_, err = db.Exec(ctx, `insert into media_outbox(owner_id,namespace,object_key,operation,status,available_at)
		values($1,$2,$3,'replicate','pending',now())`, owner, item.namespace, key)
	if err != nil {
		t.Fatalf("queue B2-to-R2 replication: %v", err)
	}
	if err := s.processOne(ctx); err != nil {
		t.Fatalf("process B2-to-R2 replication: %v", err)
	}
	assertState := func(want string) {
		t.Helper()
		var got string
		if err := db.QueryRow(ctx, `select status from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`, owner, item.namespace, key).Scan(&got); err != nil {
			t.Fatalf("read media lifecycle state: %v", err)
		}
		if got != want {
			t.Fatalf("media lifecycle state=%q, want %q", got, want)
		}
	}
	assertState("protected")
	if ok, err := s.originMatches(ctx, replica, item); err != nil || !ok {
		t.Fatalf("R2 replica did not pass exact size/content-type/SHA-256 read-back: ok=%v err=%v", ok, err)
	}

	// Simulate loss of the B2 primary. Reconciliation must quarantine the object,
	// restore B2 only from the verified R2 replica, and verify the restored B2 bytes.
	lost, err := s.signedRequest(ctx, primary, http.MethodDelete, key, nil, "")
	if err != nil {
		t.Fatalf("simulate B2 primary loss: %v", err)
	}
	io.Copy(io.Discard, io.LimitReader(lost.Body, 4096))
	lost.Body.Close()
	if lost.StatusCode != http.StatusOK && lost.StatusCode != http.StatusNoContent && lost.StatusCode != http.StatusNotFound {
		t.Fatalf("simulated B2 primary loss returned HTTP %d", lost.StatusCode)
	}
	if err := s.reconcileOne(ctx, item); err != nil {
		t.Fatalf("reconcile missing B2 primary against R2 replica: %v", err)
	}
	assertState("restore_pending")
	if err := s.processOne(ctx); err != nil {
		t.Fatalf("restore B2 primary from verified R2 replica: %v", err)
	}
	assertState("protected")
	if ok, err := s.originMatches(ctx, primary, item); err != nil || !ok {
		t.Fatalf("restored B2 primary did not pass full read-back: ok=%v err=%v", ok, err)
	}

	// Exercise the authenticated delete API and durable worker, then verify both
	// providers return 404 rather than merely accepting a delete request.
	body := strings.NewReader(`{"requester_id":"` + owner + `","owner_id":"` + owner + `","namespace":"media-private","key":"` + key + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/media/delete", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Testagram-API-Version", apiVersion)
	rec := httptest.NewRecorder()
	s.delete(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("authenticated delete API returned HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if err := s.processOne(ctx); err != nil {
		t.Fatalf("process durable two-provider deletion: %v", err)
	}
	assertState("deleted")
	for _, store := range []s3Store{primary, replica} {
		head, err := s.signedRequest(ctx, store, http.MethodHead, key, nil, "")
		if err != nil {
			t.Fatalf("%s HEAD after deletion: %v", store.name, err)
		}
		io.Copy(io.Discard, io.LimitReader(head.Body, 4096))
		head.Body.Close()
		if head.StatusCode != http.StatusNotFound {
			t.Fatalf("%s HEAD after deletion returned HTTP %d, want 404", store.name, head.StatusCode)
		}
	}
}

// TestLiveIdentityEvidenceLifecycle proves the separately authenticated private
// evidence routes against real staging B2/R2 buckets and an isolated PostgreSQL DB.
func TestLiveIdentityEvidenceLifecycle(t *testing.T) {
	if os.Getenv("TESTAGRAM_LIVE_MEDIA_SMOKE") != "true" {
		t.Skip("set TESTAGRAM_LIVE_MEDIA_SMOKE=true only in the manual provider-smoke workflow")
	}
	databaseURL := strings.TrimSpace(os.Getenv("MEDIA_DATABASE_URL"))
	mediaToken := strings.TrimSpace(os.Getenv("MEDIA_INTERNAL_TOKEN"))
	identityToken := strings.TrimSpace(os.Getenv("IDENTITY_EVIDENCE_INTERNAL_TOKEN"))
	if databaseURL == "" || len(mediaToken) < 32 || len(identityToken) < 32 || mediaToken == identityToken {
		t.Fatal("live identity evidence smoke requires the media database and two distinct 32+ character service tokens")
	}
	readStore := func(prefix, name string) s3Store {
		store := s3Store{name:name, endpoint:os.Getenv(prefix+"_ENDPOINT"), region:os.Getenv(prefix+"_REGION"), bucket:os.Getenv(prefix+"_BUCKET"), access:os.Getenv(prefix+"_ACCESS_KEY_ID"), secret:os.Getenv(prefix+"_SECRET_ACCESS_KEY")}
		if err := validateStore(store); err != nil { t.Fatalf("%s smoke configuration invalid: %v", name, err) }
		return store
	}
	primary := readStore("MEDIA_SMOKE_B2", "backblaze_b2")
	replica := readStore("MEDIA_SMOKE_R2", "cloudflare_r2")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil { t.Fatalf("open isolated media smoke database: %v", err) }
	defer db.Close()
	if err := db.Ping(ctx); err != nil { t.Fatalf("media smoke database unavailable: %v", err) }

	s := &server{
		cfg: config{token:mediaToken, identityToken:identityToken, primary:primary, replica:replica, uploadTTL:45*time.Minute, downloadTTL:5*time.Minute},
		db:db,
		http:&http.Client{Timeout:60*time.Second, CheckRedirect:func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/v1/media/status", s.status)
	mux.HandleFunc("/internal/v1/identity-evidence/readyz", s.identityReady)
	mux.HandleFunc("/internal/v1/identity-evidence/upload-url", s.uploadURL)
	mux.HandleFunc("/internal/v1/identity-evidence/verify", s.verify)
	mux.HandleFunc("/internal/v1/identity-evidence/status", s.status)
	mux.HandleFunc("/internal/v1/identity-evidence/download-url", s.downloadURL)
	mux.HandleFunc("/internal/v1/identity-evidence/delete", s.delete)
	api := httptest.NewServer(mux)
	defer api.Close()
	apiClient := &http.Client{Timeout:60*time.Second, CheckRedirect:func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	owner := "00000000-0000-4000-8000-000000000001"
	session := "10000000-0000-4000-8000-000000000002"
	evidence := "20000000-0000-4000-8000-000000000003"
	nonce, err := randomID()
	if err != nil { t.Fatal(err) }
	key := "identity/" + owner + "/" + session + "/" + evidence + ".jpg-" + nonce
	payload := []byte{0xff,0xd8,0xff,0xe0,0x00,0x10,0x4a,0x46,0x49,0x46,0x00}
	item := reconcileItem{owner:owner, namespace:"identity-evidence-private", key:key, contentType:"image/jpeg", size:int64(len(payload)), expectedSHA:hashHex(payload)}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		for _, store := range []s3Store{primary, replica} {
			resp, cleanupErr := s.signedRequest(cleanupCtx, store, http.MethodDelete, key, nil, "")
			if cleanupErr == nil { io.Copy(io.Discard, io.LimitReader(resp.Body,4096)); resp.Body.Close() }
		}
		_, _ = db.Exec(cleanupCtx, "delete from media_operations where owner_id=$1 and namespace=$2 and object_key=$3", owner, item.namespace, key)
		_, _ = db.Exec(cleanupCtx, "delete from media_outbox where owner_id=$1 and namespace=$2 and object_key=$3", owner, item.namespace, key)
		_, _ = db.Exec(cleanupCtx, "delete from media_upload_grants where owner_id=$1 and namespace=$2 and object_key=$3", owner, item.namespace, key)
		_, _ = db.Exec(cleanupCtx, "delete from media_objects where owner_id=$1 and namespace=$2 and object_key=$3", owner, item.namespace, key)
	}()

	call := func(method, route, token string, body any) *http.Response {
		t.Helper()
		var reader io.Reader
		if body != nil {
			encoded, marshalErr := json.Marshal(body)
			if marshalErr != nil { t.Fatal(marshalErr) }
			reader = bytes.NewReader(encoded)
		}
		req, requestErr := http.NewRequestWithContext(ctx, method, api.URL+route, reader)
		if requestErr != nil { t.Fatal(requestErr) }
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Testagram-API-Version", apiVersion)
		if body != nil { req.Header.Set("Content-Type", "application/json") }
		resp, requestErr := apiClient.Do(req)
		if requestErr != nil { t.Fatal(requestErr) }
		return resp
	}
	decode := func(resp *http.Response, target any, wantStatus int) {
		t.Helper()
		defer resp.Body.Close()
		if resp.StatusCode != wantStatus {
			body, _ := io.ReadAll(io.LimitReader(resp.Body,4096))
			t.Fatalf("identity evidence API returned HTTP %d, want %d: %s", resp.StatusCode, wantStatus, string(body))
		}
		if target != nil { if err := json.NewDecoder(resp.Body).Decode(target); err != nil { t.Fatal(err) } }
	}

	ready := call(http.MethodGet, "/internal/v1/identity-evidence/readyz", identityToken, nil)
	var readyState map[string]any
	decode(ready, &readyState, http.StatusOK)
	if readyState["status"] != "ready" || readyState["namespace"] != "identity-evidence-private" {
		t.Fatalf("authenticated identity evidence readiness returned unexpected response: %+v", readyState)
	}

	// Neither service credential can cross into the other route family.
	badIdentity := call(http.MethodGet, "/internal/v1/identity-evidence/status", mediaToken, nil)
	if badIdentity.StatusCode != http.StatusUnauthorized { badIdentity.Body.Close(); t.Fatalf("general media token accepted on identity route: %d", badIdentity.StatusCode) }
	badMedia := call(http.MethodGet, "/internal/v1/media/status", identityToken, nil)
	if badMedia.StatusCode != http.StatusUnauthorized { badMedia.Body.Close(); t.Fatalf("identity token accepted on general media route: %d", badMedia.StatusCode) }

	var upload map[string]any
	uploadResp := call(http.MethodPost, "/internal/v1/identity-evidence/upload-url", identityToken, uploadRequest{
		OwnerID:owner, Namespace:item.namespace, Key:key, ContentType:item.contentType, SizeBytes:item.size,
	})
	decode(uploadResp, &upload, http.StatusCreated)
	uploadURL, ok := upload["upload_url"].(string)
	if !ok || !strings.HasPrefix(uploadURL, "https://") { t.Fatal("Data Centre returned a non-HTTPS identity evidence upload grant") }

	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(payload))
	if err != nil { t.Fatal(err) }
	putReq.Header.Set("Content-Type", item.contentType)
	putResp, err := apiClient.Do(putReq)
	if err != nil { t.Fatalf("upload identity evidence to B2: %v", err) }
	io.Copy(io.Discard, io.LimitReader(putResp.Body,4096)); putResp.Body.Close()
	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 { t.Fatalf("B2 identity evidence PUT returned HTTP %d", putResp.StatusCode) }

	verifyResp := call(http.MethodPost, "/internal/v1/identity-evidence/verify", identityToken, verifyRequest{
		uploadRequest:uploadRequest{OwnerID:owner, Namespace:item.namespace, Key:key, ContentType:item.contentType, SizeBytes:item.size},
		SHA256:item.expectedSHA,
	})
	decode(verifyResp, nil, http.StatusAccepted)
	if err := s.processOne(ctx); err != nil { t.Fatalf("replicate identity evidence B2 -> R2: %v", err) }

	query := url.Values{"owner_id":[]string{owner}, "namespace":[]string{item.namespace}, "key":[]string{key}}
	statusRoute := "/internal/v1/identity-evidence/status?" + query.Encode()
	var state map[string]any
	statusResp := call(http.MethodGet, statusRoute, identityToken, nil)
	decode(statusResp, &state, http.StatusOK)
	if state["status"] != "protected" || state["sha256"] != item.expectedSHA || state["size_bytes"] != float64(item.size) || state["content_type"] != item.contentType {
		t.Fatalf("identity evidence did not reach protected state with matching metadata: %+v", state)
	}
	if ok, err := s.originMatches(ctx, replica, item); err != nil || !ok { t.Fatalf("R2 identity evidence read-back failed: ok=%v err=%v", ok, err) }

	denied := call(http.MethodPost, "/internal/v1/identity-evidence/download-url", identityToken, downloadRequest{
		RequesterID:"00000000-0000-4000-8000-000000000099", OwnerID:owner, Namespace:item.namespace, Key:key,
	})
	if denied.StatusCode != http.StatusForbidden { denied.Body.Close(); t.Fatalf("cross-application evidence download returned HTTP %d, want 403", denied.StatusCode) }

	var download map[string]any
	downloadResp := call(http.MethodPost, "/internal/v1/identity-evidence/download-url", identityToken, downloadRequest{
		RequesterID:owner, OwnerID:owner, Namespace:item.namespace, Key:key,
	})
	decode(downloadResp, &download, http.StatusOK)
	downloadURL, ok := download["url"].(string)
	if !ok || !strings.HasPrefix(downloadURL, "https://") { t.Fatal("Data Centre returned an unsafe identity evidence download URL") }
	getResp, err := apiClient.Get(downloadURL)
	if err != nil { t.Fatalf("download protected identity evidence: %v", err) }
	if getResp.StatusCode != http.StatusOK { getResp.Body.Close(); t.Fatalf("identity evidence GET returned HTTP %d", getResp.StatusCode) }
	if !digestMatches(getResp.Body, item.size, item.expectedSHA) { getResp.Body.Close(); t.Fatal("identity evidence download failed exact size/SHA-256 verification") }
	getResp.Body.Close()

	// Restore the primary from the independently verified replica.
	lost, err := s.signedRequest(ctx, primary, http.MethodDelete, key, nil, "")
	if err != nil { t.Fatalf("simulate identity evidence primary loss: %v", err) }
	io.Copy(io.Discard, io.LimitReader(lost.Body,4096)); lost.Body.Close()
	if lost.StatusCode != http.StatusOK && lost.StatusCode != http.StatusNoContent && lost.StatusCode != http.StatusNotFound { t.Fatalf("simulate B2 loss returned HTTP %d", lost.StatusCode) }
	if err := s.reconcileOne(ctx, item); err != nil { t.Fatalf("reconcile missing identity evidence primary: %v", err) }
	if err := s.processOne(ctx); err != nil { t.Fatalf("restore identity evidence B2 from R2: %v", err) }
	if ok, err := s.originMatches(ctx, primary, item); err != nil || !ok { t.Fatalf("restored B2 identity evidence read-back failed: ok=%v err=%v", ok, err) }

	deleteResp := call(http.MethodPost, "/internal/v1/identity-evidence/delete", identityToken, deleteRequest{
		RequesterID:owner, OwnerID:owner, Namespace:item.namespace, Key:key,
	})
	decode(deleteResp, nil, http.StatusAccepted)
	if err := s.processOne(ctx); err != nil { t.Fatalf("durable identity evidence deletion: %v", err) }
	statusResp = call(http.MethodGet, statusRoute, identityToken, nil)
	decode(statusResp, &state, http.StatusOK)
	if state["status"] != "deleted" { t.Fatalf("identity evidence deletion state=%v, want deleted", state["status"]) }
	for _, store := range []s3Store{primary, replica} {
		head, err := s.signedRequest(ctx, store, http.MethodHead, key, nil, "")
		if err != nil { t.Fatalf("%s HEAD after identity evidence deletion: %v", store.name, err) }
		io.Copy(io.Discard, io.LimitReader(head.Body,4096)); head.Body.Close()
		if head.StatusCode != http.StatusNotFound { t.Fatalf("%s retained identity evidence after deletion (HTTP %d)", store.name, head.StatusCode) }
	}
}
