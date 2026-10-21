package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubClient(t *testing.T) {
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing token on %s", r.URL)
		}
		switch {
		case r.URL.Path == "/users/taha":
			fmt.Fprint(w, `{"type":"User"}`)
		case r.URL.Path == "/users/taha/packages":
			if r.URL.Query().Get("page") == "1" {
				var batch []map[string]string
				for i := range 100 {
					batch = append(batch, map[string]string{"name": fmt.Sprintf("other/%d", i)})
				}
				batch[7]["name"] = "fck-fips/run"
				json.NewEncoder(w).Encode(batch)
				return
			}
			fmt.Fprint(w, `[{"name":"fck-fips/cnb/pnpm"}]`)
		case r.URL.EscapedPath() == "/users/taha/packages/container/fck-fips%2Frun/versions":
			fmt.Fprint(w, `[{"id":7,"name":"sha256:a","created_at":"2026-10-01T00:00:00Z","metadata":{"container":{"tags":["latest"]}}}]`)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.EscapedPath(), "/users/taha/packages/container/fck-fips%2Frun/versions/"):
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	g := &GitHub{API: srv.URL, Token: "tok", HTTP: srv.Client()}
	if err := g.Init(t.Context(), "taha"); err != nil {
		t.Fatal(err)
	}
	pkgs, err := g.Packages(t.Context(), "fck-fips/")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pkgs, ",") != "fck-fips/run,fck-fips/cnb/pnpm" {
		t.Errorf("packages = %v (pagination and prefix filter)", pkgs)
	}
	versions, err := g.Versions(t.Context(), "fck-fips/run")
	if err != nil || len(versions) != 1 || versions[0].Tags[0] != "latest" || versions[0].ID != 7 {
		t.Fatalf("versions = %+v, %v", versions, err)
	}
	if err := g.Delete(t.Context(), "fck-fips/run", 7); err != nil || len(deleted) != 1 {
		t.Errorf("delete: %v %v", deleted, err)
	}
}

func TestRegistryClient(t *testing.T) {
	var tokenRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenRequests++
			if r.URL.Query().Get("scope") != "repository:taha/fck-fips/run:pull" {
				t.Errorf("scope = %q", r.URL.Query().Get("scope"))
			}
			if u, p, ok := r.BasicAuth(); !ok || u != "actor" || p != "secret" {
				t.Error("token request without basic auth")
			}
			fmt.Fprint(w, `{"token":"bearer-1"}`)
		case "/v2/taha/fck-fips/run/manifests/sha256:idx":
			if r.Header.Get("Authorization") != "Bearer bearer-1" {
				t.Error("manifest request without the bearer token")
			}
			if !strings.Contains(r.Header.Get("Accept"), "application/vnd.oci.image.index.v1+json") {
				t.Error("index media type not accepted")
			}
			fmt.Fprint(w, `{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"sha256:amd64"}],"subject":{"digest":"sha256:x"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	reg := &Registry{Host: srv.URL, User: "actor", Password: "secret", HTTP: srv.Client()}
	for range 2 {
		m, err := reg.Manifest(t.Context(), "taha/fck-fips/run", "sha256:idx")
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Manifests) != 1 || m.Manifests[0].Digest != "sha256:amd64" || m.Subject.Digest != "sha256:x" {
			t.Errorf("manifest = %+v", m)
		}
	}
	if tokenRequests != 1 {
		t.Errorf("token requested %d times, want 1 (cached)", tokenRequests)
	}
	if _, err := reg.Manifest(t.Context(), "taha/fck-fips/run", "sha256:missing"); err == nil {
		t.Error("a missing manifest must be an error")
	}
}
