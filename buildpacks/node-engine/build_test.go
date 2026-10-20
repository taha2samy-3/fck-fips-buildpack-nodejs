package nodeengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/scribe"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/apk"
	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/npm"
)

type fakeSources struct {
	index        *apk.Index
	installed    [][]apk.Package
	npmInstalled []string
	bundled      string
	bundledErr   error
}

func (f *fakeSources) WolfiIndex(context.Context, string) (*apk.Index, bool, error) {
	return f.index, false, nil
}

func (f *fakeSources) InstallAPKs(_ context.Context, _ string, pkgs []apk.Package, dest string) error {
	f.installed = append(f.installed, pkgs)
	write := func(rel, body string) error {
		p := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(body), 0o755)
	}
	if err := write("bin/node", "elf"); err != nil {
		return err
	}
	return write("share/icu/78.3/icudt78l.dat", "icu")
}

func (f *fakeSources) Packument(context.Context, string) (npm.Packument, error) {
	return npm.Packument{
		Name:     "npm",
		DistTags: map[string]string{"latest": "11.6.0"},
		Versions: map[string]npm.Manifest{
			"10.9.2": {Name: "npm", Version: "10.9.2", Dist: dist("10.9.2"), Bin: json.RawMessage(`{"npm":"bin/npm-cli.js","npx":"bin/npx-cli.js"}`)},
			"11.6.0": {Name: "npm", Version: "11.6.0", Dist: dist("11.6.0"), Bin: json.RawMessage(`{"npm":"bin/npm-cli.js","npx":"bin/npx-cli.js"}`)},
		},
	}, nil
}

func dist(v string) npm.Dist { return npm.Dist{Integrity: "sha512-" + v} }

func (f *fakeSources) InstallNPM(_ context.Context, m npm.Manifest, dest string) error {
	f.npmInstalled = append(f.npmInstalled, m.Version)
	for _, bin := range []string{"npm-cli.js", "npx-cli.js"} {
		p := filepath.Join(dest, "bin", bin)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("#!/usr/bin/env node\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeSources) BundledNPM(context.Context, string) (string, error) {
	return f.bundled, f.bundledErr
}

// fakeNode answers the smoke test with nodeVersion and the FIPS check with
// fipsOutput, recording what it was asked to run.
type fakeNode struct {
	version    string
	fipsOutput string
	fipsErr    error
	calls      [][]string
}

func (f *fakeNode) run(path string, args, env []string) (string, error) {
	f.calls = append(f.calls, args)
	if slices.Contains(args, "--force-fips") {
		if !slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "OPENSSL_CONF=") }) {
			return "", errors.New("no OPENSSL_CONF")
		}
		return f.fipsOutput, f.fipsErr
	}
	if !slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "ICU_DATA=") }) {
		return "Internal error. Icu error.", errors.New("exit 1")
	}
	return f.version + "\n", nil
}

type harness struct {
	ctx     packit.BuildContext
	sources *fakeSources
	node    *fakeNode
	log     *bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cnb := t.TempDir()
	toml, err := os.ReadFile("buildpack.toml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cnb, "buildpack.toml"), toml, 0o644); err != nil {
		t.Fatal(err)
	}
	return &harness{
		ctx: packit.BuildContext{
			CNBPath:       cnb,
			WorkingDir:    t.TempDir(),
			Layers:        packit.Layers{Path: t.TempDir()},
			BuildpackInfo: packit.BuildpackInfo{Name: "node-engine", Version: "1.2.3", SBOMFormats: []string{"application/vnd.cyclonedx+json"}},
			TargetInfo:    packit.TargetInfo{OS: "linux", Arch: "amd64"},
			Plan: packit.BuildpackPlan{Entries: []packit.BuildpackPlanEntry{
				{Name: PlanNode, Metadata: map[string]any{"version": "22", "version-source": "package.json", "build": true, "launch": true}},
			}},
		},
		sources: &fakeSources{index: testIndex(), bundled: "10.9.2"},
		node:    &fakeNode{version: "v22.10.0"},
		log:     &bytes.Buffer{},
	}
}

func (h *harness) build() (packit.BuildResult, error) {
	factory := func(string, packit.BuildpackInfo) Sources { return h.sources }
	return Build(factory, h.node.run, chronos.DefaultClock, scribe.NewEmitter(h.log))(h.ctx)
}

func layer(result packit.BuildResult, name string) (packit.Layer, bool) {
	for _, l := range result.Layers {
		if l.Name == name {
			return l, true
		}
	}
	return packit.Layer{}, false
}

