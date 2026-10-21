package nodeengine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/draft"
	"github.com/paketo-buildpacks/packit/v2/scribe"

	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/apk"
	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/env"
	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/npm"
	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/sbom"
)

const (
	LayerNode  = "node"
	LayerNPM   = "npm"
	LayerCache = "cache"
)

// Sources is everything the build downloads. The real implementation talks
// to Wolfi, the npm registry and nodejs.org; tests substitute a fake.
type Sources interface {
	WolfiIndex(ctx context.Context, arch string) (idx *apk.Index, stale bool, err error)
	InstallAPKs(ctx context.Context, arch string, pkgs []apk.Package, dest string) error
	Packument(ctx context.Context, name string) (npm.Packument, error)
	InstallNPM(ctx context.Context, m npm.Manifest, dest string) error
	// BundledNPM returns the npm version upstream ships with a Node.js release.
	BundledNPM(ctx context.Context, nodeVersion string) (string, error)
}

// SourcesFactory builds Sources that cache downloads below cacheDir.
type SourcesFactory func(cacheDir string, info packit.BuildpackInfo) Sources

// NodeRunner runs the node binary at path and returns its combined output.
type NodeRunner func(path string, args, env []string) (string, error)

// buildpackMetadata is the [metadata] table of buildpack.toml.
type buildpackMetadata struct {
	DefaultVersions map[string]string `toml:"default-versions"`
}

// Build installs Node.js and npm.
func Build(newSources SourcesFactory, runNode NodeRunner, clock chronos.Clock, logger scribe.Emitter) packit.BuildFunc {
	return func(bctx packit.BuildContext) (packit.BuildResult, error) {
		logger.Title("%s %s", bctx.BuildpackInfo.Name, bctx.BuildpackInfo.Version)
		ctx := context.Background()

		meta, err := readMetadata(bctx.CNBPath)
		if err != nil {
			return packit.BuildResult{}, err
		}

		arch, err := apk.Arch(cmp.Or(bctx.TargetInfo.Arch, runtime.GOARCH))
		if err != nil {
			return packit.BuildResult{}, err
		}

		fips, err := env.Bool("BP_OPENSSL_FIPS")
		if err != nil {
			return packit.BuildResult{}, err
		}
		optimizeMemory, err := env.Bool("BP_NODE_OPTIMIZE_MEMORY")
		if err != nil {
			return packit.BuildResult{}, err
		}

		cacheLayer, err := bctx.Layers.Get(LayerCache)
		if err != nil {
			return packit.BuildResult{}, err
		}
		cacheLayer.Cache = true
		sources := newSources(cacheLayer.Path, bctx.BuildpackInfo)

		// --- Node.js -----------------------------------------------------
		constraint, source := requestedVersion(bctx.Plan.Entries)
		if constraint == "" {
			constraint, source = meta.DefaultVersions[PlanNode], "buildpack default"
		}
		logger.Process("Resolving Node.js version")
		logger.Subprocess("Requested %q (from %s)", constraint, source)

		idx, stale, err := sources.WolfiIndex(ctx, arch)
		if err != nil {
			return packit.BuildResult{}, err
		}
		if stale {
			logger.Subprocess("Warning: the Wolfi index could not be refreshed; using the cached copy")
		}
		nodePkg, err := SelectNode(idx, constraint)
		if err != nil {
			return packit.BuildResult{}, err
		}
		nodeVersion, _ := upstreamVersion(nodePkg.Version)
		logger.Subprocess("Selected Node.js %s (%s, %s)", nodeVersion, nodePkg, arch)
		logger.Break()

		pkgs, err := idx.Closure([]apk.Package{nodePkg}, excluded)
		if err != nil {
			return packit.BuildResult{}, err
		}

		planner := draft.NewPlanner()
		launch, build := planner.MergeLayerTypes(PlanNode, bctx.Plan.Entries)

		nodeLayer, err := bctx.Layers.Get(LayerNode)
		if err != nil {
			return packit.BuildResult{}, err
		}
		key := packageKey(arch, pkgs)
		if cached, _ := nodeLayer.Metadata["packages"].(string); cached == key {
			logger.Process("Reusing cached layer %s", nodeLayer.Path)
		} else {
			logger.Process("Installing Node.js %s and %d shared libraries", nodeVersion, len(pkgs)-1)
			if nodeLayer, err = nodeLayer.Reset(); err != nil {
				return packit.BuildResult{}, err
			}
			duration, err := clock.Measure(func() error {
				return sources.InstallAPKs(ctx, arch, pkgs, nodeLayer.Path)
			})
			if err != nil {
				return packit.BuildResult{}, err
			}
			for _, p := range pkgs {
				logger.Action("%s (%s)", p, p.License)
			}
			logger.Action("Verified against the signed Wolfi index; completed in %s", duration.Round(time.Millisecond))

			nodeLayer.Metadata = map[string]any{"packages": key}
			if nodeLayer.SBOM, err = apkSBOM("node", arch, pkgs).InFormats(bctx.BuildpackInfo.SBOMFormats...); err != nil {
				return packit.BuildResult{}, err
			}
		}
		logger.Break()

		// Wolfi's ICU looks for its data under /usr/share/icu, which the layer
		// relocates; ICU_DATA points it at the layer copy.
		icuData, err := findICUData(nodeLayer.Path)
		if err != nil {
			return packit.BuildResult{}, err
		}

		nodeBin := filepath.Join(nodeLayer.Path, "bin", "node")
		nodeEnv := withLibraryPath(os.Environ(), filepath.Join(nodeLayer.Path, "lib"))
		if icuData != "" {
			nodeEnv = append(nodeEnv, "ICU_DATA="+icuData)
		}
		if err := smokeTest(nodeBin, nodeEnv, nodeVersion.String(), runNode); err != nil {
			return packit.BuildResult{}, err
		}

		nodeLayer.Build, nodeLayer.Launch, nodeLayer.Cache = build, launch, build
		nodeLayer.SharedEnv.Default("NODE_HOME", nodeLayer.Path)
		if icuData != "" {
			nodeLayer.SharedEnv.Override("ICU_DATA", icuData)
		}
		nodeLayer.BuildEnv.Default("NODE_OPTIONS", "--use-openssl-ca")
		nodeLayer.LaunchEnv.Default("NODE_ENV", "production")
		if optimizeMemory {
			nodeLayer.LaunchEnv.Default("OPTIMIZE_MEMORY", "true")
		}
		if fips {
			nodeLayer.LaunchEnv.Override(env.NodeForceFIPS, "true")
		}
		nodeLayer.ExecD = []string{filepath.Join(bctx.CNBPath, "bin", "node-options")}
		logger.EnvironmentVariables(nodeLayer)

		layers := []packit.Layer{cacheLayer, nodeLayer}

		// --- npm -----------------------------------------------------------
		npmLaunch, npmBuild := planner.MergeLayerTypes(PlanNPM, bctx.Plan.Entries)
		if build || npmBuild || npmLaunch {
			npmLayer, err := installNPM(ctx, sources, bctx, nodeVersion.String(), clock, logger)
			if err != nil {
				return packit.BuildResult{}, err
			}
			npmLayer.Build, npmLayer.Cache = true, true
			npmLayer.Launch = npmLaunch
			layers = append(layers, npmLayer)
		}

		// --- FIPS ----------------------------------------------------------
		if fips {
			if err := verifyFIPS(nodeBin, nodeEnv, runNode, logger); err != nil {
				return packit.BuildResult{}, err
			}
		}

		return packit.BuildResult{Layers: layers}, nil
	}
}

