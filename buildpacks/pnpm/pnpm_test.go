package pnpm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/scribe"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/npm"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func requirements(t *testing.T, dir string) map[string]map[string]any {
	t.Helper()
	result, err := Detect()(packit.DetectContext{WorkingDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plan.Provides) != 1 || result.Plan.Provides[0].Name != PlanPNPM {
		t.Fatalf("provides = %+v", result.Plan.Provides)
	}
	out := map[string]map[string]any{}
	for _, r := range result.Plan.Requires {
		md := r.Metadata.(map[string]any)
		out[md["version-source"].(string)] = md
	}
	return out
}

// Ported from pnpm-cli detect_test.go, adapted: detection no longer resolves
// versions (that needs the registry), it records what the app asks for.
func TestDetect(t *testing.T) {
	t.Run("no configuration requires no particular version", func(t *testing.T) {
		if reqs := requirements(t, t.TempDir()); len(reqs) != 0 {
			t.Errorf("requirements = %v", reqs)
		}
	})

	t.Run("BP_PNPM_VERSION", func(t *testing.T) {
		t.Setenv("BP_PNPM_VERSION", "^9.1.0")
		if got := requirements(t, t.TempDir())["BP_PNPM_VERSION"]["version"]; got != "^9.1.0" {
			t.Errorf("version = %v", got)
		}
	})

	t.Run("packageManager in package.json", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "package.json"), `{"packageManager": "pnpm@9.15.9+sha512.abcdef"}`)
		md := requirements(t, dir)["package.json"]
		want := map[string]any{"version": "9.15.9", "version-source": "package.json", "package-manager": "9.15.9", "package-manager-hash": "sha512.abcdef"}
		if !reflect.DeepEqual(md, want) {
			t.Errorf("metadata = %v", md)
		}
	})

	t.Run("another package manager is ignored", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "package.json"), `{"packageManager": "yarn@4.1.0"}`)
		if reqs := requirements(t, dir); len(reqs) != 0 {
			t.Errorf("requirements = %v", reqs)
		}
	})

	t.Run("lockfile versions", func(t *testing.T) {
		for lock, want := range map[string]string{"5.4": "7", "'6.0'": "8", "'9.0'": ""} {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "pnpm-lock.yaml"), "lockfileVersion: "+lock+"\n\nimporters: {}\n")
			got, _ := requirements(t, dir)["pnpm-lock.yaml"]["version"].(string)
			if got != want {
				t.Errorf("lockfile %s -> %q, want %q", lock, got, want)
			}
		}
	})

	t.Run("project path", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "web", "package.json"), `{"packageManager": "pnpm@10.1.0"}`)
		t.Setenv("BP_NODE_PROJECT_PATH", "web")
		if _, ok := requirements(t, dir)["package.json"]; !ok {
			t.Error("package.json under BP_NODE_PROJECT_PATH not read")
		}
	})

	t.Run("malformed package.json", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "package.json"), `{`)
		if _, err := Detect()(packit.DetectContext{WorkingDir: dir}); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestRequestedPriority(t *testing.T) {
	entry := func(v, src string) packit.BuildpackPlanEntry {
		return packit.BuildpackPlanEntry{Name: PlanPNPM, Metadata: map[string]any{"version": v, "version-source": src}}
	}
	entries := []packit.BuildpackPlanEntry{
		{Name: PlanPNPM, Metadata: map[string]any{"build": true}},
		entry("7", "pnpm-lock.yaml"),
		entry("9.15.9", "package.json"),
	}
	if spec, src := requested(entries); spec != "9.15.9" || src != "package.json" {
		t.Errorf("got %s from %s", spec, src)
	}
	if spec, _ := requested(append(entries, entry("10", "BP_PNPM_VERSION"))); spec != "10" {
		t.Errorf("BP_PNPM_VERSION should win, got %s", spec)
	}
}

type fakeRegistry struct {
	packument npm.Packument
	installed []string
	digests   [][]npm.Digest
}

