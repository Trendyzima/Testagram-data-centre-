package main

import (
	"context"
	"fmt"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

func TestMediaAuthRequiresBothCredentialAndVersion(t *testing.T) {
	s := &server{cfg: config{token: strings.Repeat("x", 40), identityToken: strings.Repeat("i", 40)}}
	for _, tc := range []struct{name, token, version string; want bool}{
		{"both", "Bearer "+s.cfg.token, "1", true},
		{"missing bearer", "", "1", false},
		{"raw token without scheme", s.cfg.token, "1", false},
		{"wrong scheme", "Basic "+s.cfg.token, "1", false},
		{"whitespace token", "Bearer "+s.cfg.token+" extra", "1", false},
		{"missing version", "Bearer "+s.cfg.token, "", false},
		{"wrong version", "Bearer "+s.cfg.token, "2", false},
		{"wrong token", "Bearer "+strings.Repeat("y", 40), "1", false},

	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/internal/v1/media/status", nil)
			r.Header.Set("Authorization", tc.token)
			r.Header.Set("X-Testagram-API-Version", tc.version)
			w := httptest.NewRecorder()
			if got := s.auth(w, r); got != tc.want { t.Fatalf("auth=%v want %v", got, tc.want) }
		})
	}
	t.Run("identity route requires distinct token", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/internal/v1/identity-evidence/status", nil)
		r.Header.Set("Authorization", "Bearer "+s.cfg.token)
		r.Header.Set("X-Testagram-API-Version", "1")
		w := httptest.NewRecorder()
		if s.auth(w, r) { t.Fatal("general media token was accepted on identity evidence route") }
		r.Header.Set("Authorization", "Bearer "+s.cfg.identityToken)
		w = httptest.NewRecorder()
		if !s.auth(w, r) { t.Fatal("dedicated identity evidence token was rejected") }
	})

}

func TestMediaUploadValidation(t *testing.T) {
	if !validIdentity("testagram.user_123") { t.Fatal("Testagram owner IDs containing dot/underscore must be accepted") }
	if !validMediaKey("users/testagram.user_123/0123456789abcdef0123456789abcdef", "testagram.user_123") { t.Fatal("canonical Testagram owner-bound key rejected") }
	base := uploadRequest{OwnerID:"owner-1", Namespace:"media-private", Key:"users/owner-1/0123456789abcdef0123456789abcdef", ContentType:"video/mp4", SizeBytes:1024}
	if !validUpload(base) { t.Fatal("valid upload rejected") }
	cases := []uploadRequest{base, base, base, base, base, base, base, base, base}
	cases[0].Namespace = "public"
	cases[1].Key = "../escape"
	cases[2].ContentType = "video/mp4\r\nX-Evil: yes"
	cases[3].SizeBytes = 0
	cases[4].Key = "/absolute/key"
	cases[5].Key = "users//object"
	cases[6].Key = "users/another-owner/0123456789abcdef0123456789abcdef"
	cases[7].ContentType = "application/octet-stream"
	cases[8].SizeBytes = (5 << 30) + 1
	for i, in := range cases { if validUpload(in) { t.Fatalf("invalid upload case %d accepted", i) } }
}

func TestS3PresignBindsMethodAndContentType(t *testing.T) {
	store := s3Store{name:"backblaze_b2", endpoint:"https://s3.example.invalid", region:"us-west-004", bucket:"test-bucket", access:"access-key", secret:strings.Repeat("s",40)}
	put, err := presign(store, "PUT", "owner/object.mp4", 10*time.Minute, "video/mp4")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(put, "X-Amz-Signature=") || !strings.Contains(put, "X-Amz-SignedHeaders=content-type%3Bhost") { t.Fatalf("unexpected signed upload URL: %s", put) }
	get, err := presign(store, "GET", "owner/object.mp4", 5*time.Minute, "")
	if err != nil { t.Fatal(err) }
	if strings.Contains(get, "content-type") || put == get { t.Fatal("GET and PUT grants must be distinct") }
	head, err := presign(store, "HEAD", "owner/object.mp4", 5*time.Minute, "")
	if err != nil { t.Fatal(err) }
	if head == get || head == put { t.Fatal("HEAD grant must be distinct from GET and PUT") }
}

func TestS3StoreRequiresTLSAndCleanOrigin(t *testing.T) {
	base := s3Store{name:"backblaze_b2",endpoint:"https://s3.example.invalid",region:"us-west-004",bucket:"test-bucket",access:"access-key",secret:strings.Repeat("s",40)}
	if err := validateStore(base); err != nil { t.Fatalf("valid TLS endpoint rejected: %v",err) }
	base.endpoint = "http://s3.example.invalid"
	if err := validateStore(base); err == nil { t.Fatal("plaintext S3 endpoint accepted") }
	base.endpoint = "https://s3.example.invalid?token=unsafe"
	if err := validateStore(base); err == nil { t.Fatal("S3 endpoint query string accepted") }
}

func TestObjectLockKeyIsPostgresTextSafeAndUnambiguous(t *testing.T) {
	a := objectLockKey("owner", "media-private", "a|b")
	b := objectLockKey("owner|media-private", "a", "b")
	if strings.ContainsRune(a, '\x00') || strings.ContainsRune(b, '\x00') {
		t.Fatal("advisory lock key must be valid PostgreSQL text")
	}
	if a == b {
		t.Fatal("length-prefixed lock keys must not collide across tuple boundaries")
	}
}

