package apk

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/fetch"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"24.11.1-r0", "24.11.1-r0", 0},
		{"24.9.0-r0", "24.10.0-r0", -1}, // numeric, not lexical (the old Python bug)
		{"24.11.1-r2", "24.11.1-r10", -1},
		{"1.2", "1.2.0", -1},
		{"1.2.3a", "1.2.3", 1},
		{"1.0_rc1", "1.0", -1},
		{"1.0_rc2", "1.0_rc10", -1},
		{"1.0_p1", "1.0", 1},
		{"1.0_alpha", "1.0_beta", -1},
		{"1.3.2.1_rc20260921-r0", "1.3.2-r5", 1},
		{"garbage", "1.0", -1},
	}
	for _, tt := range tests {
		if got := CompareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
		if got := CompareVersions(tt.b, tt.a); got != -tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.b, tt.a, got, -tt.want)
		}
	}
}

func TestParseIndex(t *testing.T) {
	text := "C:Q1abc=\nP:nodejs-24\nV:24.1.0-r0\nA:x86_64\nS:10\nI:20\nT:Node\nL:MIT\no:nodejs-24\nD:so:libc.so.6 so:libuv.so.1\np:cmd:node=24.1.0-r0\n\n" +
		"P:libuv\nV:1.0-r0\np:so:libuv.so.1=1\n"
	pkgs, err := ParseIndex(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("got %d packages, want 2", len(pkgs))
	}
	p := pkgs[0]
	if p.Name != "nodejs-24" || p.Version != "24.1.0-r0" || p.Size != 10 || p.InstalledSize != 20 ||
		p.License != "MIT" || p.Origin != "nodejs-24" || len(p.Depends) != 2 || p.Provides[0] != "cmd:node=24.1.0-r0" {
		t.Errorf("unexpected package: %+v", p)
	}
	if p.Filename() != "nodejs-24-24.1.0-r0.apk" {
		t.Errorf("Filename() = %s", p.Filename())
	}
}

func TestProviderPrefersNewestPackage(t *testing.T) {
	idx := NewIndex([]Package{
		{Name: "libuv", Version: "1.51.0-r2", Provides: []string{"so:libuv.so.1=1"}},
		{Name: "libuv", Version: "1.53.0-r0", Provides: []string{"so:libuv.so.1=1"}},
		{Name: "libuv-old", Version: "9.0-r0", Provides: []string{"so:libuv.so.1=1"}},
	})
	p, ok := idx.Provider("so:libuv.so.1")
	if !ok {
		t.Fatal("no provider")
	}
	// Same soname version for all: the newest package wins, whatever its name.
	if p.String() != "libuv-old-9.0-r0" {
		t.Errorf("Provider = %s", p)
	}
	if p, _ := idx.Provider("libuv>=1.0"); p.Version != "1.53.0-r0" {
		t.Errorf("exact-name provider = %s, want libuv-1.53.0-r0", p)
	}
}

func TestClosure(t *testing.T) {
	idx := NewIndex([]Package{
		{Name: "node", Version: "1-r0", Depends: []string{"so:libc.so.6", "so:libicu.so.78", "cmd:python3", "!conflict"}},
		{Name: "glibc-2.44", Version: "2.44-r0", Origin: "glibc-2.44", Provides: []string{"so:libc.so.6=6"}},
		{Name: "libicu", Version: "78-r0", Depends: []string{"icu-data", "so:libc.so.6"}, Provides: []string{"so:libicu.so.78=78"}},
		{Name: "icu-data", Version: "78-r0", Depends: []string{"wolfi-baselayout"}},
		{Name: "wolfi-baselayout", Version: "1-r0"},
		{Name: "python3", Version: "3-r0", Provides: []string{"cmd:python3=3"}},
	})
	root, _ := idx.Latest("node")
	got, err := idx.Closure([]Package{root}, BaseSystem)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	if want := "node icu-data libicu"; strings.Join(names, " ") != want {
		t.Errorf("Closure = %v, want %s", names, want)
	}

	broken := NewIndex([]Package{{Name: "a", Version: "1", Depends: []string{"so:missing.so"}}})
	a, _ := broken.Latest("a")
	if _, err := broken.Closure([]Package{a}, BaseSystem); err == nil {
		t.Error("expected an error for an unresolvable dependency")
	}
}

