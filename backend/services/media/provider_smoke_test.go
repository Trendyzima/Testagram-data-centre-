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
		t.Skip("set TESTAGRAM_LIVE_MEDIA_SMOKE=true only for an operator-approved isolated non-production provider run")
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
		t.Skip("set TESTAGRAM_LIVE_MEDIA_SMOKE=true only for an operator-approved isolated non-production provider run")
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
	metadataPrimary := s3Store{
		name: "instabox_vps_metadata",
		endpoint: os.Getenv("MEDIA_SMOKE_MINIO_METADATA_ENDPOINT"),
		region: os.Getenv("MEDIA_SMOKE_MINIO_METADATA_REGION"),
		bucket: os.Getenv("MEDIA_SMOKE_MINIO_METADATA_BUCKET"),
		access: os.Getenv("MEDIA_SMOKE_MINIO_METADATA_ACCESS_KEY_ID"),
		secret: os.Getenv("MEDIA_SMOKE_MINIO_METADATA_SECRET_ACCESS_KEY"),
	}
	metadataReplica := replica
	metadataReplica.name = "cloudflare_r2_metadata"
	metadataReplica.bucket = os.Getenv("MEDIA_SMOKE_R2_METADATA_BUCKET")
	if err := validateStore(metadataPrimary); err != nil {
		t.Fatalf("VPS MinIO metadata smoke configuration invalid: %v", err)
	}
	if err := validateStore(metadataReplica); err != nil {
		t.Fatalf("R2 metadata smoke configuration invalid: %v", err)
	}
	if metadataReplica.bucket == replica.bucket {
		t.Fatal("R2 metadata sidecar bucket must be distinct from the media bucket")
	}
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
		cfg: config{token: token, primary: primary, replica: replica, metadataPrimary: metadataPrimary, metadataReplica: metadataReplica},
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
		for _, store := range []s3Store{primary, replica, metadataPrimary, metadataReplica} {
			for _, objectKey := range []string{key, key + ".metadata.json"} {
				resp, cleanupErr := s.signedRequest(cleanupCtx, store, http.MethodDelete, objectKey, nil, "")
				if cleanupErr == nil {
					io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
					resp.Body.Close()
				}
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
	for _, store := range []s3Store{metadataPrimary, metadataReplica} {
		head, err := s.signedRequest(ctx, store, http.MethodHead, key+".metadata.json", nil, "")
		if err != nil {
			t.Fatalf("%s metadata HEAD after deletion: %v", store.name, err)
		}
		io.Copy(io.Discard, io.LimitReader(head.Body, 4096))
		head.Body.Close()
		if head.StatusCode != http.StatusNotFound {
			t.Fatalf("%s retained media metadata sidecar after deletion (HTTP %d)", store.name, head.StatusCode)
		}
	}
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
// evidence routes against a dedicated staging B2 identity bucket and an isolated PostgreSQL DB.
func TestLiveIdentityEvidenceLifecycle(t *testing.T) {
	if os.Getenv("TESTAGRAM_LIVE_MEDIA_SMOKE") != "true" {
		t.Skip("set TESTAGRAM_LIVE_MEDIA_SMOKE=true only for an operator-approved isolated non-production provider run")
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
	identityPrimary := primary
	identityPrimary.name = "backblaze_b2_identity_evidence"
	identityPrimary.bucket = os.Getenv("MEDIA_SMOKE_B2_IDENTITY_BUCKET")
	identityPrimary.access = os.Getenv("MEDIA_SMOKE_B2_IDENTITY_ACCESS_KEY_ID")
	identityPrimary.secret = os.Getenv("MEDIA_SMOKE_B2_IDENTITY_SECRET_ACCESS_KEY")
	if identityPrimary.access == primary.access || identityPrimary.secret == primary.secret {
		t.Fatal("identity evidence B2 credentials must be distinct from ordinary media B2 credentials")
	}
	if err := validateStore(identityPrimary); err != nil { t.Fatalf("B2 identity evidence smoke configuration invalid: %v", err) }
	if identityPrimary.bucket == primary.bucket {
		t.Fatal("B2 identity evidence smoke bucket must be distinct from the ordinary media bucket")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil { t.Fatalf("open isolated media smoke database: %v", err) }
	defer db.Close()
	if err := db.Ping(ctx); err != nil { t.Fatalf("media smoke database unavailable: %v", err) }

	s := &server{
		cfg: config{token:mediaToken, identityToken:identityToken, primary:primary, replica:replica, identityPrimary:identityPrimary, uploadTTL:45*time.Minute, downloadTTL:5*time.Minute},
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
		for _, store := range []s3Store{identityPrimary} {
			for _, objectKey := range []string{key, key + ".metadata.json"} {
				resp, cleanupErr := s.signedRequest(cleanupCtx, store, http.MethodDelete, objectKey, nil, "")
				if cleanupErr == nil { io.Copy(io.Discard, io.LimitReader(resp.Body,4096)); resp.Body.Close() }
			}
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
	if readyState["status"] != "ready" || readyState["namespace"] != "identity-evidence-private" || readyState["provider"] != "backblaze_b2" {
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
	if err := s.processOne(ctx); err != nil { t.Fatalf("verify identity evidence in its B2 bucket: %v", err) }

	query := url.Values{"owner_id":[]string{owner}, "namespace":[]string{item.namespace}, "key":[]string{key}}
	statusRoute := "/internal/v1/identity-evidence/status?" + query.Encode()
	var state map[string]any
	statusResp := call(http.MethodGet, statusRoute, identityToken, nil)
	decode(statusResp, &state, http.StatusOK)
	if state["status"] != "protected" || state["sha256"] != item.expectedSHA || state["size_bytes"] != float64(item.size) || state["content_type"] != item.contentType {
		t.Fatalf("identity evidence did not reach protected state with matching metadata: %+v", state)
	}
	identityStore, identityReplica := s.storesForNamespace("identity-evidence-private")
	if identityStore.bucket != identityPrimary.bucket || identityStore.name != "backblaze_b2_identity_evidence" || identityReplica.bucket != "" {
		t.Fatalf("identity evidence namespace must resolve to B2 only; primary=%q replica=%q", identityStore.name, identityReplica.name)
	}
	if state["replica_verified_at"] != nil {
		t.Fatalf("B2-only identity evidence unexpectedly reports a verified replica: %+v", state)
	}

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

	// Identity evidence has no serving replica or cross-provider restore path.
	if err := s.replicate(ctx, owner, item.namespace, key); err == nil { t.Fatal("identity evidence replication unexpectedly succeeded") }
	if err := s.restorePrimary(ctx, owner, item.namespace, key); err == nil { t.Fatal("identity evidence cross-provider restore unexpectedly succeeded") }

	deleteResp := call(http.MethodPost, "/internal/v1/identity-evidence/delete", identityToken, deleteRequest{
		RequesterID:owner, OwnerID:owner, Namespace:item.namespace, Key:key,
	})
	decode(deleteResp, nil, http.StatusAccepted)
	if err := s.processOne(ctx); err != nil { t.Fatalf("durable identity evidence deletion: %v", err) }
	statusResp = call(http.MethodGet, statusRoute, identityToken, nil)
	decode(statusResp, &state, http.StatusOK)
	if state["status"] != "deleted" { t.Fatalf("identity evidence deletion state=%v, want deleted", state["status"]) }
	var deletionAuditCount int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM media_operations WHERE owner_id=$1 AND namespace='identity-evidence-private' AND object_key=$2 AND operation='delete' AND outcome='succeeded'`, owner, key).Scan(&deletionAuditCount); err != nil {
		t.Fatalf("read identity evidence deletion audit: %v", err)
	}
	if deletionAuditCount != 1 { t.Fatalf("identity evidence physical deletion audit events=%d, want exactly one durable success event", deletionAuditCount) }
	for _, store := range []s3Store{identityPrimary} {
		head, err := s.signedRequest(ctx, store, http.MethodHead, key+".metadata.json", nil, "")
		if err != nil { t.Fatalf("%s identity metadata HEAD after deletion: %v", store.name, err) }
		io.Copy(io.Discard, io.LimitReader(head.Body,4096)); head.Body.Close()
		if head.StatusCode != http.StatusNotFound { t.Fatalf("%s retained identity metadata sidecar after deletion (HTTP %d)", store.name, head.StatusCode) }
	}
	for _, store := range []s3Store{identityPrimary} {
		head, err := s.signedRequest(ctx, store, http.MethodHead, key, nil, "")
		if err != nil { t.Fatalf("%s HEAD after identity evidence deletion: %v", store.name, err) }
		io.Copy(io.Discard, io.LimitReader(head.Body,4096)); head.Body.Close()
		if head.StatusCode != http.StatusNotFound { t.Fatalf("%s retained identity evidence after deletion (HTTP %d)", store.name, head.StatusCode) }
	}
}


// TestLiveIdentityEvidenceProviderCredentialIsolation proves the Backblaze
// application keys themselves are bucket-scoped in opposite directions. This
// is stronger than checking the service bearer-token boundary alone.
func TestLiveIdentityEvidenceProviderCredentialIsolation(t *testing.T) {
	if os.Getenv("TESTAGRAM_LIVE_MEDIA_SMOKE") != "true" {
		t.Skip("requires explicit approval for isolated non-production B2 credential-scope checks")
	}
	store := func(name, bucket, access, secret string) s3Store {
		s := s3Store{name:name, endpoint:os.Getenv("MEDIA_SMOKE_B2_ENDPOINT"), region:os.Getenv("MEDIA_SMOKE_B2_REGION"), bucket:bucket, access:access, secret:secret}
		if err := validateStore(s); err != nil { t.Fatalf("%s configuration invalid: %v", name, err) }
		return s
	}
	ordinary := store("backblaze_b2_media", os.Getenv("MEDIA_SMOKE_B2_BUCKET"), os.Getenv("MEDIA_SMOKE_B2_ACCESS_KEY_ID"), os.Getenv("MEDIA_SMOKE_B2_SECRET_ACCESS_KEY"))
	identity := store("backblaze_b2_identity_evidence", os.Getenv("MEDIA_SMOKE_B2_IDENTITY_BUCKET"), os.Getenv("MEDIA_SMOKE_B2_IDENTITY_ACCESS_KEY_ID"), os.Getenv("MEDIA_SMOKE_B2_IDENTITY_SECRET_ACCESS_KEY"))
	if ordinary.bucket == identity.bucket { t.Fatal("ordinary media and identity evidence must use separate B2 buckets") }
	if ordinary.access == identity.access || ordinary.secret == identity.secret { t.Fatal("ordinary media and identity evidence must use separate B2 application keys") }
	if ordinary.endpoint != identity.endpoint || ordinary.region != identity.region { t.Fatal("ordinary and identity evidence keys must target the same configured B2 S3 service") }

	id, err := randomID()
	if err != nil { t.Fatal(err) }
	ordinaryKey := "ci-smoke/credential-isolation-media-" + id
	identityKey := "ci-smoke/credential-isolation-identity-" + id
	payload := []byte("Testagram B2 bucket-scoped credential isolation " + id)
	s := &server{http:&http.Client{Timeout:30*time.Second, CheckRedirect:func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	defer func() {
		for _, target := range []struct{store s3Store; key string}{{ordinary,ordinaryKey},{identity,identityKey}} {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
			resp, cleanupErr := s.signedRequest(cleanupCtx,target.store,http.MethodDelete,target.key,nil,"")
			if cleanupErr == nil { io.Copy(io.Discard,io.LimitReader(resp.Body,4096)); resp.Body.Close() }
			cleanupCancel()
		}
	}()

	for _, target := range []struct{store s3Store; key string}{{ordinary,ordinaryKey},{identity,identityKey}} {
		resp, err := s.signedRequest(ctx,target.store,http.MethodPut,target.key,&lengthReader{Reader:bytes.NewReader(payload),n:int64(len(payload))},"application/octet-stream")
		if err != nil { t.Fatalf("%s test object upload failed: %v",target.store.name,err) }
		io.Copy(io.Discard,io.LimitReader(resp.Body,4096)); resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 { t.Fatalf("%s test object upload returned HTTP %d",target.store.name,resp.StatusCode) }
	}
	for _, denied := range []struct{store s3Store; key string; label string}{{identity,ordinaryKey,"identity key reading ordinary-media bucket"},{ordinary,identityKey,"ordinary-media key reading identity bucket"}} {
		resp, err := s.signedRequest(ctx,denied.store,http.MethodHead,denied.key,nil,"")
		if err != nil { t.Fatalf("%s request failed: %v",denied.label,err) }
		io.Copy(io.Discard,io.LimitReader(resp.Body,4096)); resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden { t.Fatalf("%s returned HTTP %d, want 403 AccessDenied",denied.label,resp.StatusCode) }
	}
}