func TestObjectURLRejectsTraversalAndUnsafeBucket(t *testing.T) {
	store := s3Store{name:"b2",endpoint:"https://s3.example.invalid",region:"us-west-004",bucket:"test-bucket",access:"key",secret:strings.Repeat("s",40)}
	if _,err:=objectURL(store,"users/../secret");err==nil{t.Fatal("traversal key accepted")}
	store.bucket="../bucket"
	if _,err:=objectURL(store,"users/user/object");err==nil{t.Fatal("unsafe bucket path accepted")}
}

func TestDigestMatchesExactSizeAndSHA256(t *testing.T) {
	payload := "testagram-media-payload"
	sum := sha256.Sum256([]byte(payload))
	expected := hex.EncodeToString(sum[:])
	if !digestMatches(strings.NewReader(payload), int64(len(payload)), expected) {
		t.Fatal("matching object digest rejected")
	}
	if digestMatches(strings.NewReader(payload+"x"), int64(len(payload)), expected) {
		t.Fatal("oversized object accepted")
	}
	if digestMatches(strings.NewReader(payload[:len(payload)-1]), int64(len(payload)), expected) {
		t.Fatal("truncated object accepted")
	}
	if digestMatches(strings.NewReader(payload), int64(len(payload)), strings.Repeat("0",64)) {
		t.Fatal("wrong digest accepted")
	}
}


func TestPrivateDockerMinIOEndpointAllowsHTTPOnlyForObjectStorage(t *testing.T) {
	store := s3Store{name:"instabox_vps_metadata",endpoint:"http://object-storage:9000",region:"us-east-1",bucket:"testagram-media-metadata",access:"metadata-key",secret:strings.Repeat("s",40)}
	if err:=validateStore(store);err!=nil{t.Fatalf("private Docker MinIO endpoint rejected: %v",err)}
	store.endpoint="http://external.example.invalid"
	if err:=validateStore(store);err==nil{t.Fatal("non-private plaintext S3 endpoint accepted")}
}

func TestMetadataSidecarsWriteReadBackAndDeleteOnVPSAndR2(t *testing.T) {
	objects := map[string][]byte{}
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		key:=r.URL.Path
		switch r.Method {
		case http.MethodPut:
			body,err:=io.ReadAll(r.Body);if err!=nil{http.Error(w,"read failed",500);return};objects[key]=body;w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			body,ok:=objects[key];if !ok{http.NotFound(w,r);return};w.Header().Set("Content-Type","application/json");w.Header().Set("Content-Length",strconv.Itoa(len(body)));_,_=w.Write(body)
		case http.MethodDelete:
			delete(objects,key);w.WriteHeader(http.StatusNoContent)
		default: http.Error(w,"method not allowed",405)
		}
	}))
	defer ts.Close()
	store:=func(name,bucket string)s3Store{return s3Store{name:name,endpoint:ts.URL,region:"us-east-1",bucket:bucket,access:"metadata-key",secret:strings.Repeat("s",40)}}
	s:=&server{cfg:config{metadataPrimary:store("instabox_vps_metadata","vps-metadata"),metadataReplica:store("cloudflare_r2_metadata","r2-metadata")},http:ts.Client()}
	item:=reconcileItem{owner:"owner-1",namespace:"media-private",key:"users/owner-1/0123456789abcdef0123456789abcdef",contentType:"image/webp",size:123,expectedSHA:strings.Repeat("a",64)}
	if err:=s.writeMetadataSidecars(context.Background(),item);err!=nil{t.Fatalf("write metadata sidecars: %v",err)}
	metadataKey:=item.key+".metadata.json"
	for _,bucket:=range []string{"vps-metadata","r2-metadata"}{if len(objects["/"+bucket+"/"+metadataKey])==0{t.Fatalf("metadata missing from %s",bucket)}}
	if err:=s.deleteMetadataSidecars(context.Background(),item.namespace,item.key);err!=nil{t.Fatalf("delete metadata sidecars: %v",err)}
	for _,bucket:=range []string{"vps-metadata","r2-metadata"}{if _,ok:=objects["/"+bucket+"/"+metadataKey];ok{t.Fatalf("metadata remains in %s",bucket)}}
}

