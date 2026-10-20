// Package nodejs holds the meta buildpack. It has no code; this test keeps its
// two manifests consistent, since Dependabot cannot update either of them.
package nodejs

import (
	"regexp"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

type manifest struct {
	Order []struct {
		Group []struct {
			ID       string `toml:"id"`
			Version  string `toml:"version"`
			Optional bool   `toml:"optional"`
		} `toml:"group"`
	} `toml:"order"`
}

type pkg struct {
	Dependencies []struct {
		URI string `toml:"uri"`
	} `toml:"dependencies"`
}

var paketoURI = regexp.MustCompile(`^docker://docker\.io/paketobuildpacks/([a-z-]+):(\d+\.\d+\.\d+)$`)

func TestOrderMatchesPackage(t *testing.T) {
	var m manifest
	if _, err := toml.DecodeFile("buildpack.toml", &m); err != nil {
		t.Fatal(err)
	}
	var p pkg
	if _, err := toml.DecodeFile("package.toml", &p); err != nil {
		t.Fatal(err)
	}

	packaged := map[string]string{}
	for _, d := range p.Dependencies {
		match := paketoURI.FindStringSubmatch(d.URI)
		if match == nil {
			t.Errorf("unexpected dependency URI %q", d.URI)
			continue
		}
		packaged["paketo-buildpacks/"+match[1]] = match[2]
	}

	used := map[string]bool{}
	for i, o := range m.Order {
		var hasEngine bool
		for _, g := range o.Group {
			if strings.HasPrefix(g.ID, "fckfips/") {
				hasEngine = hasEngine || g.ID == "fckfips/node-engine"
				if g.Version != "{{VERSION}}" {
					t.Errorf("order %d: %s must use the {{VERSION}} placeholder, has %q", i, g.ID, g.Version)
				}
				if g.ID == "fckfips/openssl-fips" && !g.Optional {
					t.Errorf("order %d: openssl-fips must be optional", i)
				}
				continue
			}
			used[g.ID] = true
			if v, ok := packaged[g.ID]; !ok {
				t.Errorf("order %d: %s is not in package.toml", i, g.ID)
			} else if v != g.Version {
				t.Errorf("order %d: %s is %s in buildpack.toml but %s in package.toml", i, g.ID, g.Version, v)
			}
		}
		if !hasEngine {
			t.Errorf("order %d has no fckfips/node-engine", i)
		}
	}
	for id := range packaged {
		if !used[id] {
			t.Errorf("%s is packaged but no order group uses it", id)
		}
	}
}
