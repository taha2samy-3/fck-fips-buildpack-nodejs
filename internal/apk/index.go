package apk

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Package is one entry of an APKINDEX.
type Package struct {
	Name          string
	Version       string
	Arch          string
	Description   string
	URL           string
	License       string
	Origin        string
	Commit        string
	Checksum      string // "Q1" + base64(SHA-1 of the control segment)
	Size          int64
	InstalledSize int64
	Depends       []string
	Provides      []string
}

// Filename is the package's file name inside its repository directory.
func (p Package) Filename() string {
	return p.Name + "-" + p.Version + ".apk"
}

// String is "name-version", the form apk prints.
func (p Package) String() string {
	return p.Name + "-" + p.Version
}

// controlSHA1 decodes the "Q1" checksum into the raw SHA-1 digest.
func (p Package) controlSHA1() ([]byte, error) {
	encoded, ok := strings.CutPrefix(p.Checksum, "Q1")
	if !ok {
		return nil, fmt.Errorf("package %s has unsupported checksum %q", p, p.Checksum)
	}
	sum, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(sum) != 20 {
		return nil, fmt.Errorf("package %s has malformed checksum %q", p, p.Checksum)
	}
	return sum, nil
}

// ParseIndex reads the text form of an APKINDEX: blocks of "K:value" lines
// separated by blank lines.
func ParseIndex(r io.Reader) ([]Package, error) {
	var (
		pkgs []Package
		cur  Package
		seen bool
	)
	flush := func() {
		if seen && cur.Name != "" {
			pkgs = append(pkgs, cur)
		}
		cur, seen = Package{}, false
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || len(key) != 1 {
			continue
		}
		seen = true
		switch key {
		case "P":
			cur.Name = value
		case "V":
			cur.Version = value
		case "A":
			cur.Arch = value
		case "T":
			cur.Description = value
		case "U":
			cur.URL = value
		case "L":
			cur.License = value
		case "o":
			cur.Origin = value
		case "c":
			cur.Commit = value
		case "C":
			cur.Checksum = value
		case "S":
			cur.Size, _ = strconv.ParseInt(value, 10, 64)
		case "I":
			cur.InstalledSize, _ = strconv.ParseInt(value, 10, 64)
		case "D":
			cur.Depends = strings.Fields(value)
		case "p":
			cur.Provides = strings.Fields(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading APKINDEX: %w", err)
	}
	flush()
	return pkgs, nil
}

// Index answers lookups over the packages of one repository and architecture.
type Index struct {
	packages []Package
	byName   map[string][]int
	byProv   map[string][]int
}

// NewIndex builds the lookup tables for pkgs.
func NewIndex(pkgs []Package) *Index {
	idx := &Index{
		packages: pkgs,
		byName:   make(map[string][]int),
		byProv:   make(map[string][]int),
	}
	for i, p := range pkgs {
		idx.byName[p.Name] = append(idx.byName[p.Name], i)
		for _, prov := range p.Provides {
			name, _, _ := strings.Cut(prov, "=")
			idx.byProv[name] = append(idx.byProv[name], i)
		}
	}
	return idx
}

// Packages returns every entry in the index.
func (x *Index) Packages() []Package { return x.packages }

// Versions returns every version of the named package, newest first.
func (x *Index) Versions(name string) []Package {
	out := make([]Package, 0, len(x.byName[name]))
	for _, i := range x.byName[name] {
		out = append(out, x.packages[i])
	}
	sort.SliceStable(out, func(i, j int) bool {
		return CompareVersions(out[i].Version, out[j].Version) > 0
	})
	return out
}

// Latest returns the newest version of the named package.
func (x *Index) Latest(name string) (Package, bool) {
	versions := x.Versions(name)
	if len(versions) == 0 {
		return Package{}, false
	}
	return versions[0], true
}

// Provider returns the package that best satisfies a dependency token such
// as "so:libuv.so.1", "cmd:node" or a plain package name. A package with the
// exact name wins over packages that only provide it; otherwise the newest
// provider wins, ties broken by name so the choice is deterministic.
func (x *Index) Provider(dep string) (Package, bool) {
	name := depName(dep)
	if p, ok := x.Latest(name); ok {
		return p, true
	}

	var (
		best  Package
		found bool
	)
	for _, i := range x.byProv[name] {
		p := x.packages[i]
		if !found {
			best, found = p, true
			continue
		}
		if betterProvider(p, best, name) {
			best = p
		}
	}
	return best, found
}

// Closure returns roots plus everything they depend on, transitively, in a
// stable order: shared libraries ("so:") and named packages (data packages
// such as icu78-data-full). Command dependencies ("cmd:") are tools the build
// image supplies and conflicts ("!pkg") are not dependencies, so neither is
// followed. skip prunes packages, and whatever is only reachable through
// them, from the result.
func (x *Index) Closure(roots []Package, skip func(Package) bool) ([]Package, error) {
	var (
		out   []Package
		seen  = make(map[string]bool)
		queue = append([]Package(nil), roots...)
	)
	for _, r := range roots {
		seen[r.Name] = true
	}

	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		out = append(out, p)

		for _, dep := range p.Depends {
			if strings.HasPrefix(dep, "cmd:") || strings.HasPrefix(dep, "!") {
				continue
			}
			provider, ok := x.Provider(dep)
			if !ok {
				return nil, fmt.Errorf("%s depends on %s, which no package in the index provides", p, dep)
			}
			if seen[provider.Name] || skip(provider) {
				continue
			}
			seen[provider.Name] = true
			queue = append(queue, provider)
		}
	}

	sort.SliceStable(out[len(roots):], func(i, j int) bool {
		return out[len(roots)+i].Name < out[len(roots)+j].Name
	})
	return out, nil
}

// depName strips version constraints from a dependency token
// ("so:libz.so.1>=1.3" -> "so:libz.so.1").
func depName(dep string) string {
	if i := strings.IndexAny(dep, "<>=~"); i >= 0 {
		return dep[:i]
	}
	return dep
}

// betterProvider orders providers of name: the higher provided version wins
// (for "so:" names that is the soname version, the same across releases), then
// the newer package, then the lower package name.
func betterProvider(p, best Package, name string) bool {
	if c := CompareVersions(providedVersion(p, name), providedVersion(best, name)); c != 0 {
		return c > 0
	}
	if c := CompareVersions(p.Version, best.Version); c != 0 {
		return c > 0
	}
	return p.Name < best.Name
}

// providedVersion is the version a package declares for a provided name,
// falling back to the package's own version.
func providedVersion(p Package, name string) string {
	for _, prov := range p.Provides {
		n, v, ok := strings.Cut(prov, "=")
		if n == name && ok {
			return v
		}
	}
	return p.Version
}

// BaseSystem reports whether p belongs to the base image rather than to a
// layer: glibc and its loader (they must match the image the app runs on)
// and the filesystem layout and core utilities every Wolfi image has.
func BaseSystem(p Package) bool {
	switch {
	case p.Origin == "glibc" || strings.HasPrefix(p.Origin, "glibc-"),
		strings.HasPrefix(p.Name, "wolfi-"),
		strings.HasPrefix(p.Name, "merged-"),
		p.Name == "busybox", p.Name == "ca-certificates-bundle", p.Name == "tzdata":
		return true
	}
	return false
}
