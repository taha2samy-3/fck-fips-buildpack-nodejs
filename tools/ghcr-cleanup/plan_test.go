package main

import (
	"errors"
	"slices"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func old(digest string, tags ...string) Version {
	return Version{ID: int64(len(digest)), Digest: digest, Tags: tags, Created: now.Add(-30 * 24 * time.Hour)}
}

func index(children ...string) Manifest {
	m := Manifest{MediaType: "application/vnd.oci.image.index.v1+json"}
	for _, c := range children {
		m.Manifests = append(m.Manifests, Descriptor{Digest: c})
	}
	return m
}

func image() Manifest { return Manifest{MediaType: "application/vnd.oci.image.manifest.v1+json"} }

func referrer(subject string) Manifest {
	m := image()
	m.Subject = &Descriptor{Digest: subject}
	return m
}

func fetcher(t *testing.T, ms map[string]Manifest) Fetcher {
	return func(d string) (Manifest, error) {
		if m, ok := ms[d]; ok {
			return m, nil
		}
		return Manifest{}, errors.New("manifest unknown")
	}
}

func deleted(p Plan) []string {
	var out []string
	for _, v := range p.Delete {
		out = append(out, v.Digest)
	}
	slices.Sort(out)
	return out
}

func TestMultiArchChildrenAreKept(t *testing.T) {
	// run:latest -> index -> amd64, arm64 (untagged). The trap that breaks tags.
	versions := []Version{old("sha256:idx", "latest", "20261021.1"), old("sha256:amd64"), old("sha256:arm64")}
	ms := map[string]Manifest{"sha256:idx": index("sha256:amd64", "sha256:arm64"), "sha256:amd64": image(), "sha256:arm64": image()}
	plan, err := MakePlan(versions, fetcher(t, ms), now, 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Delete) != 0 {
		t.Errorf("deleted %v; per-arch manifests of a tagged index must stay", deleted(plan))
	}
}

func TestDanglingFromAnOldPushIsDeleted(t *testing.T) {
	// :latest moved to a new index; the previous index and its children are
	// untagged and unreferenced.
	versions := []Version{
		old("sha256:new", "latest"), old("sha256:new-amd64"), old("sha256:new-arm64"),
		old("sha256:prev"), old("sha256:prev-amd64"), old("sha256:prev-arm64"),
	}
	ms := map[string]Manifest{
		"sha256:new": index("sha256:new-amd64", "sha256:new-arm64"), "sha256:new-amd64": image(), "sha256:new-arm64": image(),
		"sha256:prev": index("sha256:prev-amd64", "sha256:prev-arm64"), "sha256:prev-amd64": image(), "sha256:prev-arm64": image(),
	}
	plan, err := MakePlan(versions, fetcher(t, ms), now, 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := deleted(plan), []string{"sha256:prev", "sha256:prev-amd64", "sha256:prev-arm64"}; !slices.Equal(got, want) {
		t.Errorf("deleted %v, want %v", got, want)
	}
}

func TestReferrersOfKeptImagesAreKept(t *testing.T) {
	// Attestation (untagged referrer) -> per-arch child of a tagged index, and
	// an attestation index whose own children are untagged.
	versions := []Version{
		old("sha256:idx", "1.0.0"), old("sha256:amd64"),
		old("sha256:att"),                              // subject: the tagged index
		old("sha256:att-idx"), old("sha256:att-layer"), // subject: amd64 child, lists att-layer
		old("sha256:orphan-att"), // subject: an image that no longer exists
	}
	attIdx := index("sha256:att-layer")
	attIdx.Subject = &Descriptor{Digest: "sha256:amd64"}
	ms := map[string]Manifest{
		"sha256:idx": index("sha256:amd64"), "sha256:amd64": image(),
		"sha256:att": referrer("sha256:idx"), "sha256:att-idx": attIdx, "sha256:att-layer": image(),
		"sha256:orphan-att": referrer("sha256:gone"),
	}
	plan, err := MakePlan(versions, fetcher(t, ms), now, 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := deleted(plan), []string{"sha256:orphan-att"}; !slices.Equal(got, want) {
		t.Errorf("deleted %v, want %v", got, want)
	}
}

func TestRecentVersionsAreKept(t *testing.T) {
	recent := Version{ID: 9, Digest: "sha256:in-flight", Created: now.Add(-10 * time.Minute)}
	plan, err := MakePlan([]Version{recent}, fetcher(t, map[string]Manifest{"sha256:in-flight": image()}), now, 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Delete) != 0 {
		t.Error("a version inside the grace period must be kept (a push may still be in progress)")
	}
}

func TestUnreadableUntaggedIsKept(t *testing.T) {
	plan, err := MakePlan([]Version{old("sha256:mystery")}, fetcher(t, nil), now, 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Delete) != 0 {
		t.Error("a version whose manifest cannot be read must be kept, not guessed at")
	}
}

func TestUnreadableTaggedAborts(t *testing.T) {
	if _, err := MakePlan([]Version{old("sha256:tagged", "latest")}, fetcher(t, nil), now, 72*time.Hour); err == nil {
		t.Error("a tagged image that cannot be read must abort the whole plan")
	}
}

func TestChildMissingFromThePackageIsTolerated(t *testing.T) {
	// The index lists a manifest the package does not hold (already broken
	// upstream): keep going, nothing to protect there.
	versions := []Version{old("sha256:idx", "latest")}
	plan, err := MakePlan(versions, fetcher(t, map[string]Manifest{"sha256:idx": index("sha256:missing")}), now, 72*time.Hour)
	if err != nil || len(plan.Delete) != 0 {
		t.Errorf("plan %v, err %v", deleted(plan), err)
	}
}
