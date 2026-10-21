package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// GitHub talks to the GitHub Packages REST API.
type GitHub struct {
	API   string // https://api.github.com
	Token string
	HTTP  *http.Client
	base  string // /users/<owner> or /orgs/<owner>
}

func (g *GitHub) do(ctx context.Context, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, g.API+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+g.Token)
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Init works out whether the owner is a user or an organisation.
func (g *GitHub) Init(ctx context.Context, owner string) error {
	var account struct {
		Type string `json:"type"`
	}
	if err := g.do(ctx, http.MethodGet, "/users/"+url.PathEscape(owner), &account); err != nil {
		return err
	}
	if account.Type == "Organization" {
		g.base = "/orgs/" + url.PathEscape(owner)
	} else {
		g.base = "/users/" + url.PathEscape(owner)
	}
	return nil
}

// Packages lists the owner's container packages whose name starts with prefix.
func (g *GitHub) Packages(ctx context.Context, prefix string) ([]string, error) {
	var names []string
	for page := 1; ; page++ {
		var batch []struct {
			Name string `json:"name"`
		}
		if err := g.do(ctx, http.MethodGet, fmt.Sprintf("%s/packages?package_type=container&per_page=100&page=%d", g.base, page), &batch); err != nil {
			return nil, err
		}
		for _, p := range batch {
			if strings.HasPrefix(p.Name, prefix) {
				names = append(names, p.Name)
			}
		}
		if len(batch) < 100 {
			return names, nil
		}
	}
}

// Versions lists every version of a package.
func (g *GitHub) Versions(ctx context.Context, pkg string) ([]Version, error) {
	var out []Version
	for page := 1; ; page++ {
		var batch []struct {
			ID        int64     `json:"id"`
			Name      string    `json:"name"`
			CreatedAt time.Time `json:"created_at"`
			Metadata  struct {
				Container struct {
					Tags []string `json:"tags"`
				} `json:"container"`
			} `json:"metadata"`
		}
		path := fmt.Sprintf("%s/packages/container/%s/versions?per_page=100&page=%d", g.base, url.PathEscape(pkg), page)
		if err := g.do(ctx, http.MethodGet, path, &batch); err != nil {
			return nil, err
		}
		for _, v := range batch {
			out = append(out, Version{ID: v.ID, Digest: v.Name, Tags: v.Metadata.Container.Tags, Created: v.CreatedAt})
		}
		if len(batch) < 100 {
			return out, nil
		}
	}
}

// Delete removes one package version.
func (g *GitHub) Delete(ctx context.Context, pkg string, id int64) error {
	return g.do(ctx, http.MethodDelete, fmt.Sprintf("%s/packages/container/%s/versions/%d", g.base, url.PathEscape(pkg), id), nil)
}

// Registry reads manifests from ghcr.io.
type Registry struct {
	Host     string // https://ghcr.io
	User     string
	Password string
	HTTP     *http.Client

	mu     sync.Mutex
	tokens map[string]string
}

var manifestTypes = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

func (r *Registry) token(ctx context.Context, repo string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.tokens[repo]; ok {
		return t, nil
	}
	q := url.Values{"service": {strings.TrimPrefix(strings.TrimPrefix(r.Host, "https://"), "http://")}, "scope": {"repository:" + repo + ":pull"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.Host+"/token?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	if r.Password != "" {
		req.SetBasicAuth(r.User, r.Password)
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry token for %s: %s", repo, resp.Status)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if r.tokens == nil {
		r.tokens = map[string]string{}
	}
	r.tokens[repo] = body.Token
	return body.Token, nil
}

// Manifest fetches repo@digest (repo is "<owner>/<package>").
func (r *Registry) Manifest(ctx context.Context, repo, digest string) (Manifest, error) {
	tok, err := r.token(ctx, repo)
	if err != nil {
		return Manifest{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/v2/%s/manifests/%s", r.Host, repo, digest), nil)
	if err != nil {
		return Manifest{}, err
	}
	req.Header.Set("Accept", manifestTypes)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return Manifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("manifest %s@%s: %s", repo, digest, resp.Status)
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("manifest %s@%s: %w", repo, digest, err)
	}
	return m, nil
}
