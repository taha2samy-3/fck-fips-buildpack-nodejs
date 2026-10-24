package opensslfips

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/scribe"

	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/apk"
)

func TestDetect(t *testing.T) {
	if _, err := Detect()(packit.DetectContext{}); err == nil {
		t.Error("detect must fail unless BP_OPENSSL_FIPS=true")
	}

	t.Setenv("BP_OPENSSL_FIPS", "true")
	result, err := Detect()(packit.DetectContext{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan.Provides[0].Name != PlanOpenSSLFIPS || result.Plan.Requires[0].Name != PlanOpenSSLFIPS {
		t.Errorf("plan = %+v", result.Plan)
	}

	t.Setenv("BP_OPENSSL_FIPS", "on")
	if _, err := Detect()(packit.DetectContext{}); err == nil || strings.Contains(err.Error(), "not true") {
		t.Errorf("an invalid boolean must be an error, not a silent pass/fail: %v", err)
	}
}

func TestRenderConfig(t *testing.T) {
	raw, err := renderConfig("OpenSSL FIPS Provider 3.1.2", "/layers/x/etc/ssl/fipsmodule.cnf")
	if err != nil {
		t.Fatal(err)
	}
	cnf := string(raw)
	for _, want := range []string{
		"config_diagnostics = 1",
		"openssl_conf = openssl_init",
		"nodejs_conf = openssl_init",
		".include /layers/x/etc/ssl/fipsmodule.cnf",
		"fips = fips_sect",
		"base = base_sect",
		"default_properties = fips=yes",
		"MinProtocol = TLSv1.2",
		"Groups = P-256:P-384:P-521",
	} {
		if !strings.Contains(cnf, want) {
			t.Errorf("openssl.cnf lacks %q", want)
		}
	}
	if strings.Contains(cnf, "default = default_sect") || strings.Contains(cnf, "legacy") {
		t.Error("openssl.cnf must not activate the default or legacy providers")
	}
}

type fakeRepo struct {
	index    *apk.Index
	installs int
	noMAC    bool
}

func (f *fakeRepo) Index(context.Context, string) (*apk.Index, bool, error) {
	return f.index, false, nil
}

func (f *fakeRepo) Install(_ context.Context, _ string, _ []apk.Package, dest string) error {
	f.installs++
	os.MkdirAll(filepath.Join(dest, "lib", "ossl-modules"), 0o755)
	os.MkdirAll(filepath.Join(dest, "etc", "ssl"), 0o755)
	os.WriteFile(filepath.Join(dest, "lib", "ossl-modules", "fips.so"), []byte("elf"), 0o755)
	cnf := "[fips_sect]\nactivate = 1\nmodule-mac = AB:CD\n"
	if f.noMAC {
		cnf = "[fips_sect]\nactivate = 1\n"
	}
	return os.WriteFile(filepath.Join(dest, "etc", "ssl", "fipsmodule.cnf"), []byte(cnf), 0o644)
}

func buildContext(t *testing.T) packit.BuildContext {
	t.Helper()
	cnb := t.TempDir()
	toml, err := os.ReadFile("buildpack.toml")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(cnb, "buildpack.toml"), toml, 0o644)
	return packit.BuildContext{
		CNBPath:       cnb,
		Layers:        packit.Layers{Path: t.TempDir()},
		BuildpackInfo: packit.BuildpackInfo{Name: "openssl-fips", Version: "1.0.0", SBOMFormats: []string{"application/spdx+json"}},
		TargetInfo:    packit.TargetInfo{Arch: "arm64"},
	}
}

func TestBuild(t *testing.T) {
	provider := apk.Package{Name: "openssl-provider-fips-3.1.2", Version: "3.1.2-r0", Checksum: "Q1x", Depends: []string{"so:libc.so.6"}}
	repo := &fakeRepo{index: apk.NewIndex([]apk.Package{provider})}
	var gotURL string
	factory := func(url, _ string, _ packit.BuildpackInfo) Repository { gotURL = url; return repo }

	var log bytes.Buffer
	result, err := Build(factory, chronos.DefaultClock, scribe.NewEmitter(&log))(buildContext(t))
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if gotURL != "https://taha2samy-3.github.io/node/apk" {
		t.Errorf("repository = %s", gotURL)
	}

	var layer packit.Layer
	for _, l := range result.Layers {
		if l.Name == LayerProvider {
			layer = l
		}
	}
	if !layer.Launch || !layer.Build {
		t.Errorf("provider layer must be build and launch: %+v", layer)
	}
	conf := filepath.Join(layer.Path, "etc", "ssl", "openssl.cnf")
	if layer.LaunchEnv["OPENSSL_CONF.override"] != conf ||
		layer.LaunchEnv["OPENSSL_MODULES.override"] != filepath.Join(layer.Path, "lib", "ossl-modules") {
		t.Errorf("launch env = %v", layer.LaunchEnv)
	}
	if layer.BuildEnv["BPI_OPENSSL_FIPS_CONF.override"] != conf {
		t.Errorf("build env = %v", layer.BuildEnv)
	}
	if _, ok := layer.BuildEnv["OPENSSL_CONF.override"]; ok {
		t.Error("approved mode at build time must be opt-in")
	}
	if raw, _ := os.ReadFile(conf); !bytes.Contains(raw, []byte(".include "+filepath.Join(layer.Path, "etc/ssl/fipsmodule.cnf"))) {
		t.Errorf("openssl.cnf:\n%s", raw)
	}
	if result.Launch.Labels["io.fckfips.openssl-fips.certificate"] != "#4985" {
		t.Errorf("labels = %v", result.Launch.Labels)
	}
	if !strings.Contains(log.String(), "IG 2.3.A") {
		t.Error("the operational environment caveat must be logged")
	}
}

func TestBuildAtBuildTime(t *testing.T) {
	t.Setenv("BP_OPENSSL_FIPS_BUILD", "true")
	repo := &fakeRepo{index: apk.NewIndex([]apk.Package{{Name: "openssl-provider-fips-3.1.2", Version: "3.1.2-r0"}})}
	factory := func(string, string, packit.BuildpackInfo) Repository { return repo }
	result, err := Build(factory, chronos.DefaultClock, scribe.NewEmitter(&bytes.Buffer{}))(buildContext(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range result.Layers {
		if l.Name == LayerProvider && l.BuildEnv["OPENSSL_CONF.override"] == "" {
			t.Errorf("BP_OPENSSL_FIPS_BUILD must export OPENSSL_CONF at build: %v", l.BuildEnv)
		}
	}
}

func TestBuildFailures(t *testing.T) {
	tests := []struct {
		name string
		repo *fakeRepo
		want string
	}{
		{"missing package", &fakeRepo{index: apk.NewIndex(nil)}, "does not offer"},
		{"unexpected dependency", &fakeRepo{index: apk.NewIndex([]apk.Package{
			{Name: "openssl-provider-fips-3.1.2", Version: "3.1.2-r0", Depends: []string{"so:libfoo.so.1"}},
		})}, "only the C library"},
		{"no module MAC", &fakeRepo{noMAC: true, index: apk.NewIndex([]apk.Package{
			{Name: "openssl-provider-fips-3.1.2", Version: "3.1.2-r0"},
		})}, "module-mac"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := func(string, string, packit.BuildpackInfo) Repository { return tt.repo }
			_, err := Build(factory, chronos.DefaultClock, scribe.NewEmitter(&bytes.Buffer{}))(buildContext(t))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}
