package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Version is one version of a GHCR container package.
type Version struct {
	ID      int64
	Digest  string
	Tags    []string
	Created time.Time
}

// Descriptor points at another manifest by digest.
type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
}

// Manifest is the part of an image manifest or index the plan needs.
type Manifest struct {
	MediaType string       `json:"mediaType"`
	Manifests []Descriptor `json:"manifests"` // set for an index
	Subject   *Descriptor  `json:"subject"`   // set for a referrer (attestation, SBOM, signature)
}

// Fetcher reads a manifest by digest.
type Fetcher func(digest string) (Manifest, error)

// Plan is what a cleanup run would do with one package.
type Plan struct {
	Delete []Version
	Keep   map[string]string // digest -> why it is kept
}

// MakePlan decides which versions of a package can be deleted.
//
// Deleting every untagged version, as most cleanup actions do, breaks images:
// a tag on a multi-architecture image points at an index, and the
// per-architecture manifests the index lists are untagged; so are the
// attestations, signatures and SBOMs that refer to an image through
// "subject". A version is therefore deleted only when it is untagged, older
// than grace, and nothing that is kept reaches it:
//
//   - every tagged version is kept, with every manifest it lists, recursively;
//   - a version whose subject is kept is kept too (and what it lists), until
//     nothing changes;
//   - a version whose manifest cannot be read is kept, never guessed at.
func MakePlan(versions []Version, fetch Fetcher, now time.Time, grace time.Duration) (Plan, error) {
	plan := Plan{Keep: map[string]string{}}
	byDigest := make(map[string]Version, len(versions))
	for _, v := range versions {
		byDigest[v.Digest] = v
	}
	manifests := map[string]Manifest{}

	var expand func(digest, why string) error
	expand = func(digest, why string) error {
		if _, done := plan.Keep[digest]; done {
			return nil
		}
		plan.Keep[digest] = why
		m, err := fetch(digest)
		if err != nil {
			if _, inPackage := byDigest[digest]; !inPackage {
				// A child the registry does not have: nothing to protect.
				return nil
			}
			return fmt.Errorf("reading %s (%s): %w", digest, why, err)
		}
		manifests[digest] = m
		for _, child := range m.Manifests {
			if err := expand(child.Digest, "listed by "+short(digest)); err != nil {
				return err
			}
		}
		return nil
	}

	// Roots: tags, and anything recent enough to be part of a push in flight.
	for _, v := range versions {
		switch {
		case len(v.Tags) > 0:
			if err := expand(v.Digest, "tagged "+strings.Join(v.Tags, ", ")); err != nil {
				// A tagged image that cannot be read makes the whole plan unsafe.
				return Plan{}, err
			}
		case now.Sub(v.Created) < grace:
			if err := expand(v.Digest, fmt.Sprintf("newer than %s", grace)); err != nil {
				plan.Keep[v.Digest] = "newer than grace period, unreadable"
			}
		}
	}

	// Referrers: keep whatever points at something kept, to a fixed point.
	for changed := true; changed; {
		changed = false
		for _, v := range versions {
			if _, kept := plan.Keep[v.Digest]; kept {
				continue
			}
			m, ok := manifests[v.Digest]
			if !ok {
				var err error
				if m, err = fetch(v.Digest); err != nil {
					plan.Keep[v.Digest] = "manifest unreadable: " + err.Error()
					changed = true
					continue
				}
				manifests[v.Digest] = m
			}
			if m.Subject == nil {
				continue
			}
			if _, subjectKept := plan.Keep[m.Subject.Digest]; subjectKept {
				if err := expand(v.Digest, "refers to "+short(m.Subject.Digest)); err != nil {
					plan.Keep[v.Digest] = "refers to a kept image, unreadable"
				}
				changed = true
			}
		}
	}

	for _, v := range versions {
		if _, kept := plan.Keep[v.Digest]; !kept {
			plan.Delete = append(plan.Delete, v)
		}
	}
	sort.Slice(plan.Delete, func(i, j int) bool { return plan.Delete[i].Created.Before(plan.Delete[j].Created) })
	return plan, nil
}

func short(digest string) string {
	if i := strings.IndexByte(digest, ':'); i >= 0 && len(digest) > i+13 {
		return digest[:i+13]
	}
	return digest
}
