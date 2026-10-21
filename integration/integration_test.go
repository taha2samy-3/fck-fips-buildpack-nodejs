//go:build integration

// Package integration builds the apps in testdata with the real builder and
// checks the running containers. It needs pack and docker, and a builder
// created by scripts/package.sh:
//
//	scripts/package.sh && go test -tags integration -v -timeout 60m ./integration
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// response is what testdata/*/server.js serves.
type response struct {
	Node        string `json:"node"`
	OpenSSL     string `json:"openssl"`
	FIPS        int    `json:"fips"`
	MD5         bool   `json:"md5"`
	MS          int    `json:"ms"`
	NodeOptions string `json:"nodeOptions"`
}

func builder(t *testing.T) string {
	if b := os.Getenv("BUILDER"); b != "" {
		return b
	}
	version, err := os.ReadFile("../VERSION")
	if err != nil {
		t.Fatal(err)
	}
	return "ghcr.io/taha2samy-3/fck-fips/builder-nodejs:" + strings.TrimSpace(string(version))
}

func registry(t *testing.T) string {
	b := builder(t)
	return b[:strings.LastIndex(b, "/")]
}

func TestApps(t *testing.T) {
	cases := []struct {
		name   string
		app    string
		env    []string
		args   []string
		memory string
		check  func(t *testing.T, r response)
	}{
		{
			name: "npm uses engines.node",
			app:  "npm",
			check: func(t *testing.T, r response) {
				wantPrefix(t, "node", r.Node, "v22.")
				if r.FIPS != 0 || !r.MD5 {
					t.Errorf("FIPS should be off by default: %+v", r)
				}
			},
		},
		{
			name: "yarn gets the default Node.js",
			app:  "yarn",
			check: func(t *testing.T, r response) {
				wantPrefix(t, "node", r.Node, "v24.")
			},
		},
		{
			name:   "pnpm in FIPS mode",
			app:    "pnpm",
			env:    []string{"BP_OPENSSL_FIPS=true", "BP_NODE_OPTIMIZE_MEMORY=true"},
			memory: "512m",
			check: func(t *testing.T, r response) {
				if r.FIPS != 1 || r.MD5 {
					t.Errorf("expected FIPS mode with MD5 refused: %+v", r)
				}
				for _, opt := range []string{"--force-fips", "--max-old-space-size=384"} {
					if !strings.Contains(r.NodeOptions, opt) {
						t.Errorf("NODE_OPTIONS %q lacks %s", r.NodeOptions, opt)
					}
				}
			},
		},
		{
			name: "pnpm version override relaxes packageManager",
			app:  "pnpm",
			env:  []string{"BP_PNPM_VERSION=11"},
			check: func(t *testing.T, r response) {
				if r.MS != 3600000 {
					t.Errorf("dependency not installed: %+v", r)
				}
			},
		},
		{
			name: "pnpm 12 native executable",
			app:  "pnpm",
			env:  []string{"BP_PNPM_VERSION=12"},
			check: func(t *testing.T, r response) {
				if r.MS != 3600000 {
					t.Errorf("dependency not installed: %+v", r)
				}
			},
		},
		{
			name: "distroless run image",
			app:  "pnpm",
			args: []string{"--run-image", "%REGISTRY%/run-distroless:latest"},
			check: func(t *testing.T, r response) {
				if r.MS != 3600000 {
					t.Errorf("dependency not installed: %+v", r)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			image := "fckfips-it-" + strings.ReplaceAll(strings.ToLower(tc.name), " ", "-")
			image = strings.Map(func(r rune) rune {
				if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
					return r
				}
				return '-'
			}, image)

			args := []string{"build", image, "--path", "testdata/" + tc.app, "--builder", builder(t),
				"--pull-policy", "if-not-present", "--trust-builder", "--clear-cache"}
			for _, e := range tc.env {
				args = append(args, "--env", e)
			}
			version := builder(t)[strings.LastIndex(builder(t), ":")+1:]
			for _, a := range tc.args {
				a = strings.ReplaceAll(a, "%REGISTRY%", registry(t))
				args = append(args, strings.ReplaceAll(a, "%VERSION%", version))
			}
			run(t, "pack", args...)
			t.Cleanup(func() { exec.Command("docker", "rmi", "-f", image).Run() })

			tc.check(t, serve(t, image, tc.memory))
		})
	}
}

// TestFIPSFailsClosed corrupts the FIPS module and expects node to refuse to
// start instead of running without FIPS.
func TestFIPSFailsClosed(t *testing.T) {
	image := "fckfips-it-fips-tamper"
	run(t, "pack", "build", image, "--path", "testdata/pnpm", "--builder", builder(t),
		"--pull-policy", "if-not-present", "--trust-builder", "--env", "BP_OPENSSL_FIPS=true")
	t.Cleanup(func() { exec.Command("docker", "rmi", "-f", image).Run() })

	script := `printf '\000\000\000\000' | dd of=/layers/fckfips_openssl-fips/openssl-fips/lib/ossl-modules/fips.so bs=1 seek=200000 conv=notrunc 2>/dev/null
exec /cnb/lifecycle/launcher node -e 'console.log(require("crypto").getFips())'`
	out, err := exec.Command("docker", "run", "--rm", "--user", "0", "--entrypoint", "bash", image, "-c", script).CombinedOutput()
	if err == nil {
		t.Fatalf("node started with a corrupted FIPS module:\n%s", out)
	}
	if !strings.Contains(string(out), "fips") {
		t.Errorf("unexpected failure:\n%s", out)
	}
}

func serve(t *testing.T, image, memory string) response {
	t.Helper()
	port := freePort(t)
	args := []string{"run", "-d", "-p", fmt.Sprintf("127.0.0.1:%d:8080", port)}
	if memory != "" {
		args = append(args, "--memory", memory)
	}
	id := strings.TrimSpace(run(t, "docker", append(args, image)...))
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("docker", "logs", id).CombinedOutput()
			t.Logf("container logs:\n%s", out)
		}
		exec.Command("docker", "rm", "-f", id).Run()
	})

	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err == nil {
			defer resp.Body.Close()
			var r response
			if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
				t.Fatal(err)
			}
			t.Logf("app: %+v", r)
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("app did not answer: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out.String())
	}
	return out.String()
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func wantPrefix(t *testing.T, what, got, prefix string) {
	t.Helper()
	if !strings.HasPrefix(got, prefix) {
		t.Errorf("%s = %q, want %s*", what, got, prefix)
	}
}
