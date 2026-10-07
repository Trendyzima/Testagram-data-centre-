package main
import("net/http/httptest";"strings";"testing")
func TestHealth(t *testing.T){s:=NewStore();r:=httptest.NewRequest("GET","/healthz",nil);w:=httptest.NewRecorder();s.health(w,r);if w.Code!=200||!strings.Contains(w.Body.String(),""ok":true"){t.Fatalf("unexpected response: %d %s",w.Code,w.Body.String())}}
