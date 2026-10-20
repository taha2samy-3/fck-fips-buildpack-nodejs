package nodeengine

import (
	"strings"
	"testing"

	"github.com/paketo-buildpacks/packit/v2"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/apk"
)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{"", "", false},
		{"  \n", "", false},
		{"22", "22", false},
		{"v22.11.0\n", "22.11.0", false},
		{"^20.10", "^20.10", false},
		{">=18 <21", ">=18 <21", false},
		{"20.x || 22.x", "20.x || 22.x", false},
		{"lts/iron", "20", false},
		{"lts/Jod", "22", false},
		{"lts/*", newestLTS, false},
		{"node", "*", false},
		{"latest", "*", false},
		{"# pinned for CI\n24 # current LTS\n", "24", false},
		{"lts/unknown", "", true},
		{"banana", "", true},
	}
	for _, tt := range tests {
		got, err := NormalizeVersion(tt.in)
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("NormalizeVersion(%q) = %q, %v; want %q, error %t", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestRequestedVersion(t *testing.T) {
	entry := func(version, source string) packit.BuildpackPlanEntry {
		return packit.BuildpackPlanEntry{Name: PlanNode, Metadata: map[string]any{"version": version, "version-source": source}}
	}
	entries := []packit.BuildpackPlanEntry{
		{Name: PlanNode, Metadata: map[string]any{"launch": true}},
		entry("20", ".node-version"),
		entry("18", "some-other-buildpack"),
		entry("22", "package.json"),
		entry("21", ".nvmrc"),
		{Name: "npm", Metadata: map[string]any{"version": "99", "version-source": "BP_NODE_VERSION"}},
	}
	if v, src := requestedVersion(entries); v != "22" || src != "package.json" {
		t.Errorf("got %q from %q, want 22 from package.json", v, src)
	}
	entries = append(entries, entry("24", "BP_NODE_VERSION"))
	if v, _ := requestedVersion(entries); v != "24" {
		t.Errorf("BP_NODE_VERSION should win, got %q", v)
	}
	if v, _ := requestedVersion(nil); v != "" {
		t.Errorf("no entries should give no version, got %q", v)
	}
}

func testIndex() *apk.Index {
	return apk.NewIndex([]apk.Package{
		{Name: "nodejs-22", Version: "22.9.0-r0"},
		{Name: "nodejs-22", Version: "22.10.0-r0"}, // numerically newer than 22.9
		{Name: "nodejs-22", Version: "22.10.0-r3"},
		{Name: "nodejs-24", Version: "24.1.0-r0"},
		{Name: "nodejs-24-minimal", Version: "24.9.0-r0"},
		{Name: "nodejs-26", Version: "26.0.0-r0"},
	})
}

func TestSelectNode(t *testing.T) {
	tests := []struct{ constraint, want string }{
		{"22", "nodejs-22-22.10.0-r3"},
		{"22.9", "nodejs-22-22.9.0-r0"},
		{"^22.9.0", "nodejs-22-22.10.0-r3"},
		{"24", "nodejs-24-24.1.0-r0"},
		{"*", "nodejs-26-26.0.0-r0"},
		{">=22 <26", "nodejs-24-24.1.0-r0"},
	}
	for _, tt := range tests {
		p, err := SelectNode(testIndex(), tt.constraint)
		if err != nil || p.String() != tt.want {
			t.Errorf("SelectNode(%q) = %s, %v; want %s", tt.constraint, p, err, tt.want)
		}
	}
	if _, err := SelectNode(testIndex(), "18"); err == nil {
		t.Error("expected an error for a missing major")
	} else if !strings.Contains(err.Error(), "22, 24, 26") {
		t.Errorf("error should list available majors: %v", err)
	}
}
