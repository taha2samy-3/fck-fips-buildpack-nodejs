package opensslfips

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/scribe"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/apk"
	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/env"
	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/sbom"
)

const (
	LayerProvider = "openssl-fips"
	LayerCache    = "cache"
)

// Repository is the signed apk repository the provider comes from.
type Repository interface {
	Index(ctx context.Context, arch string) (idx *apk.Index, stale bool, err error)
	Install(ctx context.Context, arch string, pkgs []apk.Package, dest string) error
}

// RepositoryFactory builds a Repository that caches its index below cacheDir.
type RepositoryFactory func(url, cacheDir string, info packit.BuildpackInfo) Repository

// moduleMetadata is [metadata.module] in buildpack.toml: which validated
// module to install. It changes only when NIST lists a new certificate.
type moduleMetadata struct {
	Package     string `toml:"package"`
	Name        string `toml:"name"`
	Certificate string `toml:"certificate"`
	Repository  string `toml:"repository"`
}

// Build installs the provider and points OpenSSL at it.
func Build(newRepository RepositoryFactory, clock chronos.Clock, logger scribe.Emitter) packit.BuildFunc {
	return func(bctx packit.BuildContext) (packit.BuildResult, error) {
		logger.Title("%s %s", bctx.BuildpackInfo.Name, bctx.BuildpackInfo.Version)
		ctx := context.Background()

		module, err := readModule(bctx.CNBPath)
		if err != nil {
			return packit.BuildResult{}, err
		}
		atBuild, err := env.Bool("BP_OPENSSL_FIPS_BUILD")
		if err != nil {
			return packit.BuildResult{}, err
		}
		arch, err := apk.Arch(cmp.Or(bctx.TargetInfo.Arch, runtime.GOARCH))
		if err != nil {
			return packit.BuildResult{}, err
		}

		cacheLayer, err := bctx.Layers.Get(LayerCache)
		if err != nil {
			return packit.BuildResult{}, err
		}
		cacheLayer.Cache = true

		repoURL := env.String("BP_OPENSSL_FIPS_REPOSITORY", module.Repository)
		repo := newRepository(repoURL, cacheLayer.Path, bctx.BuildpackInfo)

		logger.Process("Resolving %s", module.Name)
		idx, stale, err := repo.Index(ctx, arch)
		if err != nil {
			return packit.BuildResult{}, err
		}
		if stale {
			logger.Subprocess("Warning: %s could not be refreshed; using the cached index", repoURL)
		}
		provider, ok := idx.Latest(module.Package)
		if !ok {
			return packit.BuildResult{}, fmt.Errorf("%s does not offer %s for %s", repoURL, module.Package, arch)
		}
		if err := checkDepends(provider); err != nil {
			return packit.BuildResult{}, err
		}
		pkgs := []apk.Package{provider}
		logger.Subprocess("Selected %s from %s", provider, repoURL)
		logger.Break()

		layer, err := bctx.Layers.Get(LayerProvider)
		if err != nil {
			return packit.BuildResult{}, err
		}
		key := arch + " " + provider.String() + "@" + provider.Checksum
		if cached, _ := layer.Metadata["package"].(string); cached == key {
			logger.Process("Reusing cached layer %s", layer.Path)
		} else {
			logger.Process("Installing %s", provider)
			if layer, err = layer.Reset(); err != nil {
				return packit.BuildResult{}, err
			}
			duration, err := clock.Measure(func() error {
				return repo.Install(ctx, arch, pkgs, layer.Path)
			})
			if err != nil {
				return packit.BuildResult{}, err
			}
			logger.Action("Verified against the signed index; completed in %s", duration.Round(time.Millisecond))
			layer.Metadata = map[string]any{"package": key}

			doc := sbom.Document{Name: LayerProvider}
			for _, p := range pkgs {
				doc.Components = append(doc.Components, sbom.Component{
					Name:     p.Name,
					Version:  p.Version,
					PURL:     fmt.Sprintf("pkg:apk/%s@%s?arch=%s", p.Name, p.Version, arch),
					License:  p.License,
					Supplier: provider.Origin,
				})
			}
			if layer.SBOM, err = doc.InFormats(bctx.BuildpackInfo.SBOMFormats...); err != nil {
				return packit.BuildResult{}, err
			}
		}

		modules := filepath.Join(layer.Path, "lib", "ossl-modules")
		moduleConf := filepath.Join(layer.Path, "etc", "ssl", "fipsmodule.cnf")
		if err := checkModule(modules, moduleConf); err != nil {
			return packit.BuildResult{}, fmt.Errorf("%s: %w", provider, err)
		}
		conf := filepath.Join(layer.Path, "etc", "ssl", "openssl.cnf")
		rendered, err := renderConfig(module.Name, moduleConf)
		if err != nil {
			return packit.BuildResult{}, err
		}
		if err := os.WriteFile(conf, rendered, 0o644); err != nil {
			return packit.BuildResult{}, err
		}

		layer.Launch, layer.Build, layer.Cache = true, true, true
		layer.LaunchEnv.Override("OPENSSL_CONF", conf)
		layer.LaunchEnv.Override("OPENSSL_MODULES", modules)
		// Later buildpacks (node-engine) use these to verify FIPS mode
		// without forcing it on every build step.
		layer.BuildEnv.Override(env.FIPSConf, conf)
		layer.BuildEnv.Override(env.FIPSModules, modules)
		if atBuild {
			layer.BuildEnv.Override("OPENSSL_CONF", conf)
			layer.BuildEnv.Override("OPENSSL_MODULES", modules)
		}
		logger.EnvironmentVariables(layer)

		logger.Process("FIPS 140-3 validated module")
		logger.Subprocess("%s, CMVP certificate %s", module.Name, module.Certificate)
		logger.Subprocess("The validation belongs to the module. This image is not FIPS certified.")
		logger.Subprocess("The certificate lists tested operational environments; Wolfi is not one of them.")
		logger.Subprocess("Using the unchanged module here is a port under FIPS 140-3 IG 2.3.A, which the operator affirms.")
		if !atBuild {
			logger.Subprocess("Approved mode applies at launch. Set BP_OPENSSL_FIPS_BUILD=true to enforce it during the build as well.")
		}
		logger.Break()

		return packit.BuildResult{
			Layers: []packit.Layer{cacheLayer, layer},
			Launch: packit.LaunchMetadata{
				Labels: map[string]string{
					"io.fckfips.openssl-fips.module":      module.Name,
					"io.fckfips.openssl-fips.certificate": module.Certificate,
					"io.fckfips.openssl-fips.package":     provider.String(),
				},
			},
		}, nil
	}
}