func TestIdentityEvidenceNamespaceIsPrivateAndBoundToApplication(t *testing.T) {
	owner := "00000000-0000-4000-8000-000000000001"
	session := "10000000-0000-4000-8000-000000000002"
	evidence := "20000000-0000-4000-8000-000000000003"
	key := "identity/" + owner + "/" + session + "/" + evidence + ".jpg-" + strings.Repeat("a", 43)
	valid := uploadRequest{OwnerID: owner, Namespace: "identity-evidence-private", Key: key, ContentType: "image/jpeg", SizeBytes: 1024}
	if !validUpload(valid) { t.Fatal("valid private identity evidence upload rejected") }
	if !validObjectForNamespace("identity-evidence-private", key, owner) { t.Fatal("valid application-bound evidence key rejected") }
	if validObjectForNamespace("identity-evidence-private", key, "00000000-0000-4000-8000-000000000099") { t.Fatal("evidence key accepted for a different application") }

	invalid := []uploadRequest{valid, valid, valid, valid, valid, valid}
	invalid[0].Namespace = "media-public"
	invalid[1].ContentType = "application/octet-stream"
	invalid[2].ContentType = "application/pdf"
	invalid[2].Key = "identity/" + owner + "/" + session + "/" + evidence + ".pdf-" + strings.Repeat("a", 43)
	invalid[3].SizeBytes = (15 << 20) + 1
	invalid[4].Key = "identity/" + owner + "/../" + evidence + ".jpg-" + strings.Repeat("a", 43)
	invalid[5].Key = "identity/" + owner + "/" + session + "/" + evidence + ".exe-" + strings.Repeat("a", 43)
	if !validUpload(invalid[2]) { t.Fatal("PDF identity evidence should be accepted") }
	for i, item := range invalid {
		if i == 2 { continue }
		if validUpload(item) { t.Fatalf("invalid identity evidence case %d accepted", i) }
	}
	if validObjectForNamespace("identity-evidence-private", key, owner) && validObjectForNamespace("media-private", key, owner) {
		t.Fatal("identity evidence key crossed into general media namespace")
	}
}

func TestIdentityEvidenceNamespaceRequiresDedicatedRoute(t *testing.T) {
	s := &server{cfg: config{identityEnabled: true}}
	for _, tc := range []struct{ path, namespace string; want bool }{
		{"/internal/v1/media/upload-url", "media-private", true},
		{"/internal/v1/identity-evidence/upload-url", "identity-evidence-private", true},
		{"/internal/v1/media/upload-url", "identity-evidence-private", false},
		{"/internal/v1/identity-evidence/upload-url", "media-private", false},
	} {
		r := httptest.NewRequest("POST", tc.path, nil)
		w := httptest.NewRecorder()
		if got := s.namespaceAllowedForRoute(w, r, tc.namespace); got != tc.want {
			t.Fatalf("namespace route %s namespace %s = %v, want %v", tc.path, tc.namespace, got, tc.want)
		}
	}
}

func TestIdentityEvidenceReadinessRequiresDedicatedCredential(t *testing.T) {
	s := &server{cfg: config{token: strings.Repeat("m", 40), identityToken: strings.Repeat("i", 40)}}
	request := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/internal/v1/identity-evidence/readyz", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Testagram-API-Version", apiVersion)
		w := httptest.NewRecorder()
		s.identityReady(w, r)
		return w
	}
	if got := request(s.cfg.token).Code; got != http.StatusUnauthorized {
		t.Fatalf("general media token identity readiness status=%d, want 401", got)
	}
	if got := request(s.cfg.identityToken).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("identity evidence token should pass auth then fail closed on missing DB: status=%d, want 503", got)
	}
}

func TestMediaReadinessRequiresDedicatedMediaCredential(t *testing.T) {
	s := &server{cfg: config{token: strings.Repeat("m", 40), identityToken: strings.Repeat("i", 40)}}
	request := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/internal/v1/media/readyz", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Testagram-API-Version", apiVersion)
		w := httptest.NewRecorder()
		s.mediaReady(w, r)
		return w
	}
	if got := request(s.cfg.identityToken).Code; got != http.StatusUnauthorized {
		t.Fatalf("identity evidence token media readiness status=%d, want 401", got)
	}
	if got := request(s.cfg.token).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("media token should pass auth then fail closed on missing DB: status=%d, want 503", got)
	}
}

func TestIdentityEvidenceUsesDedicatedB2OnlyBucket(t *testing.T) {
 s := &server{cfg: config{identityEnabled:true,
  primary:s3Store{name:"b2-media",bucket:"media-b2",access:"media-key",secret:"media-secret"},
  replica:s3Store{name:"r2-media",bucket:"media-r2"},
  identityPrimary:s3Store{name:"backblaze_b2_identity_evidence",bucket:"identity-b2",access:"identity-key",secret:"identity-secret"},
  metadataPrimary:s3Store{name:"vps-metadata",bucket:"metadata-vps"},
  metadataReplica:s3Store{name:"r2-metadata",bucket:"metadata-r2"},
 }}
 p,r := s.storesForNamespace("identity-evidence-private")
 if p.bucket!="identity-b2" || p.name!="backblaze_b2_identity_evidence" { t.Fatalf("identity evidence routed to %q/%q",p.name,p.bucket) }
 if p.access=="media-key" || p.secret=="media-secret" { t.Fatal("identity evidence reused ordinary media provider credentials") }
 if r.bucket!="" || r.name!="" { t.Fatalf("identity evidence unexpectedly has a replica: %#v",r) }
 p,r = s.storesForNamespace("media-private")
 if p.bucket!="media-b2" || r.bucket!="media-r2" { t.Fatalf("general media routing changed: %q/%q",p.bucket,r.bucket) }
 mp,mr := s.metadataStoresForNamespace("identity-evidence-private")
 if mp.bucket!="identity-b2" || mr.bucket!="" { t.Fatalf("identity metadata escaped dedicated B2 bucket: %q/%q",mp.bucket,mr.bucket) }
 mp,mr = s.metadataStoresForNamespace("media-private")
 if mp.bucket!="metadata-vps" || mr.bucket!="metadata-r2" { t.Fatalf("media metadata routing changed: %q/%q",mp.bucket,mr.bucket) }
}
func TestIdentityEvidenceCannotReplicateOrRestoreFromAnotherProvider(t *testing.T) {
 s := &server{}
 if err := s.replicate(context.Background(), "owner", "identity-evidence-private", "identity/key"); err == nil || !strings.Contains(err.Error(), "must never be replicated") {
  t.Fatalf("identity replication was not rejected: %v", err)
 }
 if err := s.restorePrimary(context.Background(), "owner", "identity-evidence-private", "identity/key"); err == nil || !strings.Contains(err.Error(), "B2-only") {
  t.Fatalf("identity restore from another provider was not rejected: %v", err)
 }
}