func TestReadSignedIndex(t *testing.T) {
	_, pkg := buildPackage(t, "hello", "1.0-r0", entry{name: "usr/bin/hello", body: "hi"})
	raw := buildIndex(t, pkg)

	pkgs, err := ReadSignedIndex(bytes.NewReader(raw), testKeyring(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].Checksum != pkg.Checksum {
		t.Fatalf("unexpected packages: %+v", pkgs)
	}

	t.Run("untrusted key", func(t *testing.T) {
		other, _ := rsa.GenerateKey(rand.Reader, 2048)
		k := Keyring{}
		k.Add(testKeyName, publicPEM(t, other))
		if _, err := ReadSignedIndex(bytes.NewReader(raw), k); err == nil {
			t.Error("index verified with the wrong key")
		}
		if _, err := ReadSignedIndex(bytes.NewReader(raw), Keyring{}); err == nil {
			t.Error("index verified with an empty keyring")
		}
	})

	t.Run("tampered", func(t *testing.T) {
		tampered := bytes.Clone(raw)
		tampered[len(tampered)-12] ^= 0x01
		if _, err := ReadSignedIndex(bytes.NewReader(tampered), testKeyring(t)); err == nil {
			t.Error("tampered index verified")
		}
	})

	t.Run("unsigned", func(t *testing.T) {
		unsigned := member(t, entry{name: "APKINDEX", body: "P:x\nV:1\n"})
		if _, err := ReadSignedIndex(bytes.NewReader(unsigned), testKeyring(t)); err == nil {
			t.Error("unsigned index accepted")
		}
	})

	t.Run("sha1 signature", func(t *testing.T) {
		index := member(t, entry{name: "APKINDEX", body: "P:x\nV:1\n"})
		sig := member(t, entry{name: ".SIGN.RSA." + testKeyName, body: "whatever"})
		if _, err := ReadSignedIndex(bytes.NewReader(append(sig, index...)), testKeyring(t)); err == nil ||
			!strings.Contains(err.Error(), "SHA-1") {
			t.Errorf("want SHA-1 rejection, got %v", err)
		}
	})
}

func TestVerify(t *testing.T) {
	raw, pkg := buildPackage(t, "hello", "1.0-r0", entry{name: "usr/bin/hello", body: "hi", mode: 0o755})
	keys := testKeyring(t)

	if err := Verify(bytes.NewReader(raw), pkg, keys); err != nil {
		t.Fatalf("valid package rejected: %v", err)
	}

	t.Run("tampered data", func(t *testing.T) {
		tampered := bytes.Clone(raw)
		tampered[len(tampered)-12] ^= 0x01
		if err := Verify(bytes.NewReader(tampered), pkg, keys); err == nil {
			t.Error("tampered package verified")
		}
	})

	t.Run("checksum from another package", func(t *testing.T) {
		_, other := buildPackage(t, "hello", "1.0-r0", entry{name: "usr/bin/hello", body: "evil"})
		if err := Verify(bytes.NewReader(raw), other, keys); err == nil {
			t.Error("package verified against the wrong index entry")
		}
	})

	t.Run("name mismatch", func(t *testing.T) {
		renamed := pkg
		renamed.Name = "other"
		if err := Verify(bytes.NewReader(raw), renamed, keys); err == nil {
			t.Error("package verified under another name")
		}
	})

	t.Run("unknown package key is fine", func(t *testing.T) {
		// The signed index vouches for the control segment.
		if err := Verify(bytes.NewReader(raw), pkg, Keyring{}); err != nil {
			t.Errorf("rejected: %v", err)
		}
	})

	t.Run("bad package signature", func(t *testing.T) {
		other, _ := rsa.GenerateKey(rand.Reader, 2048)
		k := Keyring{}
		k.Add(testKeyName, publicPEM(t, other))
		if err := Verify(bytes.NewReader(raw), pkg, k); err == nil {
			t.Error("package with a bad signature verified")
		}
	})
}

func TestExtractUsrMerge(t *testing.T) {
	raw, _ := buildPackage(t, "hello", "1.0-r0",
		entry{name: "usr/", typeflag: tar.TypeDir, mode: 0o755},
		entry{name: "usr/bin/hello", body: "#!/bin/sh\n", mode: 0o755},
		entry{name: "usr/lib/libhello.so.1.2", body: "elf"},
		entry{name: "usr/lib/libhello.so.1", typeflag: tar.TypeSymlink, linkname: "libhello.so.1.2"},
		entry{name: "usr/bin/hello-abs", typeflag: tar.TypeSymlink, linkname: "/usr/bin/hello"},
		entry{name: "etc/hello.conf", body: "x"},
	)
	dest := t.TempDir()
	if err := Extract(bytes.NewReader(raw), dest, UsrMerge); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dest, "bin", "hello"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("bin/hello: %v %v", info, err)
	}
	for link, want := range map[string]string{
		"lib/libhello.so.1": "libhello.so.1.2",
		"bin/hello-abs":     "hello", // absolute target rewritten inside the layer
	} {
		if got, err := os.Readlink(filepath.Join(dest, link)); err != nil || got != want {
			t.Errorf("%s -> %q (%v), want %q", link, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "etc", "hello.conf")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "usr")); !os.IsNotExist(err) {
		t.Error("usr/ should not exist in the layer")
	}
}

