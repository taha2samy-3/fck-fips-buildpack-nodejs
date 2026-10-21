package pnpminstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/paketo-buildpacks/libnodejs"
	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/draft"
	"github.com/paketo-buildpacks/packit/v2/sbom"
	"github.com/paketo-buildpacks/packit/v2/scribe"

	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/env"
)

const (
	LayerBuildModules  = "build-modules"
	LayerLaunchModules = "launch-modules"
	LayerStore         = "pnpm-store"

	// MismatchEnv is set by the pnpm buildpack when the pnpm it installed
	// differs from the one "packageManager" pins.
	MismatchEnv = "BPI_PNPM_VERSION_MISMATCH"
)

// InstallProcess is what Build needs from pnpm; Installer implements it.
type InstallProcess interface {
	Versions(dir string) (pnpmVersion, nodeVersion string, err error)
	ConfiguredStore(dir string) (path string, exists bool, err error)
	CacheKey(dir string, extra ...string) (string, error)
	Install(dir, layerDir, pnpmVersion string, prod bool, opts Options, offlineStore bool) error
}

// SBOMGenerator catalogues the installed dependencies.
type SBOMGenerator interface {
	Generate(dir string) (sbom.SBOM, error)
}

// Paths are host locations Build works with.
type Paths struct {
	Home string // links for npmrc/pnpmrc bindings go here
	Tmp  string // holds the node_modules indirection link
}

