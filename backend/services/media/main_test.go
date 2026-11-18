package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
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
	s := &server{}
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

func TestIdentityEvidenceUsesDedicatedB2AndR2Buckets(t *testing.T) {
 s := &server{cfg: config{
  primary:s3Store{name:"b2-media",bucket:"media-b2"},
  replica:s3Store{name:"r2-media",bucket:"media-r2"},
  identityPrimary:s3Store{name:"b2-identity",bucket:"identity-b2"},
  identityReplica:s3Store{name:"r2-identity",bucket:"identity-r2"},
  metadataPrimary:s3Store{name:"vps-metadata",bucket:"metadata-vps"},
  metadataReplica:s3Store{name:"r2-metadata",bucket:"metadata-r2"},
 }}
 p,r := s.storesForNamespace("identity-evidence-private")
 if p.bucket!="identity-b2" || r.bucket!="identity-r2" { t.Fatalf("identity evidence routed to %q/%q",p.bucket,r.bucket) }
 p,r = s.storesForNamespace("media-private")
 if p.bucket!="media-b2" || r.bucket!="media-r2" { t.Fatalf("media routed to %q/%q",p.bucket,r.bucket) }
 mp,mr := s.metadataStoresForNamespace("identity-evidence-private")
 if mp.bucket!="identity-b2" || mr.bucket!="identity-r2" { t.Fatalf("identity sidecars not isolated: %q/%q",mp.bucket,mr.bucket) }
 mp,mr = s.metadataStoresForNamespace("media-private")
 if mp.bucket!="metadata-vps" || mr.bucket!="metadata-r2" { t.Fatalf("media metadata routing changed: %q/%q",mp.bucket,mr.bucket) }
}