func TestBuildInstallsNodeAndNPM(t *testing.T) {
	h := newHarness(t)
	result, err := h.build()
	if err != nil {
		t.Fatalf("%v\n%s", err, h.log)
	}

	node, ok := layer(result, LayerNode)
	if !ok {
		t.Fatal("no node layer")
	}
	if !node.Build || !node.Launch || !node.Cache {
		t.Errorf("node layer flags: build=%t launch=%t cache=%t", node.Build, node.Launch, node.Cache)
	}
	if got := h.sources.installed[0][0].String(); got != "nodejs-22-22.10.0-r3" {
		t.Errorf("installed %s", got)
	}
	if node.SharedEnv["ICU_DATA.override"] != filepath.Join(node.Path, "share/icu/78.3") {
		t.Errorf("ICU_DATA = %q", node.SharedEnv["ICU_DATA.override"])
	}
	if node.LaunchEnv["NODE_ENV.default"] != "production" {
		t.Errorf("launch env = %v", node.LaunchEnv)
	}
	if _, ok := node.LaunchEnv["BPI_NODE_FORCE_FIPS.override"]; ok {
		t.Error("FIPS must be off unless requested")
	}
	if len(node.ExecD) != 1 || filepath.Base(node.ExecD[0]) != "node-options" {
		t.Errorf("exec.d = %v", node.ExecD)
	}
	if node.SBOM == nil {
		t.Error("node layer has no SBOM")
	}

	npmLayer, ok := layer(result, LayerNPM)
	if !ok {
		t.Fatal("no npm layer")
	}
	if !npmLayer.Build || npmLayer.Launch {
		t.Errorf("npm layer flags: build=%t launch=%t", npmLayer.Build, npmLayer.Launch)
	}
	if h.sources.npmInstalled[0] != "10.9.2" {
		t.Errorf("npm %v, want the bundled 10.9.2", h.sources.npmInstalled)
	}
	if target, err := os.Readlink(filepath.Join(npmLayer.Path, "bin", "npm")); err != nil || target != "../lib/node_modules/npm/bin/npm-cli.js" {
		t.Errorf("bin/npm -> %q (%v)", target, err)
	}
	if cache, ok := layer(result, LayerCache); !ok || !cache.Cache || cache.Build || cache.Launch {
		t.Errorf("cache layer: %+v", cache)
	}
}

func TestBuildReusesLayers(t *testing.T) {
	h := newHarness(t)
	result, err := h.build()
	if err != nil {
		t.Fatal(err)
	}
	// Persist metadata the way packit.Run does between builds.
	for _, l := range result.Layers {
		writeLayerTOML(t, l)
	}

	h.log.Reset()
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	if len(h.sources.installed) != 1 || len(h.sources.npmInstalled) != 1 {
		t.Errorf("second build reinstalled: apk %d, npm %d", len(h.sources.installed), len(h.sources.npmInstalled))
	}
	if !strings.Contains(h.log.String(), "Reusing cached layer") {
		t.Errorf("log does not mention reuse:\n%s", h.log)
	}
}

// writeLayerTOML stands in for packit.Run, which writes <layer>.toml after
// the BuildFunc returns.
func writeLayerTOML(t *testing.T, l packit.Layer) {
	t.Helper()
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(map[string]any{"metadata": l.Metadata}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.Path+".toml", b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildNPMVersionOverride(t *testing.T) {
	h := newHarness(t)
	t.Setenv("BP_NPM_VERSION", "11")
	h.sources.bundledErr = errors.New("must not be asked")
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	if h.sources.npmInstalled[0] != "11.6.0" {
		t.Errorf("npm %v, want 11.6.0", h.sources.npmInstalled)
	}
}

func TestBuildFailsWhenNodeDoesNotRun(t *testing.T) {
	h := newHarness(t)
	h.node.version = "v20.0.0"
	if _, err := h.build(); err == nil || !strings.Contains(err.Error(), "expected v22.10.0") {
		t.Errorf("err = %v", err)
	}
}

func TestBuildFIPS(t *testing.T) {
	h := newHarness(t)
	t.Setenv("BP_OPENSSL_FIPS", "true")

	t.Run("without the openssl-fips buildpack", func(t *testing.T) {
		if _, err := h.build(); err == nil || !strings.Contains(err.Error(), "openssl-fips buildpack") {
			t.Errorf("err = %v", err)
		}
	})

	t.Setenv("BPI_OPENSSL_FIPS_CONF", "/layers/fips/openssl.cnf")
	t.Setenv("BPI_OPENSSL_FIPS_MODULES", "/layers/fips/ossl-modules")

	t.Run("verified", func(t *testing.T) {
		h.node.fipsOutput = `{"fips":1,"openssl":"4.0.3"}`
		result, err := h.build()
		if err != nil {
			t.Fatalf("%v\n%s", err, h.log)
		}
		node, _ := layer(result, LayerNode)
		if node.LaunchEnv["BPI_NODE_FORCE_FIPS.override"] != "true" {
			t.Errorf("launch env = %v", node.LaunchEnv)
		}
		if !strings.Contains(h.log.String(), "crypto.getFips() = 1") {
			t.Errorf("log:\n%s", h.log)
		}
	})

	t.Run("check fails", func(t *testing.T) {
		h.node.fipsErr = errors.New("exit 1")
		h.node.fipsOutput = "Error: MD5 is available"
		if _, err := h.build(); err == nil || !strings.Contains(err.Error(), "FIPS mode") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestWithLibraryPath(t *testing.T) {
	got := withLibraryPath([]string{"A=1", "LD_LIBRARY_PATH=/x"}, "/layer/lib")
	if !slices.Equal(got, []string{"A=1", "LD_LIBRARY_PATH=/layer/lib:/x"}) {
		t.Errorf("got %v", got)
	}
	got = withLibraryPath([]string{"A=1"}, "/layer/lib")
	if !slices.Equal(got, []string{"A=1", "LD_LIBRARY_PATH=/layer/lib"}) {
		t.Errorf("got %v", got)
	}
}

func TestExcluded(t *testing.T) {
	for name, want := range map[string]bool{
		"openssl-provider-legacy-allowed": true,
		"openssl-config-fipshardened":     true,
		"wolfi-baselayout":                true,
		"libuv":                           false,
		"openssl-4.0-libcrypto":           false,
	} {
		if got := excluded(apk.Package{Name: name}); got != want {
			t.Errorf("excluded(%s) = %t", name, got)
		}
	}
	if !excluded(apk.Package{Name: "ld-linux-2.44", Origin: "glibc-2.44"}) {
		t.Error("glibc packages must be excluded")
	}
}
