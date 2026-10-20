// Package fetch downloads files over HTTP with retries and, optionally, an
// on-disk cache revalidated with ETag / Last-Modified.
package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Client is safe for concurrent use.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	// Attempts is the number of tries for a request that fails with a network
	// error or a 5xx/429 response. Zero means 3.
	Attempts int
	// CacheDir, when set, keeps downloaded files for Cached.
	CacheDir string
}

// New returns a client with sensible timeouts that honours HTTP(S)_PROXY.
func New(userAgent string) *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 15 * time.Minute},
		UserAgent: userAgent,
	}
}

// StatusError is returned for a response that is not 2xx (or 304 for Cached).
type StatusError struct {
	URL    string
	Status int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("GET %s: %d %s", e.URL, e.Status, http.StatusText(e.Status))
}

// Get performs a GET and returns the response for a 2xx status. Retries
// happen before any body is handed to the caller.
func (c *Client) Get(ctx context.Context, url string, header http.Header) (*http.Response, error) {
	attempts := c.Attempts
	if attempts <= 0 {
		attempts = 3
	}

	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * time.Second):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range header {
			req.Header[k] = v
		}
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 || resp.StatusCode == http.StatusNotModified {
			return resp, nil
		}
		resp.Body.Close()
		lastErr = &StatusError{URL: url, Status: resp.StatusCode}
		if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			break
		}
	}
	return nil, lastErr
}

// Download writes the body of url to path, replacing it atomically.
func (c *Client) Download(ctx context.Context, url, path string) error {
	resp, err := c.Get(ctx, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return &StatusError{URL: url, Status: resp.StatusCode}
	}
	return writeAtomic(path, resp.Body)
}

type cacheMeta struct {
	URL          string `json:"url"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
}

// Cached returns a local path holding the current content of url. With a
// CacheDir the previous copy is revalidated and reused when unchanged. If the
// server cannot be reached the previous copy is returned together with the
// error wrapped in ErrStale, so callers that can tolerate stale data (a signed
// index, for example) may carry on.
func (c *Client) Cached(ctx context.Context, url string) (string, error) {
	if c.CacheDir == "" {
		f, err := os.CreateTemp("", "fetch-*")
		if err != nil {
			return "", err
		}
		f.Close()
		return f.Name(), c.Download(ctx, url, f.Name())
	}

	key := sha256.Sum256([]byte(url))
	base := filepath.Join(c.CacheDir, hex.EncodeToString(key[:16]))
	dataPath, metaPath := base+".data", base+".json"

	var meta cacheMeta
	header := http.Header{}
	if raw, err := os.ReadFile(metaPath); err == nil && json.Unmarshal(raw, &meta) == nil && meta.URL == url {
		if _, err := os.Stat(dataPath); err == nil {
			if meta.ETag != "" {
				header.Set("If-None-Match", meta.ETag)
			}
			if meta.LastModified != "" {
				header.Set("If-Modified-Since", meta.LastModified)
			}
		}
	}
	haveCopy := len(header) > 0

	resp, err := c.Get(ctx, url, header)
	if err != nil {
		if haveCopy {
			return dataPath, &staleError{err: err}
		}
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified && haveCopy {
		return dataPath, nil
	}

	if err := os.MkdirAll(c.CacheDir, 0o755); err != nil {
		return "", err
	}
	if err := writeAtomic(dataPath, resp.Body); err != nil {
		return "", err
	}
	meta = cacheMeta{URL: url, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}
	raw, _ := json.Marshal(meta)
	if err := os.WriteFile(metaPath, raw, 0o644); err != nil {
		return "", err
	}
	return dataPath, nil
}

// ErrStale marks a Cached result that could not be revalidated.
var ErrStale = errors.New("using cached copy, revalidation failed")

type staleError struct{ err error }

func (e *staleError) Error() string { return fmt.Sprintf("%v: %v", ErrStale, e.err) }
func (e *staleError) Unwrap() []error {
	return []error{ErrStale, e.err}
}

func writeAtomic(path string, r io.Reader) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// UserAgent identifies a buildpack to the servers it downloads from.
func UserAgent(id, version string) string {
	return "fck-fips-buildpacks-nodejs " + id + "/" + version + " (+https://github.com/taha2samy-3/fck-fips-buildpacks-nodejs)"
}