func installNPM(ctx context.Context, sources Sources, bctx packit.BuildContext, nodeVersion string, clock chronos.Clock, logger scribe.Emitter) (packit.Layer, error) {
	logger.Process("Resolving npm version")
	spec, source := os.Getenv("BP_NPM_VERSION"), "BP_NPM_VERSION"
	if spec == "" {
		bundled, err := sources.BundledNPM(ctx, nodeVersion)
		if err != nil {
			return packit.Layer{}, fmt.Errorf("finding the npm version bundled with Node.js %s (set BP_NPM_VERSION to choose one): %w", nodeVersion, err)
		}
		spec, source = bundled, "bundled with Node.js "+nodeVersion
	}
	logger.Subprocess("Requested %q (%s)", spec, source)

	packument, err := sources.Packument(ctx, "npm")
	if err != nil {
		return packit.Layer{}, err
	}
	manifest, err := packument.Resolve(spec)
	if err != nil {
		return packit.Layer{}, err
	}
	logger.Subprocess("Selected npm %s", manifest.Version)
	if want := manifest.Engine("node"); want != "" && !satisfies(nodeVersion, want) {
		logger.Subprocess("Warning: npm %s declares engines.node %q, which Node.js %s does not satisfy", manifest.Version, want, nodeVersion)
	}
	logger.Break()

	layer, err := bctx.Layers.Get(LayerNPM)
	if err != nil {
		return packit.Layer{}, err
	}
	if cached, _ := layer.Metadata["integrity"].(string); cached != "" && cached == manifest.Dist.Integrity {
		logger.Process("Reusing cached layer %s", layer.Path)
		logger.Break()
		return layer, nil
	}

	logger.Process("Installing npm %s", manifest.Version)
	if layer, err = layer.Reset(); err != nil {
		return packit.Layer{}, err
	}
	duration, err := clock.Measure(func() error {
		pkgDir := filepath.Join(layer.Path, "lib", "node_modules", "npm")
		if err := sources.InstallNPM(ctx, manifest, pkgDir); err != nil {
			return err
		}
		return npm.LinkBins(filepath.Join(layer.Path, "bin"), pkgDir, manifest)
	})
	if err != nil {
		return packit.Layer{}, err
	}
	logger.Action("Verified against the registry integrity; completed in %s", duration.Round(time.Millisecond))
	logger.Break()

	layer.Metadata = map[string]any{"integrity": manifest.Dist.Integrity}
	doc := sbom.Document{Name: "npm", Components: []sbom.Component{npmComponent(manifest)}}
	if layer.SBOM, err = doc.InFormats(bctx.BuildpackInfo.SBOMFormats...); err != nil {
		return packit.Layer{}, err
	}
	return layer, nil
}

