package fetch

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestGetRetriesServerErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		if r.Header.Get("User-Agent") != "ua" {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := New("ua")
	path := filepath.Join(t.TempDir(), "out")
	if err := c.Download(t.Context(), srv.URL, path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "ok" || calls.Load() != 2 {
		t.Errorf("body %q after %d calls", b, calls.Load())
	}
}

func TestGetDoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	err := New("ua").Download(t.Context(), srv.URL, filepath.Join(t.TempDir(), "out"))
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusNotFound || calls.Load() != 1 {
		t.Errorf("err = %v after %d calls", err, calls.Load())
	}
}

func TestCachedRevalidatesAndFallsBack(t *testing.T) {
	var calls atomic.Int32
	up := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !up {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		if r.Header.Get("If-None-Match") == `"1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"1"`)
		w.Write([]byte("index"))
	}))
	defer srv.Close()

	c := New("ua")
	c.Attempts = 1
	c.CacheDir = t.TempDir()

	for range 2 {
		path, err := c.Cached(t.Context(), srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(path); string(b) != "index" {
			t.Errorf("content = %q", b)
		}
	}

	up = false
	path, err := c.Cached(t.Context(), srv.URL)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("want ErrStale, got %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "index" {
		t.Errorf("stale content = %q", b)
	}

	empty := New("ua")
	empty.Attempts = 1
	empty.CacheDir = t.TempDir()
	if _, err := empty.Cached(t.Context(), srv.URL); err == nil || errors.Is(err, ErrStale) {
		t.Errorf("without a cached copy want a hard error, got %v", err)
	}
}
