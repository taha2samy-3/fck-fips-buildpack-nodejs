// Package npm resolves and downloads packages from an npm registry,
// verifying every tarball against its published integrity.
package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1" //nolint:gosec // only accepted for corepack's legacy "+sha1." pins
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/archive"
	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/fetch"
)

// DefaultRegistry is the public npm registry.
const DefaultRegistry = "https://registry.npmjs.org"

// Registry is an npm compatible registry (npmjs.org, Verdaccio, Artifactory…).
type Registry struct {
	URL    string
	Client *fetch.Client
}

// Manifest is the part of a version's metadata the buildpacks use.
type Manifest struct {
	Name    string          `json:"name"`
	Version string          `json:"version"`
	Bin     json.RawMessage `json:"bin"`
	// OptionalDependencies maps package names to exact versions or ranges.
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	Engines              json.RawMessage   `json:"engines"`
	Deprecated           json.RawMessage   `json:"deprecated"`
	Dist                 Dist              `json:"dist"`
}

// Dist locates a version's tarball and its Subresource Integrity.
type Dist struct {
	Tarball   string `json:"tarball"`
	Integrity string `json:"integrity"`
}

// Engine returns the engines entry for name ("node"), or "" when absent.
// Old packages have malformed engines fields (arrays, nested objects), which
// count as absent rather than failing the whole document.
func (m Manifest) Engine(name string) string {
	var engines map[string]any
	if json.Unmarshal(m.Engines, &engines) != nil {
		return ""
	}
	v, _ := engines[name].(string)
	return v
}

// Packument lists every published version of a package.
type Packument struct {
	Name     string              `json:"name"`
	DistTags map[string]string   `json:"dist-tags"`
	Versions map[string]Manifest `json:"versions"`
}

// Packument fetches the abbreviated metadata document for name.
func (r Registry) Packument(ctx context.Context, name string) (Packument, error) {
	url := strings.TrimSuffix(r.URL, "/") + "/" + strings.ReplaceAll(name, "/", "%2f")
	resp, err := r.Client.Get(ctx, url, map[string][]string{
		// The abbreviated form is a fraction of the size and has everything needed.
		"Accept": {"application/vnd.npm.install-v1+json; q=1.0, application/json; q=0.8"},
	})
	if err != nil {
		return Packument{}, err
	}
	defer resp.Body.Close()

	var p Packument
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return Packument{}, fmt.Errorf("decoding %s: %w", url, err)
	}
	if len(p.Versions) == 0 {
		return Packument{}, fmt.Errorf("%s lists no versions", url)
	}
	return p, nil
}

// Resolve picks the version a spec selects: an exact version, a dist-tag
// ("latest", "next-10") or a semver range ("10", "^9.1", "11.x"). Ranges
// select the highest match and skip pre-releases unless the range names one.
func (p Packument) Resolve(spec string) (Manifest, error) {
	spec = strings.TrimSpace(strings.TrimPrefix(spec, "v"))
	if spec == "" {
		spec = "latest"
	}

	if m, ok := p.Versions[spec]; ok {
		return m, nil
	}
	if v, ok := p.DistTags[spec]; ok {
		if m, ok := p.Versions[v]; ok {
			return m, nil
		}
	}

	constraint, err := semver.NewConstraint(spec)
	if err != nil {
		return Manifest{}, fmt.Errorf("%s: %q is neither a version, a dist-tag nor a range", p.Name, spec)
	}
	var best *semver.Version
	for raw := range p.Versions {
		v, err := semver.StrictNewVersion(raw)
		if err != nil || !constraint.Check(v) {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best = v
		}
	}
	if best == nil {
		return Manifest{}, fmt.Errorf("no published version of %s satisfies %q (latest is %s)", p.Name, spec, p.DistTags["latest"])
	}
	return p.Versions[best.Original()], nil
}

// Digest is an expected hash of a tarball.
type Digest struct {
	Algorithm string // sha1, sha256, sha512
	Sum       []byte
}

// ParseIntegrity reads a Subresource Integrity string ("sha512-<base64>",
// possibly several separated by spaces) and returns the strongest digest.
func ParseIntegrity(sri string) (Digest, error) {
	var best Digest
	rank := map[string]int{"sha256": 1, "sha384": 2, "sha512": 3}
	for _, field := range strings.Fields(sri) {
		algo, b64, ok := strings.Cut(field, "-")
		if !ok || rank[algo] == 0 {
			continue
		}
		if i := strings.IndexByte(b64, '?'); i >= 0 {
			b64 = b64[:i]
		}
		sum, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			continue
		}
		if rank[algo] > rank[best.Algorithm] {
			best = Digest{Algorithm: algo, Sum: sum}
		}
	}
	if best.Algorithm == "" {
		return Digest{}, fmt.Errorf("no usable sha256/sha384/sha512 integrity in %q", sri)
	}
	return best, nil
}

