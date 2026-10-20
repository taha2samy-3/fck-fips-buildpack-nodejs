package apk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/archive"
	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/fetch"
)

// Repository is a signed apk repository such as https://packages.wolfi.dev/os.
type Repository struct {
	URL    string
	Keys   Keyring
	Client *fetch.Client
}

// Arch maps a Go/OCI architecture name to the apk one.
func Arch(goarch string) (string, error) {
	switch goarch {
	case "amd64", "x86_64":
		return "x86_64", nil
	case "arm64", "aarch64":
		return "aarch64", nil
	}
	return "", fmt.Errorf("architecture %q is not supported", goarch)
}

// Index downloads (or revalidates) and verifies the repository index for
// arch. A cached index that could not be revalidated is still used, since
// its signature is checked either way; stale reports that case.
func (r Repository) Index(ctx context.Context, arch string) (idx *Index, stale bool, err error) {
	url := strings.TrimSuffix(r.URL, "/") + "/" + arch + "/APKINDEX.tar.gz"
	path, err := r.Client.Cached(ctx, url)
	if err != nil && !errors.Is(err, fetch.ErrStale) {
		return nil, false, fmt.Errorf("fetching %s: %w", url, err)
	}
	stale = err != nil
	if r.Client.CacheDir == "" {
		defer os.Remove(path)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	pkgs, err := ReadSignedIndex(f, r.Keys)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", url, err)
	}
	return NewIndex(pkgs), stale, nil
}

// Install downloads pkgs, verifies each against the signed index entry and
// extracts them below dest in the order given, so a later package overrides
// files of an earlier one. Nothing is written to dest until every package
// has been verified.
func (r Repository) Install(ctx context.Context, arch string, pkgs []Package, dest string, mapName archive.Mapper) error {
	tmp, err := os.MkdirTemp("", "apk-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	paths := make([]string, len(pkgs))
	errs := make([]error, len(pkgs))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, p := range pkgs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			paths[i] = filepath.Join(tmp, p.Filename())
			errs[i] = r.fetchVerified(ctx, arch, p, paths[i])
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}

	for i, p := range pkgs {
		f, err := os.Open(paths[i])
		if err != nil {
			return err
		}
		err = Extract(f, dest, mapName)
		f.Close()
		if err != nil {
			return fmt.Errorf("extracting %s: %w", p, err)
		}
	}
	return nil
}

func (r Repository) fetchVerified(ctx context.Context, arch string, p Package, path string) error {
	url := strings.TrimSuffix(r.URL, "/") + "/" + arch + "/" + p.Filename()
	if err := r.Client.Download(ctx, url, path); err != nil {
		return fmt.Errorf("downloading %s: %w", p, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return Verify(f, p, r.Keys)
}

// UsrMerge maps an apk's /usr tree onto the layer root, so that usr/bin and
// usr/lib land in <layer>/bin and <layer>/lib, which the CNB lifecycle puts
// on PATH and LD_LIBRARY_PATH by itself.
func UsrMerge(name string) (string, bool) {
	if name == "usr" {
		return "", false
	}
	if rest, ok := strings.CutPrefix(name, "usr/"); ok {
		return rest, true
	}
	return name, true
}