// checkDepends makes sure the provider needs nothing but the C library,
// which the run image supplies. The provider repository carries only the
// module, so anything else could not be installed from it.
func checkDepends(p apk.Package) error {
	for _, dep := range p.Depends {
		if dep == "so:libc.so.6" || strings.HasPrefix(dep, "so:ld-linux") {
			continue
		}
		return fmt.Errorf("%s depends on %s; only the C library is expected", p, dep)
	}
	return nil
}

// checkModule confirms the package delivered the module and its integrity
// MAC. The MAC itself is checked by the module's self test on every load.
func checkModule(modules, moduleConf string) error {
	if _, err := os.Stat(filepath.Join(modules, "fips.so")); err != nil {
		return fmt.Errorf("fips.so missing: %w", err)
	}
	raw, err := os.ReadFile(moduleConf)
	if err != nil {
		return fmt.Errorf("fipsmodule.cnf missing: %w", err)
	}
	if !bytes.Contains(raw, []byte("module-mac")) {
		return fmt.Errorf("%s has no module-mac", moduleConf)
	}
	return nil
}

func readModule(cnbPath string) (moduleMetadata, error) {
	var bp struct {
		Metadata struct {
			Module moduleMetadata `toml:"module"`
		} `toml:"metadata"`
	}
	if _, err := toml.DecodeFile(filepath.Join(cnbPath, "buildpack.toml"), &bp); err != nil {
		return moduleMetadata{}, fmt.Errorf("reading buildpack.toml: %w", err)
	}
	m := bp.Metadata.Module
	if m.Package == "" || m.Repository == "" {
		return moduleMetadata{}, fmt.Errorf("buildpack.toml is missing [metadata.module] package or repository")
	}
	return m, nil
}