func (f *fakeRegistry) Packument(context.Context, string) (npm.Packument, error) {
	return f.packument, nil
}

func (f *fakeRegistry) Install(_ context.Context, m npm.Manifest, dest string, extra ...npm.Digest) error {
	f.installed = append(f.installed, m.Version)
	f.digests = append(f.digests, extra)
	p := filepath.Join(dest, "bin", "pnpm.cjs")
	os.MkdirAll(filepath.Dir(p), 0o755)
	return os.WriteFile(p, []byte("#!/usr/bin/env node\n"), 0o644)
}

func manifest(v, nodeRange string) npm.Manifest {
	return npm.Manifest{
		Name:    "pnpm",
		Version: v,
		Bin:     json.RawMessage(`{"pnpm":"bin/pnpm.cjs"}`),
		Engines: json.RawMessage(`{"node":"` + nodeRange + `"}`),
		Dist:    npm.Dist{Integrity: "sha512-" + v},
	}
}

func setup(t *testing.T, entries ...packit.BuildpackPlanEntry) (packit.BuildContext, *fakeRegistry) {
	t.Helper()
	cnb := t.TempDir()
	toml, _ := os.ReadFile("buildpack.toml")
	os.WriteFile(filepath.Join(cnb, "buildpack.toml"), toml, 0o644)
	reg := &fakeRegistry{packument: npm.Packument{
		Name:     "pnpm",
		DistTags: map[string]string{"latest": "11.2.0"},
		Versions: map[string]npm.Manifest{
			"9.15.9": manifest("9.15.9", ">=18.12"),
			"10.2.0": manifest("10.2.0", ">=18.12"),
			"11.2.0": manifest("11.2.0", ">=22.13"),
		},
	}}
	base := packit.BuildpackPlanEntry{Name: PlanPNPM, Metadata: map[string]any{"build": true}}
	return packit.BuildContext{
		CNBPath:       cnb,
		Layers:        packit.Layers{Path: t.TempDir()},
		BuildpackInfo: packit.BuildpackInfo{Name: "pnpm", Version: "1.0.0", SBOMFormats: []string{"application/vnd.cyclonedx+json"}},
		Plan:          packit.BuildpackPlan{Entries: append([]packit.BuildpackPlanEntry{base}, entries...)},
	}, reg
}

func node(v string) NodeVersion { return func() (string, error) { return v, nil } }

