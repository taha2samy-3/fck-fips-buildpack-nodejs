package pnpminstall

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/pexec"
	"github.com/paketo-buildpacks/packit/v2/sbom"
	"github.com/paketo-buildpacks/packit/v2/scribe"
	"github.com/paketo-buildpacks/packit/v2/servicebindings"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func app(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"name":"app","engines":{"node":"22"},"dependencies":{"ms":"2.1.3"}}`)
	writeFile(t, filepath.Join(dir, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n")
	return dir
}

// --- Detect (ported from yarn-install's detect_test.go) ---------------------

func TestDetect(t *testing.T) {
	result, err := Detect()(packit.DetectContext{WorkingDir: app(t)})
	if err != nil {
		t.Fatal(err)
	}
	want := packit.BuildPlan{
		Provides: []packit.BuildPlanProvision{{Name: PlanNodeModules}},
		Requires: []packit.BuildPlanRequirement{
			{Name: PlanNode, Metadata: map[string]any{"build": true, "version": "22", "version-source": "package.json"}},
			{Name: PlanPNPM, Metadata: map[string]any{"build": true, "launch": false}},
		},
	}
	if !reflect.DeepEqual(result.Plan, want) {
		t.Errorf("plan = %+v", result.Plan)
	}

	t.Run("pnpm at launch on request", func(t *testing.T) {
		t.Setenv("BP_PNPM_IN_LAUNCH", "true")
		result, _ := Detect()(packit.DetectContext{WorkingDir: app(t)})
		if result.Plan.Requires[1].Metadata.(map[string]any)["launch"] != true {
			t.Error("BP_PNPM_IN_LAUNCH=true should request pnpm at launch")
		}
	})

	t.Run("without engines.node", func(t *testing.T) {
		dir := app(t)
		writeFile(t, filepath.Join(dir, "package.json"), `{}`)
		result, _ := Detect()(packit.DetectContext{WorkingDir: dir})
		if md := result.Plan.Requires[0].Metadata.(map[string]any); !reflect.DeepEqual(md, map[string]any{"build": true}) {
			t.Errorf("node metadata = %v", md)
		}
	})

	t.Run("no pnpm-lock.yaml", func(t *testing.T) {
		dir := app(t)
		os.Remove(filepath.Join(dir, "pnpm-lock.yaml"))
		if _, err := Detect()(packit.DetectContext{WorkingDir: dir}); !isDetectFail(err) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("no package.json", func(t *testing.T) {
		dir := app(t)
		os.Remove(filepath.Join(dir, "package.json"))
		if _, err := Detect()(packit.DetectContext{WorkingDir: dir}); !isDetectFail(err) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("project path cannot be found", func(t *testing.T) {
		t.Setenv("BP_NODE_PROJECT_PATH", "nope")
		if _, err := Detect()(packit.DetectContext{WorkingDir: app(t)}); err == nil || isDetectFail(err) {
			t.Errorf("err = %v", err)
		}
	})
}

// isDetectFail reports a clean "does not apply" result: packit.Fail's type
// is internal, so compare types rather than values.
func isDetectFail(err error) bool {
	return err != nil && reflect.TypeOf(err) == reflect.TypeOf(packit.Fail)
}

// --- ConfiguresBuilds ---------------------------------------------------------

func TestConfiguresBuilds(t *testing.T) {
	tests := []struct {
		name, pkg, workspace string
		want                 bool
	}{
		{"nothing", `{}`, "", false},
		{"package.json onlyBuiltDependencies", `{"pnpm":{"onlyBuiltDependencies":["esbuild"]}}`, "", true},
		{"workspace allowBuilds (pnpm 11)", `{}`, "packages:\n  - 'apps/*'\nallowBuilds:\n  esbuild: true\n", true},
		{"nested key does not count", `{}`, "catalog:\n  onlyBuiltDependencies: x\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "package.json"), tt.pkg)
			if tt.workspace != "" {
				writeFile(t, filepath.Join(dir, "pnpm-workspace.yaml"), tt.workspace)
			}
			if got, err := ConfiguresBuilds(dir); err != nil || got != tt.want {
				t.Errorf("got %t, %v", got, err)
			}
		})
	}
}

// --- Installer (ported from install_process_test.go) --------------------------

type fakeExec struct {
	version  string
	storeDir string
	config   string
	calls    []pexec.Execution
	fail     map[string]error
}

func (f *fakeExec) Execute(e pexec.Execution) error {
	f.calls = append(f.calls, e)
	cmd := strings.Join(e.Args, " ")
	if err := f.fail[e.Args[0]]; err != nil {
		return err
	}
	out := func(s string) {
		if e.Stdout != nil {
			io.WriteString(e.Stdout, s)
		}
	}
	switch {
	case cmd == "--version":
		out(f.version + "\n")
	case cmd == "config get store-dir":
		out(f.storeDir + "\n")
	case cmd == "config list":
		out(f.config)
	case e.Args[0] == "install":
		os.MkdirAll(filepath.Join(e.Dir, "node_modules", "ms"), 0o755)
		os.WriteFile(filepath.Join(e.Dir, "node_modules", "ms", "index.js"), []byte("module.exports=1"), 0o644)
	}
	return nil
}

func (f *fakeExec) installs() []pexec.Execution {
	var out []pexec.Execution
	for _, c := range f.calls {
		if c.Args[0] == "install" {
			out = append(out, c)
		}
	}
	return out
}

func newInstaller(pnpm *fakeExec, relax bool) Installer {
	return NewInstaller(pnpm, &fakeExec{version: "v24.1.0"}, scribe.NewEmitter(io.Discard), relax)
}

func TestInstallArguments(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		prod     bool
		allow    bool
		offline  bool
		want     []string
		unwanted []string
	}{
		{"pnpm 10 dev install", "10.2.0", false, true, false,
			[]string{"install", "--frozen-lockfile", "--store-dir", "--prefer-offline", "--dangerously-allow-all-builds"}, []string{"--prod", "--offline"}},
		{"pnpm 9 never gets the pnpm 10 flag", "9.15.9", true, true, false,
			[]string{"--prod"}, []string{"--dangerously-allow-all-builds"}},
		{"strict build scripts", "10.2.0", false, false, false,
			nil, []string{"--dangerously-allow-all-builds"}},
		{"configured offline store", "10.2.0", false, false, true,
			[]string{"--offline"}, []string{"--store-dir", "--prefer-offline"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, layer := app(t), t.TempDir()
			pnpm := &fakeExec{version: tt.version}
			err := newInstaller(pnpm, false).Install(dir, layer, tt.version, tt.prod, Options{StoreDir: "/store", AllowBuilds: tt.allow}, tt.offline)
			if err != nil {
				t.Fatal(err)
			}
			args := pnpm.installs()[0].Args
			for _, w := range tt.want {
				if !slices.Contains(args, w) {
					t.Errorf("args %v lack %s", args, w)
				}
			}
			for _, u := range tt.unwanted {
				if slices.Contains(args, u) {
					t.Errorf("args %v contain %s", args, u)
				}
			}
			env := pnpm.installs()[0].Env
			if !slices.Contains(env, "CI=true") {
				t.Error("install must run non-interactively (CI=true)")
			}
			if slices.Contains(env, "NODE_ENV=development") == tt.prod {
				t.Errorf("NODE_ENV=development present = %t for prod = %t", !tt.prod, tt.prod)
			}

			// node_modules moved into the layer.
			if _, err := os.Stat(filepath.Join(layer, "node_modules", "ms", "index.js")); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestInstallerRelaxesVersionCheckEverywhere(t *testing.T) {
	pnpm := &fakeExec{version: "11.2.0", storeDir: "undefined"}
	i := newInstaller(pnpm, true)
	dir := app(t)
	if _, _, err := i.Versions(dir); err != nil {
		t.Fatal(err)
	}
	if err := i.Install(dir, t.TempDir(), "11.2.0", false, Options{StoreDir: "/s"}, false); err != nil {
		t.Fatal(err)
	}
	for _, c := range pnpm.calls {
		for _, want := range []string{"pnpm_config_pm_on_fail=warn", "npm_config_package_manager_strict=false"} {
			if !slices.Contains(c.Env, want) {
				t.Errorf("pnpm %v ran without %s", c.Args, want)
			}
		}
	}

	strict := &fakeExec{version: "10.2.0"}
	newInstaller(strict, false).Versions(dir)
	if slices.Contains(strict.calls[0].Env, "pnpm_config_pm_on_fail=warn") {
		t.Error("the check must stay on when versions match")
	}
}

func TestConfiguredStore(t *testing.T) {
	dir := app(t)
	if path, ok, err := newInstaller(&fakeExec{storeDir: "undefined"}, false).ConfiguredStore(dir); err != nil || ok || path != "" {
		t.Errorf("unset store: %q %t %v", path, ok, err)
	}
	store := t.TempDir()
	if path, ok, _ := newInstaller(&fakeExec{storeDir: store}, false).ConfiguredStore(dir); !ok || path != store {
		t.Errorf("existing store: %q %t", path, ok)
	}
	if _, ok, _ := newInstaller(&fakeExec{storeDir: "relative/missing"}, false).ConfiguredStore(dir); ok {
		t.Error("a missing store is not an offline store")
	}
}

func TestCacheKey(t *testing.T) {
	dir := app(t)
	i := newInstaller(&fakeExec{config: "registry=https://registry.npmjs.org/"}, false)
	key := func(extra ...string) string {
		k, err := i.CacheKey(dir, extra...)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	base := key("node=v24.1.0")
	if key("node=v24.1.0") != base {
		t.Error("key is not stable")
	}
	if key("node=v22.0.0") == base {
		t.Error("node version must change the key (native addons)")
	}
	for _, f := range []string{"pnpm-lock.yaml", "pnpm-workspace.yaml", ".npmrc", ".pnpmfile.cjs"} {
		writeFile(t, filepath.Join(dir, f), "changed "+f)
		if k := key("node=v24.1.0"); k == base {
			t.Errorf("changing %s did not change the key", f)
		} else {
			base = k
		}
	}
	other := newInstaller(&fakeExec{config: "registry=https://mirror/"}, false)
	if k, _ := other.CacheKey(dir, "node=v24.1.0"); k == base {
		t.Error("pnpm config must change the key")
	}
}

func TestMajorOf(t *testing.T) {
	for in, want := range map[string]int{"10.2.0": 10, "v9.1.0\n": 9, "11": 11, "": 0, "x": 0} {
		if got := majorOf(in); got != want {
			t.Errorf("majorOf(%q) = %d", in, got)
		}
	}
}

// --- Build (ported from build_test.go) ------------------------------------------

type fakeBindings struct {
	bindings map[string][]servicebindings.Binding
	err      error
}

func (f fakeBindings) Resolve(typ, _, _ string) ([]servicebindings.Binding, error) {
	return f.bindings[typ], f.err
}

type fakeSBOM struct{ calls int }

func (f *fakeSBOM) Generate(string) (sbom.SBOM, error) {
	f.calls++
	return sbom.SBOM{}, nil
}

type buildHarness struct {
	ctx      packit.BuildContext
	pnpm     *fakeExec
	bindings fakeBindings
	sbom     *fakeSBOM
	paths    Paths
}

func newBuild(t *testing.T, build, launch bool) *buildHarness {
	t.Helper()
	return &buildHarness{
		ctx: packit.BuildContext{
			WorkingDir:    app(t),
			CNBPath:       "/cnb",
			Layers:        packit.Layers{Path: t.TempDir()},
			Platform:      packit.Platform{Path: t.TempDir()},
			BuildpackInfo: packit.BuildpackInfo{Name: "pnpm-install", Version: "1.0.0", SBOMFormats: []string{"application/vnd.cyclonedx+json"}},
			Plan: packit.BuildpackPlan{Entries: []packit.BuildpackPlanEntry{
				{Name: PlanNodeModules, Metadata: map[string]any{"build": build, "launch": launch}},
			}},
		},
		pnpm:  &fakeExec{version: "10.2.0", storeDir: "undefined", config: "{}"},
		sbom:  &fakeSBOM{},
		paths: Paths{Home: t.TempDir(), Tmp: t.TempDir()},
	}
}

func (h *buildHarness) run() (packit.BuildResult, error) {
	return Build(h.bindings, newInstaller(h.pnpm, false), h.sbom, h.paths, chronos.DefaultClock, scribe.NewEmitter(io.Discard))(h.ctx)
}

func layerNamed(r packit.BuildResult, name string) (packit.Layer, bool) {
	for _, l := range r.Layers {
		if l.Name == name {
			return l, true
		}
	}
	return packit.Layer{}, false
}

func TestBuildBuildAndLaunch(t *testing.T) {
	h := newBuild(t, true, true)
	result, err := h.run()
	if err != nil {
		t.Fatal(err)
	}

	store, _ := layerNamed(result, LayerStore)
	if !store.Cache || store.Build || store.Launch {
		t.Errorf("store layer flags: %+v", store)
	}
	build, ok := layerNamed(result, LayerBuildModules)
	if !ok || !build.Build || build.Launch || build.BuildEnv["NODE_ENV.override"] != "development" {
		t.Errorf("build-modules: %+v", build)
	}
	launch, ok := layerNamed(result, LayerLaunchModules)
	if !ok || !launch.Launch || launch.Build || filepath.Base(launch.ExecD[0]) != "setup-symlinks" {
		t.Errorf("launch-modules: %+v", launch)
	}

	installs := h.pnpm.installs()
	if len(installs) != 2 || slices.Contains(installs[0].Args, "--prod") || !slices.Contains(installs[1].Args, "--prod") {
		t.Errorf("want a dev install then a prod install, got %v", installs)
	}
	if !slices.Contains(installs[0].Args, filepath.Join(store.Path, "store")) {
		t.Errorf("store not in the cache layer: %v", installs[0].Args)
	}
	if h.sbom.calls != 1 {
		t.Errorf("SBOM generated %d times, want once", h.sbom.calls)
	}

	// The project's node_modules resolves to the launch layer through tmp.
	link, _ := os.Readlink(filepath.Join(h.ctx.WorkingDir, "node_modules"))
	if link != filepath.Join(h.paths.Tmp, "node_modules") {
		t.Errorf("node_modules -> %s", link)
	}
	if target, _ := os.Readlink(link); target != filepath.Join(launch.Path, "node_modules") {
		t.Errorf("tmp link -> %s", target)
	}
}

func TestBuildReusesLayer(t *testing.T) {
	h := newBuild(t, false, true)
	result, err := h.run()
	if err != nil {
		t.Fatal(err)
	}
	launch, _ := layerNamed(result, LayerLaunchModules)
	writeFile(t, launch.Path+".toml", fmt.Sprintf("[metadata]\ncache_sha = %q\n", launch.Metadata["cache_sha"]))

	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if n := len(h.pnpm.installs()); n != 1 {
		t.Errorf("pnpm install ran %d times, want 1 (second build reuses the layer)", n)
	}

	writeFile(t, filepath.Join(h.ctx.WorkingDir, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n# changed\n")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if n := len(h.pnpm.installs()); n != 2 {
		t.Errorf("a changed lockfile must reinstall; installs = %d", n)
	}
}

func TestBuildFailures(t *testing.T) {
	t.Run("not required", func(t *testing.T) {
		h := newBuild(t, false, false)
		if _, err := h.run(); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("pnpm missing", func(t *testing.T) {
		h := newBuild(t, true, false)
		h.pnpm.fail = map[string]error{"--version": errors.New("exec: pnpm: not found")}
		if _, err := h.run(); err == nil || !strings.Contains(err.Error(), "pnpm --version") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("install fails", func(t *testing.T) {
		h := newBuild(t, true, false)
		h.pnpm.fail = map[string]error{"install": errors.New("exit status 1")}
		if _, err := h.run(); err == nil || !strings.Contains(err.Error(), "pnpm install failed") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("invalid BP_DISABLE_SBOM", func(t *testing.T) {
		t.Setenv("BP_DISABLE_SBOM", "maybe")
		if _, err := newBuild(t, true, false).run(); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("binding resolver fails", func(t *testing.T) {
		h := newBuild(t, true, false)
		h.bindings.err = errors.New("boom")
		if _, err := h.run(); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestBuildSBOMDisabled(t *testing.T) {
	t.Setenv("BP_DISABLE_SBOM", "true")
	h := newBuild(t, true, false)
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if h.sbom.calls != 0 {
		t.Error("SBOM generated although disabled")
	}
}

// --- Bindings (ported from package_manager_configuration_manager_test.go) ------

func binding(t *testing.T, typ, entry string) servicebindings.Binding {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, entry), "//registry/:_authToken=secret")
	return servicebindings.Binding{Name: typ, Type: typ, Path: dir, Entries: map[string]*servicebindings.Entry{entry: servicebindings.NewEntry(filepath.Join(dir, entry))}}
}

func TestLinkConfig(t *testing.T) {
	t.Run("pnpmrc alone is linked as .npmrc too", func(t *testing.T) {
		home := t.TempDir()
		b := binding(t, "pnpmrc", ".pnpmrc")
		cleanup, err := linkConfig(fakeBindings{bindings: map[string][]servicebindings.Binding{"pnpmrc": {b}}}, "", home)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{".pnpmrc", ".npmrc"} {
			if target, _ := os.Readlink(filepath.Join(home, name)); target != filepath.Join(b.Path, ".pnpmrc") {
				t.Errorf("%s -> %q", name, target)
			}
		}
		if err := cleanup(); err != nil {
			t.Fatal(err)
		}
		if entries, _ := os.ReadDir(home); len(entries) != 0 {
			t.Errorf("links left behind: %v", entries)
		}
	})

	t.Run("npmrc takes precedence", func(t *testing.T) {
		home := t.TempDir()
		npmrc, pnpmrc := binding(t, "npmrc", ".npmrc"), binding(t, "pnpmrc", ".pnpmrc")
		cleanup, err := linkConfig(fakeBindings{bindings: map[string][]servicebindings.Binding{"npmrc": {npmrc}, "pnpmrc": {pnpmrc}}}, "", home)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		if target, _ := os.Readlink(filepath.Join(home, ".npmrc")); target != filepath.Join(npmrc.Path, ".npmrc") {
			t.Errorf(".npmrc -> %q", target)
		}
	})

	t.Run("errors", func(t *testing.T) {
		two := fakeBindings{bindings: map[string][]servicebindings.Binding{"npmrc": {binding(t, "npmrc", ".npmrc"), binding(t, "npmrc", ".npmrc")}}}
		if _, err := linkConfig(two, "", t.TempDir()); err == nil {
			t.Error("two bindings of one type must fail")
		}
		wrong := fakeBindings{bindings: map[string][]servicebindings.Binding{"npmrc": {binding(t, "npmrc", "other")}}}
		if _, err := linkConfig(wrong, "", t.TempDir()); err == nil {
			t.Error("a binding without the entry must fail")
		}
		home := t.TempDir()
		writeFile(t, filepath.Join(home, ".npmrc"), "user's own")
		existing := fakeBindings{bindings: map[string][]servicebindings.Binding{"npmrc": {binding(t, "npmrc", ".npmrc")}}}
		if _, err := linkConfig(existing, "", home); err == nil {
			t.Error("an existing file must not be overwritten")
		}
		if b, _ := os.ReadFile(filepath.Join(home, ".npmrc")); !bytes.Equal(b, []byte("user's own")) {
			t.Error("existing .npmrc was modified")
		}
	})
}
