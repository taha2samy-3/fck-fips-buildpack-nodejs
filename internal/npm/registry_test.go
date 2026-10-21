package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/fetch"
)

func packument(versions ...string) Packument {
	p := Packument{Name: "pnpm", DistTags: map[string]string{"latest": "10.2.0", "next-11": "11.0.0-rc.1"}, Versions: map[string]Manifest{}}
	for _, v := range versions {
		p.Versions[v] = Manifest{Name: "pnpm", Version: v}
	}
	return p
}

func TestResolve(t *testing.T) {
	p := packument("9.15.9", "9.1.4", "10.1.0", "10.2.0", "11.0.0-rc.1", "7.33.6")
	tests := []struct{ spec, want string }{
		{"9.15.9", "9.15.9"},
		{"v9.15.9", "9.15.9"},
		{"9", "9.15.9"},
		{"9.x", "9.15.9"},
		{"9.1", "9.1.4"},
		{"~9.1.0", "9.1.4"},
		{"^9.1.0", "9.15.9"},
		{">=9.0.0 <11", "10.2.0"},
		{"latest", "10.2.0"},
		{"", "10.2.0"},
		{"next-11", "11.0.0-rc.1"},
		{"11", ""}, // only a pre-release exists: a range does not pick it
		{"12", ""},
		{"not a version", ""},
	}
	for _, tt := range tests {
		m, err := p.Resolve(tt.spec)
		if tt.want == "" {
			if err == nil {
				t.Errorf("Resolve(%q) = %s, want error", tt.spec, m.Version)
			}
			continue
		}
		if err != nil || m.Version != tt.want {
			t.Errorf("Resolve(%q) = %s, %v; want %s", tt.spec, m.Version, err, tt.want)
		}
	}
}

func TestParseIntegrity(t *testing.T) {
	sum := sha512.Sum512([]byte("x"))
	b64 := base64.StdEncoding.EncodeToString(sum[:])
	d, err := ParseIntegrity("sha1-abc sha512-" + b64)
	if err != nil || d.Algorithm != "sha512" || !bytes.Equal(d.Sum, sum[:]) {
		t.Errorf("ParseIntegrity = %+v, %v", d, err)
	}
	if _, err := ParseIntegrity("sha1-qUqP5cyxm6YcTAhz05Hph5gvu9M="); err == nil {
		t.Error("sha1-only integrity accepted")
	}
}

func TestParseCorepackHash(t *testing.T) {
	d, err := ParseCorepackHash("sha512.abcd")
	if err != nil || d.Algorithm != "sha512" || hex.EncodeToString(d.Sum) != "abcd" {
		t.Errorf("got %+v, %v", d, err)
	}
	for _, bad := range []string{"sha512", "md5.abcd", "sha512.zz"} {
		if _, err := ParseCorepackHash(bad); err == nil {
			t.Errorf("ParseCorepackHash(%q) succeeded", bad)
		}
	}
}

func TestManifestHelpers(t *testing.T) {
	var m Manifest
	json.Unmarshal([]byte(`{"name":"@scope/tool","bin":"cli.js","engines":{"node":">=18.12"}}`), &m)
	if bins, _ := m.Bins(); bins["tool"] != "cli.js" {
		t.Errorf("Bins = %v", bins)
	}
	if m.Engine("node") != ">=18.12" {
		t.Errorf("Engine = %q", m.Engine("node"))
	}
	json.Unmarshal([]byte(`{"name":"pnpm","bin":{"pnpm":"bin/pnpm.cjs","pnpx":"bin/pnpx.cjs"},"engines":["node >= 0.8"]}`), &m)
	if bins, _ := m.Bins(); len(bins) != 2 {
		t.Errorf("Bins = %v", bins)
	}
	if m.Engine("node") != "" {
		t.Error("malformed engines should read as absent")
	}
}

func tarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"package/package.json": `{"name":"pnpm"}`, "package/bin/pnpm.cjs": "#!/usr/bin/env node\n"} {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestInstall(t *testing.T) {
	tgz := tarball(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pnpm":
			if !strings.Contains(r.Header.Get("Accept"), "application/vnd.npm.install-v1+json") {
				t.Error("abbreviated metadata not requested")
			}
			w.Write([]byte(`{"name":"pnpm","dist-tags":{"latest":"10.0.0"},"versions":{"10.0.0":{"name":"pnpm","version":"10.0.0"}}}`))
		case "/pnpm/-/pnpm-10.0.0.tgz":
			w.Write(tgz)
		}
	}))
	defer srv.Close()

	reg := Registry{URL: srv.URL, Client: fetch.New("test")}
	p, err := reg.Packument(t.Context(), "pnpm")
	if err != nil {
		t.Fatal(err)
	}
	m, _ := p.Resolve("10")
	m.Dist.Tarball = srv.URL + "/pnpm/-/pnpm-10.0.0.tgz"
	sum := sha512.Sum512(tgz)
	m.Dist.Integrity = "sha512-" + base64.StdEncoding.EncodeToString(sum[:])

	dest := filepath.Join(t.TempDir(), "pnpm")
	pin := Digest{Algorithm: "sha512", Sum: sum[:]}
	if err := reg.Install(t.Context(), m, dest, pin); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "bin", "pnpm.cjs")); err != nil {
		t.Error(err)
	}

	bin := filepath.Join(t.TempDir(), "bin")
	m.Bin = json.RawMessage(`{"pnpm":"bin/pnpm.cjs"}`)
	if err := LinkBins(bin, dest, m); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(bin, "pnpm")); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("bin/pnpm not executable: %v %v", info, err)
	}

	wrong := Digest{Algorithm: "sha512", Sum: make([]byte, 64)}
	if err := reg.Install(t.Context(), m, filepath.Join(t.TempDir(), "x"), wrong); err == nil {
		t.Error("install with a wrong pinned hash succeeded")
	}
	m.Dist.Integrity = "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))
	if err := reg.Install(t.Context(), m, filepath.Join(t.TempDir(), "y")); err == nil {
		t.Error("install with a wrong integrity succeeded")
	}
}