func TestMediaRetryDelaySecondsUsesBoundedExponentialJitter(t *testing.T) {
	tests := []struct {
		name string
		attempt int
		min int
		max int
	}{
		{name: "first attempt", attempt: 1, min: 4, max: 6},
		{name: "second attempt", attempt: 2, min: 8, max: 12},
		{name: "third attempt", attempt: 3, min: 16, max: 24},
		{name: "high attempt is capped", attempt: 20, min: 2880, max: 3600},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			seen := map[int]bool{}
			for i := 0; i < 200; i++ {
				got := mediaRetryDelaySeconds(tc.attempt)
				if got < tc.min || got > tc.max {
					t.Fatalf("retry delay %d outside [%d,%d]", got, tc.min, tc.max)
				}
				seen[got] = true
			}
			if len(seen) < 2 {
				t.Fatal("retry delay did not apply jitter across repeated samples")
			}
		})
	}
	if got := mediaRetryDelaySeconds(0); got < 4 || got > 6 {
		t.Fatalf("invalid attempt should normalize to first retry window, got %d", got)
	}
}

func TestMediaAuthRejectsAmbiguousAuthorizationHeaders(t *testing.T) {
 s := &server{cfg: config{token: strings.Repeat("x", 40), identityToken: strings.Repeat("i", 40)}}
 for _, values := range [][]string{
  {"Bearer " + s.cfg.token, "Bearer " + s.cfg.token},
  {"Bearer " + strings.Repeat("y", 40), "Bearer " + s.cfg.token},
  {"Bearer " + s.cfg.token + " "},
  {" Bearer " + s.cfg.token},
 } {
  r := httptest.NewRequest(http.MethodPost, "/internal/v1/media/upload-url", nil)
  for _, value := range values { r.Header.Add("Authorization", value) }
  r.Header.Set("X-Testagram-API-Version", apiVersion)
  w := httptest.NewRecorder()
  if s.auth(w, r) { t.Fatalf("ambiguous Authorization values accepted: %#v", values) }
  if w.Code != http.StatusUnauthorized { t.Fatalf("status=%d, want 401", w.Code) }
 }
}

func TestIdentityCredentialIsolationUsesSignedReadOnlyVersionListing(t *testing.T) {
	requests := make([]string, 0, 2)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasVersions := r.URL.Query()["versions"]
		if r.Method != http.MethodGet || !hasVersions ||
			r.URL.Query().Get("prefix") != "__testagram_identity_credential_scope_probe__/" ||
			r.URL.Query().Get("max-keys") != "1" {
			t.Errorf("credential isolation must use bounded read-only ListObjectVersions, got %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") == "" || r.Header.Get("X-Amz-Date") == "" {
			t.Error("credential isolation request omitted SigV4 authentication")
		}
		requests = append(requests, r.URL.Path+" "+r.Header.Get("Authorization"))
		if (r.URL.Path == "/media-bucket/" && strings.Contains(r.Header.Get("Authorization"), "Credential=identity-access/")) ||
			(r.URL.Path == "/identity-bucket/" && strings.Contains(r.Header.Get("Authorization"), "Credential=media-access/")) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte("<ListVersionsResult><IsTruncated>false</IsTruncated></ListVersionsResult>"))
	}))
	defer ts.Close()
	media := s3Store{name: "backblaze_b2", endpoint: ts.URL, region: "us-west-004", bucket: "media-bucket", access: "media-access", secret: strings.Repeat("m", 40)}
	identity := s3Store{name: "backblaze_b2_identity_evidence", endpoint: ts.URL, region: "us-west-004", bucket: "identity-bucket", access: "identity-access", secret: strings.Repeat("i", 40)}
	svc := &server{cfg: config{primary: media, identityPrimary: identity}, http: ts.Client()}
	if err := svc.verifyIdentityCredentialIsolation(context.Background()); err != nil {
		t.Fatalf("bucket-scoped credentials were rejected: %v", err)
	}
	if len(requests) != 2 || !strings.Contains(requests[0], "/media-bucket/") || !strings.Contains(requests[0], "Credential=identity-access/") ||
		!strings.Contains(requests[1], "/identity-bucket/") || !strings.Contains(requests[1], "Credential=media-access/") {
		t.Fatalf("unexpected cross-bucket credential probes: %v", requests)
	}
	identity.access = media.access
	svc.cfg.identityPrimary = identity
	if err := svc.verifyIdentityCredentialIsolation(context.Background()); err == nil {
		t.Fatal("shared B2 access key accepted for identity evidence")
	}
}

