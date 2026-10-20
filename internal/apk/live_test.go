package apk

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"testing"
)

// TestLiveWolfi checks the verifier against the real Wolfi repository.
// It needs network access, so it only runs with APK_LIVE_TEST=1.
func TestLiveWolfi(t *testing.T) {
	if os.Getenv("APK_LIVE_TEST") != "1" {
		t.Skip("set APK_LIVE_TEST=1 to run against packages.wolfi.dev")
	}
	key, err := os.ReadFile("../keys/wolfi-signing.rsa.pub")
	if err != nil {
		t.Fatal(err)
	}
	keys := Keyring{}
	if err := keys.Add("wolfi-signing.rsa.pub", key); err != nil {
		t.Fatal(err)
	}

	get := func(url string) []byte {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	base := "https://packages.wolfi.dev/os/x86_64/"
	pkgs, err := ReadSignedIndex(bytes.NewReader(get(base+"APKINDEX.tar.gz")), keys)
	if err != nil {
		t.Fatal(err)
	}
	idx := NewIndex(pkgs)
	p, ok := idx.Latest("libuv")
	if !ok {
		t.Fatal("libuv not found")
	}
	raw := get(base + p.Filename())
	if err := Verify(bytes.NewReader(raw), p, keys); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Extract(bytes.NewReader(raw), dir, func(n string) (string, bool) { return n, true }); err != nil {
		t.Fatal(err)
	}
	t.Logf("verified and extracted %s", p)

	tampered := bytes.Clone(raw)
	tampered[len(tampered)-100] ^= 0xff
	if err := Verify(bytes.NewReader(tampered), p, keys); err == nil {
		t.Fatal("tampered package verified")
	} else {
		t.Logf("tampered package rejected: %v", err)
	}

	node, _ := idx.Latest("nodejs-24")
	closure, err := idx.Closure([]Package{node}, BaseSystem)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range closure {
		t.Logf("closure: %s", c)
	}
}