// ParseCorepackHash reads the hash suffix of a packageManager field
// ("sha512.<hex>"), as written by corepack.
func ParseCorepackHash(s string) (Digest, error) {
	algo, hexSum, ok := strings.Cut(s, ".")
	if !ok {
		return Digest{}, fmt.Errorf("malformed packageManager hash %q", s)
	}
	switch algo {
	case "sha1", "sha224", "sha256", "sha512":
	default:
		return Digest{}, fmt.Errorf("unsupported packageManager hash algorithm %q", algo)
	}
	sum, err := hex.DecodeString(hexSum)
	if err != nil {
		return Digest{}, fmt.Errorf("malformed packageManager hash %q: %w", s, err)
	}
	return Digest{Algorithm: algo, Sum: sum}, nil
}

func (d Digest) newHash() hash.Hash {
	switch d.Algorithm {
	case "sha1":
		return sha1.New() //nolint:gosec
	case "sha224":
		return sha256.New224()
	case "sha256":
		return sha256.New()
	case "sha384":
		return sha512.New384()
	default:
		return sha512.New()
	}
}

// Install downloads the tarball of m, checks it against its registry
// integrity and any extra digests, and extracts the package contents
// (without the leading "package/" directory) into dest.
func (r Registry) Install(ctx context.Context, m Manifest, dest string, extra ...Digest) error {
	digests := append([]Digest(nil), extra...)
	integrity, err := ParseIntegrity(m.Dist.Integrity)
	if err != nil {
		return fmt.Errorf("%s@%s: %w", m.Name, m.Version, err)
	}
	digests = append(digests, integrity)

	tmp, err := os.CreateTemp("", "npm-*.tgz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	tmp.Close()

	if err := r.Client.Download(ctx, m.Dist.Tarball, tmp.Name()); err != nil {
		return fmt.Errorf("downloading %s@%s: %w", m.Name, m.Version, err)
	}

	f, err := os.Open(tmp.Name())
	if err != nil {
		return err
	}
	defer f.Close()

	hashes := make([]hash.Hash, len(digests))
	writers := make([]io.Writer, len(digests))
	for i, d := range digests {
		hashes[i] = d.newHash()
		writers[i] = hashes[i]
	}
	if _, err := io.Copy(io.MultiWriter(writers...), f); err != nil {
		return err
	}
	for i, d := range digests {
		if got := hashes[i].Sum(nil); !bytes.Equal(got, d.Sum) {
			return fmt.Errorf("%s@%s: tarball %s digest does not match (got %x)", m.Name, m.Version, d.Algorithm, got)
		}
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s@%s: %w", m.Name, m.Version, err)
	}
	defer gz.Close()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	// npm tarballs keep everything under one top-level directory, normally "package/".
	return archive.Extract(tar.NewReader(gz), dest, archive.StripComponents(1))
}

// Bins returns the executables a manifest declares, by command name.
func (m Manifest) Bins() (map[string]string, error) {
	if len(m.Bin) == 0 || string(m.Bin) == "null" {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(m.Bin, &single); err == nil {
		name := m.Name
		if i := strings.LastIndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		return map[string]string{name: single}, nil
	}
	var many map[string]string
	if err := json.Unmarshal(m.Bin, &many); err != nil {
		return nil, errors.New("unrecognised \"bin\" field")
	}
	return many, nil
}

// LinkBins creates binDir/<command> symlinks to the executables a package
// installed in pkgDir declares, and marks them executable. The scripts start
// with "#!/usr/bin/env node", so no shell wrapper is needed.
func LinkBins(binDir, pkgDir string, m Manifest) error {
	bins, err := m.Bins()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	for name, rel := range bins {
		target := filepath.Join(pkgDir, filepath.FromSlash(rel))
		if err := os.Chmod(target, 0o755); err != nil {
			return fmt.Errorf("bin %s: %w", name, err)
		}
		link, err := filepath.Rel(binDir, target)
		if err != nil {
			return err
		}
		if err := os.Symlink(link, filepath.Join(binDir, name)); err != nil {
			return err
		}
	}
	return nil
}