// Build installs node_modules for the build and/or launch image.
func Build(bindings BindingResolver, installer InstallProcess, sbomGenerator SBOMGenerator, paths Paths, clock chronos.Clock, logger scribe.Emitter) packit.BuildFunc {
	return func(bctx packit.BuildContext) (result packit.BuildResult, err error) {
		logger.Title("%s %s", bctx.BuildpackInfo.Name, bctx.BuildpackInfo.Version)

		projectPath, err := libnodejs.FindProjectPath(bctx.WorkingDir)
		if err != nil {
			return packit.BuildResult{}, err
		}

		unlink, err := linkConfig(bindings, bctx.Platform.Path, paths.Home)
		if err != nil {
			return packit.BuildResult{}, err
		}
		defer func() {
			if cleanupErr := unlink(); err == nil {
				err = cleanupErr
			}
		}()

		strictScripts, err := env.Bool("BP_PNPM_STRICT_BUILD_SCRIPTS")
		if err != nil {
			return packit.BuildResult{}, err
		}
		mismatch, err := env.Bool(MismatchEnv)
		if err != nil {
			return packit.BuildResult{}, err
		}
		sbomDisabled, err := env.Bool("BP_DISABLE_SBOM")
		if err != nil {
			return packit.BuildResult{}, err
		}

		pnpmVersion, nodeVersion, err := installer.Versions(projectPath)
		if err != nil {
			return packit.BuildResult{}, err
		}
		logger.Process("Using pnpm %s on Node.js %s", pnpmVersion, nodeVersion)

		configures, err := ConfiguresBuilds(projectPath)
		if err != nil {
			return packit.BuildResult{}, err
		}
		allowBuilds := !strictScripts && !configures && majorOf(pnpmVersion) >= 10
		switch {
		case configures:
			logger.Subprocess("Dependency build scripts: as configured by the project")
		case allowBuilds:
			logger.Subprocess("Dependency build scripts: allowed (set BP_PNPM_STRICT_BUILD_SCRIPTS=true to keep pnpm's default of blocking them)")
		}

		storeLayer, err := bctx.Layers.Get(LayerStore)
		if err != nil {
			return packit.BuildResult{}, err
		}
		storeLayer.Cache = true
		layers := []packit.Layer{storeLayer}

		storePath, offlineStore, err := installer.ConfiguredStore(projectPath)
		if err != nil {
			return packit.BuildResult{}, err
		}
		if offlineStore {
			logger.Subprocess("Using the configured store %s offline", storePath)
		}
		logger.Break()

		opts := Options{
			StoreDir:    filepath.Join(storeLayer.Path, "store"),
			AllowBuilds: allowBuilds,
		}
		prodAtLaunch := os.Getenv("NODE_ENV") != "development"

		var cachedSBOM *sbom.SBOM
		layerSBOM := func() (packit.SBOMFormatter, error) {
			if sbomDisabled {
				return nil, nil
			}
			if cachedSBOM == nil {
				logger.GeneratingSBOM(projectPath)
				var s sbom.SBOM
				duration, err := clock.Measure(func() (err error) {
					s, err = sbomGenerator.Generate(projectPath)
					return err
				})
				if err != nil {
					return nil, err
				}
				logger.Action("Completed in %s", duration.Round(time.Millisecond))
				logger.Break()
				cachedSBOM = &s
			}
			logger.FormattingSBOM(bctx.BuildpackInfo.SBOMFormats...)
			return cachedSBOM.InFormats(bctx.BuildpackInfo.SBOMFormats...)
		}

		install := func(name string, prod bool) (packit.Layer, error) {
			layer, err := bctx.Layers.Get(name)
			if err != nil {
				return packit.Layer{}, err
			}
			key, err := installer.CacheKey(projectPath,
				"pnpm="+pnpmVersion, "node="+nodeVersion,
				fmt.Sprintf("prod=%t allow-builds=%t mismatch=%t offline=%t", prod, allowBuilds, mismatch, offlineStore))
			if err != nil {
				return packit.Layer{}, err
			}

			if cached, _ := layer.Metadata["cache_sha"].(string); cached == key {
				logger.Process("Reusing cached layer %s", layer.Path)
				logger.Break()
				return layer, linkNodeModules(projectPath, layer.Path, paths.Tmp)
			}

			what := "all"
			if prod {
				what = "production"
			}
			logger.Process("Installing %s dependencies", what)
			if layer, err = layer.Reset(); err != nil {
				return packit.Layer{}, err
			}
			duration, err := clock.Measure(func() error {
				return installer.Install(projectPath, layer.Path, pnpmVersion, prod, opts, offlineStore)
			})
			if err != nil {
				return packit.Layer{}, err
			}
			logger.Action("Completed in %s", duration.Round(time.Millisecond))
			logger.Break()

			layer.Metadata = map[string]any{"cache_sha": key}
			if layer.SBOM, err = layerSBOM(); err != nil {
				return packit.Layer{}, err
			}
			return layer, linkNodeModules(projectPath, layer.Path, paths.Tmp)
		}

		launch, build := draft.NewPlanner().MergeLayerTypes(PlanNodeModules, bctx.Plan.Entries)
		if !launch && !build {
			return packit.BuildResult{}, errors.New("no buildpack asked for node_modules at build or launch")
		}

		if build {
			layer, err := install(LayerBuildModules, false)
			if err != nil {
				return packit.BuildResult{}, err
			}
			layer.Build, layer.Cache = true, true
			layer.BuildEnv.Append("PATH", filepath.Join(layer.Path, "node_modules", ".bin"), string(os.PathListSeparator))
			layer.BuildEnv.Override("NODE_ENV", "development")
			logger.EnvironmentVariables(layer)
			layers = append(layers, layer)
		}

		// Installed last, so the project's node_modules link ends up
		// pointing at the launch layer.
		if launch {
			layer, err := install(LayerLaunchModules, prodAtLaunch)
			if err != nil {
				return packit.BuildResult{}, err
			}
			layer.Launch, layer.Cache = true, true
			layer.LaunchEnv.Append("PATH", filepath.Join(layer.Path, "node_modules", ".bin"), string(os.PathListSeparator))
			layer.LaunchEnv.Default("NODE_PROJECT_PATH", projectPath)
			layer.ExecD = []string{filepath.Join(bctx.CNBPath, "bin", "setup-symlinks")}
			logger.EnvironmentVariables(layer)
			layers = append(layers, layer)
		}

		return packit.BuildResult{Layers: layers}, nil
	}
}