func TestIdentityEvidenceReadinessUsesReadOnlyVersionListing(t *testing.T) {
	methods := make([]string, 0, 2)
	paths := make([]string, 0, 2)
	var queryValues []url.Values
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		queryValues = append(queryValues, r.URL.Query())
		if r.Method != http.MethodGet || r.URL.Path != "/identity-evidence-private/" {
			t.Errorf("identity readiness must issue only read-only ListObjectVersions, got %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if _, ok := r.URL.Query()["versions"]; !ok || r.URL.Query().Get("prefix") != "__testagram_identity_readiness_probe__/" {
			t.Errorf("identity readiness must list only the reserved read-only probe prefix: %v", r.URL.Query())
			http.Error(w, "invalid list prefix", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") == "" || r.Header.Get("X-Amz-Date") == "" {
			t.Errorf("ListObjectVersions request must be SigV4 authenticated")
			http.Error(w, "missing signature", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<ListVersionsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><IsTruncated>false</IsTruncated></ListVersionsResult>`))
	}))
	defer httpServer.Close()
	store := s3Store{name:"backblaze_b2_identity_evidence", endpoint:httpServer.URL, region:"us-west-004", bucket:"identity-evidence-private", access:"test-access", secret:strings.Repeat("s",40)}
	s := &server{cfg:config{identityPrimary:store},http:httpServer.Client()}
	if err := s.probeIdentityEvidenceStore(context.Background(), store); err != nil { t.Fatalf("read-only B2 readiness failed: %v",err) }
	if len(methods)!=1 || methods[0]!=http.MethodGet || len(paths)!=1 || paths[0]!="/identity-evidence-private/" {
		t.Fatalf("readiness performed unexpected provider operations: methods=%v paths=%v",methods,paths)
	}
	if len(queryValues)!=1 || queryValues[0].Get("max-keys")!="1000" {
		t.Fatalf("readiness did not use bounded version listing: %v",queryValues)
	}
}


func TestIdentityEvidenceB2StoreRequiresSeparateBucketAndApplicationKey(t *testing.T) {
	media := s3Store{name:"backblaze_b2",endpoint:"https://s3.us-west-004.backblazeb2.com",region:"us-west-004",bucket:"ordinary-media-staging",access:"media-key-id",secret:"media-secret"}
	identity := s3Store{name:"backblaze_b2_identity_evidence",endpoint:media.endpoint,region:media.region,bucket:"identity-evidence-staging",access:"identity-key-id",secret:"identity-secret"}
	if err := validateIdentityStoreIsolation(media,identity); err != nil { t.Fatalf("separate identity store rejected: %v",err) }

	cases := []struct{name string; mutate func(*s3Store)}{
		{"shared bucket",func(s *s3Store){s.bucket=media.bucket}},
		{"shared access key",func(s *s3Store){s.access=media.access}},
		{"shared secret",func(s *s3Store){s.secret=media.secret}},
		{"different endpoint",func(s *s3Store){s.endpoint="https://other.example.invalid"}},
		{"different region",func(s *s3Store){s.region="us-east-1"}},
	}
	for _,tc:=range cases {
		t.Run(tc.name,func(t *testing.T){
			candidate:=identity
			tc.mutate(&candidate)
			if err:=validateIdentityStoreIsolation(media,candidate);err==nil{t.Fatalf("%s was accepted",tc.name)}
		})
	}
}


func TestDeleteAllB2ObjectVersionsPermanentlyRemovesVersionsAndDeleteMarkers(t *testing.T) {
	const key = "identity/owner/evidence.jpg"
	remaining := map[string]bool{"v3":true,"d2":true,"v1":true,"neighbor":true}
	deleted := make([]string,0,3)
	plainDeletes := 0
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		if r.Header.Get("Authorization") == "" { t.Error("B2 request omitted SigV4 authorization") }
		if r.Method == http.MethodGet {
			if r.URL.Path != "/identity-bucket/" { t.Errorf("version list path=%q",r.URL.Path) }
			if _,ok:=r.URL.Query()["versions"];!ok { t.Error("version list omitted versions subresource") }
			if got:=r.URL.Query().Get("prefix");got!=key { t.Errorf("version list prefix=%q, want exact evidence key",got) }
			if got:=r.URL.Query().Get("max-keys");got!="1000" { t.Errorf("version list max-keys=%q",got) }
			w.Header().Set("Content-Type","application/xml")
			if r.URL.Query().Get("key-marker")=="" {
				if remaining["v3"]||remaining["d2"]||remaining["v1"] {
					_,_=fmt.Fprintf(w,`<ListVersionsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Version><Key>%s</Key><VersionId>v3</VersionId></Version><DeleteMarker><Key>%s</Key><VersionId>d2</VersionId></DeleteMarker><Version><Key>%s-neighbor</Key><VersionId>neighbor</VersionId></Version><IsTruncated>true</IsTruncated><NextKeyMarker>%s</NextKeyMarker><NextVersionIdMarker>d2</NextVersionIdMarker></ListVersionsResult>`,key,key,key,key)
				} else {
					_,_=fmt.Fprintf(w,`<ListVersionsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Version><Key>%s-neighbor</Key><VersionId>neighbor</VersionId></Version><IsTruncated>false</IsTruncated></ListVersionsResult>`,key)
				}
				return
			}
			if r.URL.Query().Get("key-marker")!=key || r.URL.Query().Get("version-id-marker")!="d2" { t.Errorf("unexpected pagination markers: %v",r.URL.Query()) }
			if remaining["v1"] {
				_,_=fmt.Fprintf(w,`<ListVersionsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Version><Key>%s</Key><VersionId>v1</VersionId></Version><Version><Key>%s-neighbor</Key><VersionId>neighbor</VersionId></Version><IsTruncated>false</IsTruncated></ListVersionsResult>`,key,key)
			} else {
				_,_=fmt.Fprintf(w,`<ListVersionsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Version><Key>%s-neighbor</Key><VersionId>neighbor</VersionId></Version><IsTruncated>false</IsTruncated></ListVersionsResult>`,key)
			}
			return
		}
		if r.Method==http.MethodDelete {
			if r.URL.Path!="/identity-bucket/"+key { t.Errorf("version delete path=%q",r.URL.Path) }
			versionID:=r.URL.Query().Get("versionId")
			if versionID=="" { plainDeletes++;http.Error(w,"versionId is required for permanent deletion",http.StatusBadRequest);return }
			if !remaining[versionID] { http.Error(w,"version not found",http.StatusNotFound);return }
			delete(remaining,versionID);deleted=append(deleted,versionID);w.WriteHeader(http.StatusNoContent);return
		}
		http.Error(w,"unexpected method",http.StatusMethodNotAllowed)
	}))
	defer httpServer.Close()
	store:=s3Store{name:"backblaze_b2_identity_evidence",endpoint:httpServer.URL,region:"us-east-005",bucket:"identity-bucket",access:"identity-key",secret:"identity-secret"}
	s:=&server{http:httpServer.Client()}
	if err:=s.deleteAllB2ObjectVersions(context.Background(),store,key);err!=nil { t.Fatalf("permanent B2 version deletion: %v",err) }
	if len(deleted)!=3 { t.Fatalf("deleted version IDs=%v, want all three data/delete-marker versions",deleted) }
	if remaining["v3"]||remaining["d2"]||remaining["v1"] { t.Fatalf("identity evidence versions remain: %+v",remaining) }
	if !remaining["neighbor"] { t.Fatal("version deletion crossed the exact object-key boundary") }
	if plainDeletes!=0 { t.Fatalf("permanent deletion issued %d unversioned DELETE requests",plainDeletes) }
	if deleted[0]!="v3"||deleted[1]!="d2"||deleted[2]!="v1" { t.Fatalf("unexpected version deletion order: %v",deleted) }
}


func TestEnqueueLegacyIdentityEvidencePurgesPostgresIntegration(t *testing.T) {
	if strings.TrimSpace(os.Getenv("MEDIA_PURGE_INTEGRATION")) != "true" {
		t.Skip("set MEDIA_PURGE_INTEGRATION=true to run against an explicitly provisioned disposable media database")
	}
	databaseURL := strings.TrimSpace(os.Getenv("MEDIA_DATABASE_URL"))
	if databaseURL == "" { t.Fatal("MEDIA_PURGE_INTEGRATION=true requires MEDIA_DATABASE_URL") }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil { t.Fatalf("open media integration database: %v", err) }
	defer db.Close()
	if err := db.Ping(ctx); err != nil { t.Fatalf("media integration database unavailable: %v", err) }

	id, err := randomID()
	if err != nil { t.Fatal(err) }
	owner := id[:8] + "-" + id[8:12] + "-4" + id[13:16] + "-8" + id[17:20] + "-" + id[20:]
	identityKey := "identity/" + owner + "/10000000-0000-4000-8000-000000000001/20000000-0000-4000-8000-000000000002.jpg"
	ordinaryOwner := "30000000-0000-4000-8000-000000000003"
	ordinaryKey := "users/" + ordinaryOwner + "/legacy-media.jpg"
	defer func() {
		_, _ = db.Exec(context.Background(), `delete from media_outbox where owner_id=$1 and object_key=$2`, owner, identityKey)
		_, _ = db.Exec(context.Background(), `delete from media_objects where owner_id=$1 and object_key=$2`, owner, identityKey)
		_, _ = db.Exec(context.Background(), `delete from media_outbox where owner_id=$1 and object_key=$2`, ordinaryOwner, ordinaryKey)
		_, _ = db.Exec(context.Background(), `delete from media_objects where owner_id=$1 and object_key=$2`, ordinaryOwner, ordinaryKey)
	}()
	if _, err := db.Exec(ctx, `insert into media_objects(owner_id,namespace,object_key,content_type,size_bytes,status,identity_evidence_purged_at) values($1,'identity-evidence-private',$2,'image/jpeg',10,'deleted',null)`, owner, identityKey); err != nil { t.Fatalf("insert legacy deleted identity evidence: %v", err) }
	if _, err := db.Exec(ctx, `insert into media_outbox(owner_id,namespace,object_key,operation,status,attempts,available_at) values($1,'identity-evidence-private',$2,'delete','done',9,now()-interval '1 minute')`, owner, identityKey); err != nil { t.Fatalf("insert prior delete-marker-only outbox row: %v", err) }
	if _, err := db.Exec(ctx, `insert into media_objects(owner_id,namespace,object_key,content_type,size_bytes,status) values($1,'media-public',$2,'image/jpeg',10,'deleted')`, ordinaryOwner, ordinaryKey); err != nil { t.Fatalf("insert ordinary deleted media: %v", err) }
	if _, err := db.Exec(ctx, `insert into media_outbox(owner_id,namespace,object_key,operation,status,attempts) values($1,'media-public',$2,'delete','done',3)`, ordinaryOwner, ordinaryKey); err != nil { t.Fatalf("insert ordinary media outbox row: %v", err) }

	// A missing database row must not be treated as proof that the B2 key
	// and metadata sidecar are absent. Exercise the authenticated HTTP delete
	// route and verify that it creates a durable permanent-purge tombstone.
	orphanID, err := randomID()
	if err != nil { t.Fatal(err) }
	orphanOwner := orphanID[:8] + "-" + orphanID[8:12] + "-4" + orphanID[13:16] + "-8" + orphanID[17:20] + "-" + orphanID[20:]
	orphanKey := "identity/" + orphanOwner + "/40000000-0000-4000-8000-000000000004/50000000-0000-4000-8000-000000000005.jpg-" + strings.Repeat("a", 43)
	defer func() {
		_, _ = db.Exec(context.Background(), `delete from media_operations where owner_id=$1 and object_key=$2`, orphanOwner, orphanKey)
		_, _ = db.Exec(context.Background(), `delete from media_outbox where owner_id=$1 and object_key=$2`, orphanOwner, orphanKey)
		_, _ = db.Exec(context.Background(), `delete from media_objects where owner_id=$1 and object_key=$2`, orphanOwner, orphanKey)
	}()
	identityToken := strings.Repeat("i", 40)
	orphanServer := &server{db:db,cfg:config{identityToken:identityToken}}
	body := fmt.Sprintf(`{"requester_id":%q,"owner_id":%q,"namespace":"identity-evidence-private","key":%q}`,orphanOwner,orphanOwner,orphanKey)
	request := httptest.NewRequest(http.MethodPost,"/internal/v1/identity-evidence/delete",strings.NewReader(body))
	request.Header.Set("Authorization","Bearer "+identityToken)
	request.Header.Set("X-Testagram-API-Version",apiVersion)
	recorder := httptest.NewRecorder()
	orphanServer.delete(recorder,request)
	if recorder.Code!=http.StatusAccepted { t.Fatalf("orphaned identity evidence delete returned %d: %s",recorder.Code,recorder.Body.String()) }
	var orphanMediaStatus,orphanOutboxStatus string
	if err := db.QueryRow(ctx,`select status from media_objects where owner_id=$1 and namespace='identity-evidence-private' and object_key=$2`,orphanOwner,orphanKey).Scan(&orphanMediaStatus); err != nil { t.Fatalf("orphan purge tombstone missing: %v",err) }
	if err := db.QueryRow(ctx,`select status from media_outbox where owner_id=$1 and namespace='identity-evidence-private' and object_key=$2 and operation='delete'`,orphanOwner,orphanKey).Scan(&orphanOutboxStatus); err != nil { t.Fatalf("orphan purge outbox missing: %v",err) }
	if orphanMediaStatus!="delete_pending"||orphanOutboxStatus!="pending" { t.Fatalf("orphan evidence was not durably queued: media=%q outbox=%q",orphanMediaStatus,orphanOutboxStatus) }
	var orphanAuditCount int
	if err := db.QueryRow(ctx,`select count(*) from media_operations where owner_id=$1 and namespace='identity-evidence-private' and object_key=$2 and operation='delete' and outcome='queued'`,orphanOwner,orphanKey).Scan(&orphanAuditCount); err != nil { t.Fatal(err) }
	if orphanAuditCount!=1 { t.Fatalf("orphan evidence purge queued without exactly one audit event: %d",orphanAuditCount) }

	s := &server{db:db}
	if err := s.enqueueLegacyIdentityEvidencePurges(ctx); err != nil { t.Fatalf("enqueue legacy identity purge: %v", err) }
	var mediaStatus, outboxStatus string
	var attempts int
	if err := db.QueryRow(ctx, `select status from media_objects where owner_id=$1 and object_key=$2`, owner, identityKey).Scan(&mediaStatus); err != nil { t.Fatal(err) }
	if err := db.QueryRow(ctx, `select status,attempts from media_outbox where owner_id=$1 and object_key=$2 and operation='delete'`, owner, identityKey).Scan(&outboxStatus,&attempts); err != nil { t.Fatal(err) }
	if mediaStatus!="delete_pending" || outboxStatus!="pending" || attempts!=0 { t.Fatalf("legacy identity purge not requeued: media=%q outbox=%q attempts=%d",mediaStatus,outboxStatus,attempts) }

	if _, err := db.Exec(ctx, `update media_outbox set status='retry',attempts=7,available_at=now()+interval '10 minutes' where owner_id=$1 and object_key=$2 and operation='delete'`, owner, identityKey); err != nil { t.Fatal(err) }
	if err := s.enqueueLegacyIdentityEvidencePurges(ctx); err != nil { t.Fatalf("preserve scheduled retry: %v", err) }
	var retryAvailable time.Time
	if err := db.QueryRow(ctx, `select status,attempts,available_at from media_outbox where owner_id=$1 and object_key=$2 and operation='delete'`, owner, identityKey).Scan(&outboxStatus,&attempts,&retryAvailable); err != nil { t.Fatal(err) }
	if outboxStatus!="retry" || attempts!=7 || !retryAvailable.After(time.Now().Add(5*time.Minute)) { t.Fatalf("legacy sweep reset an active retry schedule: status=%q attempts=%d available=%s",outboxStatus,attempts,retryAvailable) }

	if _, err := db.Exec(ctx, `update media_outbox set status='done' where owner_id=$1 and object_key=$2 and operation='delete'`, owner, identityKey); err != nil { t.Fatal(err) }
	if err := s.enqueueLegacyIdentityEvidencePurges(ctx); err != nil { t.Fatalf("requeue stale completed purge: %v", err) }
	if err := db.QueryRow(ctx, `select status,attempts from media_outbox where owner_id=$1 and object_key=$2 and operation='delete'`, owner, identityKey).Scan(&outboxStatus,&attempts); err != nil { t.Fatal(err) }
	if outboxStatus!="pending" || attempts!=0 { t.Fatalf("stale completed identity purge was not requeued: status=%q attempts=%d",outboxStatus,attempts) }

	if _, err := db.Exec(ctx, `update media_outbox set status='dead',attempts=12 where owner_id=$1 and object_key=$2 and operation='delete'`, owner, identityKey); err != nil { t.Fatal(err) }
	if err := s.enqueueLegacyIdentityEvidencePurges(ctx); err != nil { t.Fatalf("preserve permanent-purge dead letter: %v", err) }
	if err := db.QueryRow(ctx, `select status from media_outbox where owner_id=$1 and object_key=$2 and operation='delete'`, owner, identityKey).Scan(&outboxStatus); err != nil { t.Fatal(err) }
	if outboxStatus!="dead" { t.Fatalf("legacy sweep silently retried a permanent-purge dead letter: %q",outboxStatus) }
	if _, err := db.Exec(ctx, `update media_objects set status='deleted' where owner_id=$1 and object_key=$2`, owner, identityKey); err != nil { t.Fatal(err) }
	if err := s.enqueueLegacyIdentityEvidencePurges(ctx); err != nil { t.Fatalf("requeue pre-existing legacy deleted record: %v", err) }
	if err := db.QueryRow(ctx, `select status from media_outbox where owner_id=$1 and object_key=$2 and operation='delete'`, owner, identityKey).Scan(&outboxStatus); err != nil { t.Fatal(err) }
	if outboxStatus!="pending" { t.Fatalf("legacy deleted row with a prior dead job was not requeued once: %q",outboxStatus) }

	if err := db.QueryRow(ctx, `select status from media_objects where owner_id=$1 and object_key=$2`, ordinaryOwner, ordinaryKey).Scan(&mediaStatus); err != nil { t.Fatal(err) }
	if mediaStatus!="deleted" { t.Fatalf("legacy sweep modified ordinary media status: %q",mediaStatus) }
	if err := db.QueryRow(ctx, `select status from media_outbox where owner_id=$1 and object_key=$2 and operation='delete'`, ordinaryOwner, ordinaryKey).Scan(&outboxStatus); err != nil { t.Fatal(err) }
	if outboxStatus!="done" { t.Fatalf("legacy sweep modified ordinary media outbox: %q",outboxStatus) }

	if _, err := db.Exec(ctx, `update media_outbox set status='done' where owner_id=$1 and object_key=$2 and operation='delete'`, owner, identityKey); err != nil { t.Fatal(err) }
	if _, err := db.Exec(ctx, `update media_objects set status='deleted',identity_evidence_purged_at=now() where owner_id=$1 and object_key=$2`, owner, identityKey); err != nil { t.Fatal(err) }
	if err := s.enqueueLegacyIdentityEvidencePurges(ctx); err != nil { t.Fatalf("skip already-purged identity evidence: %v", err) }
	if err := db.QueryRow(ctx, `select status from media_outbox where owner_id=$1 and object_key=$2 and operation='delete'`, owner, identityKey).Scan(&outboxStatus); err != nil { t.Fatal(err) }
	if outboxStatus!="done" { t.Fatalf("verified permanent purge was incorrectly requeued: %q",outboxStatus) }
}

func TestDisabledIdentityEvidenceRouteFailsClosed(t *testing.T) {
	s := &server{cfg: config{token: strings.Repeat("m", 40), identityEnabled: false}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/internal/v1/identity-evidence/upload-url", nil)
	if s.namespaceAllowedForRoute(w, r, "identity-evidence-private") {
		t.Fatal("disabled identity-evidence route was accepted")
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled identity-evidence route status=%d, want 404", w.Code)
	}
}

func TestDisabledIdentityEvidenceStoresAreNotSelected(t *testing.T) {
	s := &server{cfg: config{
		identityEnabled: false,
		primary: s3Store{name: "ordinary-media", bucket: "media"},
		replica: s3Store{name: "ordinary-replica", bucket: "replica"},
		identityPrimary: s3Store{name: "identity", bucket: "identity"},
	}}
	primary, replica := s.storesForNamespace("identity-evidence-private")
	if primary.bucket != "" || replica.bucket != "" {
		t.Fatalf("disabled identity namespace selected storage: primary=%q replica=%q", primary.bucket, replica.bucket)
	}
}
