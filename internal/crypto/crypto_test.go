package crypto

import("bytes";"testing")
func TestRoundTrip(t *testing.T){k:=bytes.Repeat([]byte{9},32);p:=[]byte("testagram");c,e:=Encrypt(k,p);if e!=nil{t.Fatal(e)};got,e:=Decrypt(k,c);if e!=nil{t.Fatal(e)};if !bytes.Equal(got,p){t.Fatalf("got %q",got)}}