func TestBuildDefaultVersion(t *testing.T) {
	ctx, reg := setup(t)
	result, err := Build(reg, node("v24.1.0"), chronos.DefaultClock, scribe.NewEmitter(&bytes.Buffer{}))(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reg.installed[0] != "10.2.0" {
		t.Errorf("installed %v, want the buildpack default (pnpm 10)", reg.installed)
	}
	layer := result.Layers[0]
	if !layer.Build || layer.Launch || !layer.Cache {
		t.Errorf("flags build=%t launch=%t cache=%t", layer.Build, layer.Launch, layer.Cache)
	}
	if target, err := os.Readlink(filepath.Join(layer.Path, "bin", "pnpm")); err != nil || target != "../lib/node_modules/pnpm/bin/pnpm.cjs" {
		t.Errorf("bin/pnpm -> %q, %v", target, err)
	}
	if layer.BuildEnv[MismatchEnv+".override"] != "false" {
		t.Errorf("build env = %v", layer.BuildEnv)
	}
}

func TestBuildPackageManagerHashIsEnforced(t *testing.T) {
	ctx, reg := setup(t, packit.BuildpackPlanEntry{Name: PlanPNPM, Metadata: map[string]any{
		"version": "9.15.9", "version-source": "package.json", "package-manager": "9.15.9", "package-manager-hash": "sha512.abcd",
	}})
	if _, err := Build(reg, node("v22.0.0"), chronos.DefaultClock, scribe.NewEmitter(&bytes.Buffer{}))(ctx); err != nil {
		t.Fatal(err)
	}
	if len(reg.digests[0]) != 1 || reg.digests[0][0].Algorithm != "sha512" {
		t.Errorf("packageManager hash not passed to the download: %v", reg.digests)
	}
}

func TestBuildOverrideSignalsMismatch(t *testing.T) {
	ctx, reg := setup(t,
		packit.BuildpackPlanEntry{Name: PlanPNPM, Metadata: map[string]any{"version": "9.15.9", "version-source": "package.json", "package-manager": "9.15.9", "package-manager-hash": "sha512.abcd"}},
		packit.BuildpackPlanEntry{Name: PlanPNPM, Metadata: map[string]any{"version": "10", "version-source": "BP_PNPM_VERSION"}},
	)
	result, err := Build(reg, node("v22.0.0"), chronos.DefaultClock, scribe.NewEmitter(&bytes.Buffer{}))(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reg.installed[0] != "10.2.0" {
		t.Errorf("installed %v", reg.installed)
	}
	if len(reg.digests[0]) != 0 {
		t.Error("the pinned hash belongs to 9.15.9 and must not be applied to 10.2.0")
	}
	if result.Layers[0].BuildEnv[MismatchEnv+".override"] != "true" {
		t.Errorf("mismatch not signalled: %v", result.Layers[0].BuildEnv)
	}
}

func TestBuildChecksNodeEngine(t *testing.T) {
	ctx, reg := setup(t, packit.BuildpackPlanEntry{Name: PlanPNPM, Metadata: map[string]any{"version": "11", "version-source": "BP_PNPM_VERSION"}})
	_, err := Build(reg, node("v20.11.0"), chronos.DefaultClock, scribe.NewEmitter(&bytes.Buffer{}))(ctx)
	if err == nil || !strings.Contains(err.Error(), "requires Node.js >=22.13") {
		t.Errorf("err = %v", err)
	}

	// Without a node on PATH the check is skipped, not failed.
	ctx, reg = setup(t, packit.BuildpackPlanEntry{Name: PlanPNPM, Metadata: map[string]any{"version": "11", "version-source": "BP_PNPM_VERSION"}})
	if _, err := Build(reg, func() (string, error) { return "", nil }, chronos.DefaultClock, scribe.NewEmitter(&bytes.Buffer{}))(ctx); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestBuildUnknownVersion(t *testing.T) {
	ctx, reg := setup(t, packit.BuildpackPlanEntry{Name: PlanPNPM, Metadata: map[string]any{"version": "8.15.0", "version-source": "package.json"}})
	_, err := Build(reg, node("v22.0.0"), chronos.DefaultClock, scribe.NewEmitter(&bytes.Buffer{}))(ctx)
	if err == nil {
		t.Fatal("expected an error for an unpublished version")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("unexpected error type: %v", err)
	}
}

func TestBuildLaunchFlag(t *testing.T) {
	ctx, reg := setup(t, packit.BuildpackPlanEntry{Name: PlanPNPM, Metadata: map[string]any{"launch": true}})
	result, err := Build(reg, node("v22.0.0"), chronos.DefaultClock, scribe.NewEmitter(&bytes.Buffer{}))(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Layers[0].Launch {
		t.Error("pnpm layer should be in the launch image when requested")
	}
}

func TestParsePackageManager(t *testing.T) {
	tests := []struct {
		in   string
		want PackageManager
	}{
		{"pnpm@10.1.0", PackageManager{Version: "10.1.0"}},
		{"  pnpm@9.0.0+sha512.ab\n", PackageManager{Version: "9.0.0", Hash: "sha512.ab"}}, // whitespace is trimmed
		{"yarn@4.0.0", PackageManager{}},
		{"", PackageManager{}},
	}
	for _, tt := range tests {
		if got := ParsePackageManager(tt.in); got != tt.want {
			t.Errorf("ParsePackageManager(%q) = %+v", tt.in, got)
		}
	}
}