// findICUData returns the directory holding ICU's icudt*.dat in the layer,
// or "" if node was built without system ICU.
func findICUData(layer string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(layer, "share", "icu", "*", "icudt*.dat"))
	if err != nil || len(matches) == 0 {
		return "", err
	}
	slices.Sort(matches)
	return filepath.Dir(matches[len(matches)-1]), nil
}

// nodeSmokeTest touches what a relocated node most easily gets wrong: the
// shared libraries, ICU data (Intl) and OpenSSL.
const nodeSmokeTest = `
console.log(process.version);
new Intl.DateTimeFormat('ar-EG').format(0);
require('crypto').createHash('sha256').update('').digest('hex');
`

// smokeTest runs the installed node before the image is exported, so a
// layer that cannot start never ships.
func smokeTest(nodeBin string, environ []string, want string, runNode NodeRunner) error {
	out, err := runNode(nodeBin, []string{"-e", nodeSmokeTest}, environ)
	if err != nil {
		return fmt.Errorf("installed node does not run: %w\n%s", err, out)
	}
	if got := strings.TrimSpace(out); got != "v"+want {
		return fmt.Errorf("installed node reports %q, expected v%s", got, want)
	}
	return nil
}

// excluded keeps base-system packages out of the node layer, and OpenSSL
// provider modules and configurations too: libcrypto pulls some in, but which
// providers an image carries is chosen explicitly (see openssl-fips), never
// as a side effect.
func excluded(p apk.Package) bool {
	return apk.BaseSystem(p) ||
		strings.HasPrefix(p.Name, "openssl-provider-") ||
		strings.HasPrefix(p.Name, "openssl-config-")
}

// packageKey identifies the exact set of packages installed in a layer.
func packageKey(arch string, pkgs []apk.Package) string {
	names := make([]string, len(pkgs))
	for i, p := range pkgs {
		names[i] = p.String() + "@" + p.Checksum
	}
	slices.Sort(names)
	return arch + " " + strings.Join(names, " ")
}

func apkSBOM(name, arch string, pkgs []apk.Package) sbom.Document {
	doc := sbom.Document{Name: name}
	for _, p := range pkgs {
		doc.Components = append(doc.Components, sbom.Component{
			Name:     p.Name,
			Version:  p.Version,
			PURL:     fmt.Sprintf("pkg:apk/wolfi/%s@%s?arch=%s&distro=wolfi", p.Name, p.Version, arch),
			License:  p.License,
			Supplier: "Wolfi",
		})
	}
	return doc
}

func npmComponent(m npm.Manifest) sbom.Component {
	c := sbom.Component{
		Name:     m.Name,
		Version:  m.Version,
		PURL:     "pkg:npm/" + strings.ReplaceAll(m.Name, "@", "%40") + "@" + m.Version,
		Supplier: "npm",
	}
	if d, err := npm.ParseIntegrity(m.Dist.Integrity); err == nil {
		c.Checksum = fmt.Sprintf("%s:%x", d.Algorithm, d.Sum)
	}
	return c
}

func readMetadata(cnbPath string) (buildpackMetadata, error) {
	var bp struct {
		Metadata buildpackMetadata `toml:"metadata"`
	}
	if _, err := toml.DecodeFile(filepath.Join(cnbPath, "buildpack.toml"), &bp); err != nil {
		return buildpackMetadata{}, fmt.Errorf("reading buildpack.toml: %w", err)
	}
	if bp.Metadata.DefaultVersions[PlanNode] == "" {
		return buildpackMetadata{}, errors.New("buildpack.toml has no default node version")
	}
	return bp.Metadata, nil
}

// withLibraryPath prepends dir to LD_LIBRARY_PATH in environ.
func withLibraryPath(environ []string, dir string) []string {
	out := make([]string, 0, len(environ)+1)
	value := dir
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "LD_LIBRARY_PATH="); ok {
			if v != "" {
				value = dir + ":" + v
			}
			continue
		}
		out = append(out, kv)
	}
	return append(out, "LD_LIBRARY_PATH="+value)
}