func TestRepository(t *testing.T) {
	apkRaw, pkg := buildPackage(t, "hello", "1.0-r0", entry{name: "usr/bin/hello", body: "hi"})
	pkg.Depends = []string{"so:libc.so.6"}
	index := buildIndex(t, pkg)

	var indexRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/os/x86_64/APKINDEX.tar.gz":
			indexRequests++
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"v1"`)
			w.Write(index)
		case "/os/x86_64/hello-1.0-r0.apk":
			w.Write(apkRaw)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := fetch.New("test")
	client.CacheDir = t.TempDir()
	repo := Repository{URL: srv.URL + "/os", Keys: testKeyring(t), Client: client}

	for range 2 {
		idx, stale, err := repo.Index(t.Context(), "x86_64")
		if err != nil || stale {
			t.Fatalf("Index: %v (stale %t)", err, stale)
		}
		if _, ok := idx.Latest("hello"); !ok {
			t.Fatal("hello not in index")
		}
	}
	if indexRequests != 2 {
		t.Errorf("index requests = %d, want 2 (second one revalidated)", indexRequests)
	}

	idx, _, _ := repo.Index(t.Context(), "x86_64")
	p, _ := idx.Latest("hello")
	dest := t.TempDir()
	if err := repo.Install(t.Context(), "x86_64", []Package{p}, dest, UsrMerge); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "bin", "hello")); err != nil || string(b) != "hi" {
		t.Errorf("bin/hello = %q, %v", b, err)
	}

	// A package that does not match its index entry must leave dest untouched.
	bad := p
	bad.Checksum = "Q1" + strings.Repeat("A", 27) + "="
	empty := t.TempDir()
	if err := repo.Install(t.Context(), "x86_64", []Package{bad}, empty, UsrMerge); err == nil {
		t.Error("install of a mismatched package succeeded")
	}
	if entries, _ := os.ReadDir(empty); len(entries) != 0 {
		t.Errorf("destination was written to: %v", entries)
	}
}

func TestArch(t *testing.T) {
	for in, want := range map[string]string{"amd64": "x86_64", "x86_64": "x86_64", "arm64": "aarch64", "aarch64": "aarch64"} {
		if got, err := Arch(in); err != nil || got != want {
			t.Errorf("Arch(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := Arch("s390x"); err == nil {
		t.Error("expected an error for s390x")
	}
}
