package pnpm

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/Masterminds/semver/v3"
	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/draft"
	"github.com/paketo-buildpacks/packit/v2/scribe"

	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/npm"
	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/sbom"
)

const (
	LayerPNPM = "pnpm"

	// MismatchEnv tells pnpm-install that the delivered pnpm differs from the
	// one pinned in "packageManager" (because BP_PNPM_VERSION overrode it), so
	// pnpm's own version check has to be relaxed for the install to run.
	MismatchEnv = "BPI_PNPM_VERSION_MISMATCH"
)

var versionSources = []string{"BP_PNPM_VERSION", "package.json", "pnpm-lock.yaml"}

// Registry is the npm registry pnpm is downloaded from.
type Registry interface {
	Packument(ctx context.Context, name string) (npm.Packument, error)
	Install(ctx context.Context, m npm.Manifest, dest string, extra ...npm.Digest) error
}

// NodeVersion returns the version of the node on PATH ("" if there is none).
type NodeVersion func() (string, error)

// Build installs pnpm.
func Build(registry Registry, nodeVersion NodeVersion, clock chronos.Clock, logger scribe.Emitter) packit.BuildFunc {
	return func(bctx packit.BuildContext) (packit.BuildResult, error) {
		logger.Title("%s %s", bctx.BuildpackInfo.Name, bctx.BuildpackInfo.Version)
		ctx := context.Background()

		spec, source := requested(bctx.Plan.Entries)
		if spec == "" {
			def, err := defaultVersion(bctx.CNBPath)
			if err != nil {
				return packit.BuildResult{}, err
			}
			spec, source = def, "buildpack default"
		}

		logger.Process("Resolving pnpm version")
		logger.Subprocess("Requested %q (from %s)", spec, source)
		packument, err := registry.Packument(ctx, "pnpm")
		if err != nil {
			return packit.BuildResult{}, err
		}
		manifest, err := packument.Resolve(spec)
		if err != nil {
			return packit.BuildResult{}, err
		}
		logger.Subprocess("Selected pnpm %s", manifest.Version)

		// A hash in packageManager pins the tarball; honour it whenever the
		// pinned version is the one being installed.
		var extra []npm.Digest
		pinned, _ := pinnedPackageManager(bctx.Plan.Entries)
		if pinned.Version == manifest.Version && pinned.Hash != "" {
			d, err := npm.ParseCorepackHash(pinned.Hash)
			if err != nil {
				return packit.BuildResult{}, fmt.Errorf("package.json packageManager: %w", err)
			}
			extra = append(extra, d)
			logger.Subprocess("Tarball must also match the packageManager %s hash", d.Algorithm)
		}
		mismatch := pinned.Version != "" && pinned.Version != manifest.Version
		if mismatch {
			logger.Subprocess("Note: package.json pins pnpm@%s; installing %s as %s asks", pinned.Version, manifest.Version, source)
		}

		// pnpm 12+ is a native executable published as a per-platform
		// optional dependency of the "pnpm" package, chosen by its install
		// script. Pick it here instead: the exact version the wrapper pins,
		// for this architecture, glibc build.
		arch := cmp.Or(bctx.TargetInfo.Arch, runtime.GOARCH)
		var native *npm.Manifest
		if name, version, ok := nativePackage(manifest, arch); ok {
			p, err := registry.Packument(ctx, name)
			if err != nil {
				return packit.BuildResult{}, err
			}
			m, err := p.Resolve(version)
			if err != nil {
				return packit.BuildResult{}, err
			}
			native = &m
			logger.Subprocess("Native executable %s@%s", m.Name, m.Version)
		} else if err := checkNode(manifest, nodeVersion); err != nil {
			return packit.BuildResult{}, err
		}
		logger.Break()

		key := manifest.Dist.Integrity
		if native != nil {
			key += " " + native.Dist.Integrity
		}

		launch, build := draft.NewPlanner().MergeLayerTypes(PlanPNPM, bctx.Plan.Entries)

		layer, err := bctx.Layers.Get(LayerPNPM)
		if err != nil {
			return packit.BuildResult{}, err
		}
		if cached, _ := layer.Metadata["integrity"].(string); cached != "" && cached == key {
			logger.Process("Reusing cached layer %s", layer.Path)
		} else {
			logger.Process("Installing pnpm %s", manifest.Version)
			if layer, err = layer.Reset(); err != nil {
				return packit.BuildResult{}, err
			}
			duration, err := clock.Measure(func() error {
				pkgDir := filepath.Join(layer.Path, "lib", "node_modules", "pnpm")
				if err := registry.Install(ctx, manifest, pkgDir, extra...); err != nil {
					return err
				}
				binDir := filepath.Join(layer.Path, "bin")
				if native == nil {
					return npm.LinkBins(binDir, pkgDir, manifest)
				}
				nativeDir := filepath.Join(layer.Path, "lib", "node_modules", filepath.FromSlash(native.Name))
				if err := registry.Install(ctx, *native, nativeDir); err != nil {
					return err
				}
				return linkNative(binDir, filepath.Join(nativeDir, "pnpm"), manifest)
			})
			if err != nil {
				return packit.BuildResult{}, err
			}
			logger.Action("Verified against the registry integrity; completed in %s", duration.Round(time.Millisecond))

			layer.Metadata = map[string]any{"integrity": key}
			c := sbom.Component{
				Name:     "pnpm",
				Version:  manifest.Version,
				PURL:     "pkg:npm/pnpm@" + manifest.Version,
				License:  "MIT",
				Supplier: "npm",
			}
			if d, err := npm.ParseIntegrity(manifest.Dist.Integrity); err == nil {
				c.Checksum = fmt.Sprintf("%s:%x", d.Algorithm, d.Sum)
			}
			doc := sbom.Document{Name: "pnpm", Components: []sbom.Component{c}}
			if native != nil {
				nc := sbom.Component{
					Name:     native.Name,
					Version:  native.Version,
					PURL:     "pkg:npm/" + strings.Replace(native.Name, "@", "%40", 1) + "@" + native.Version,
					License:  "MIT",
					Supplier: "npm",
				}
				if d, err := npm.ParseIntegrity(native.Dist.Integrity); err == nil {
					nc.Checksum = fmt.Sprintf("%s:%x", d.Algorithm, d.Sum)
				}
				doc.Components = append(doc.Components, nc)
			}
			if layer.SBOM, err = doc.InFormats(bctx.BuildpackInfo.SBOMFormats...); err != nil {
				return packit.BuildResult{}, err
			}
		}
		logger.Break()

		layer.Build, layer.Launch, layer.Cache = build, launch, build
		layer.BuildEnv.Override(MismatchEnv, fmt.Sprint(mismatch))
		logger.EnvironmentVariables(layer)

		return packit.BuildResult{Layers: []packit.Layer{layer}}, nil
	}
}

// nativePackage returns the platform package of a native pnpm release
// (pnpm 12+) for arch, if the release has one.
func nativePackage(m npm.Manifest, arch string) (name, version string, ok bool) {
	cpu := map[string]string{"amd64": "x64", "x86_64": "x64", "arm64": "arm64", "aarch64": "arm64"}[arch]
	if cpu == "" {
		return "", "", false
	}
	name = "@pnpm/exe.linux-" + cpu
	version, ok = m.OptionalDependencies[name]
	return name, version, ok
}

// linkNative points every command the wrapper declares (pnpm, pn, pnpx,
// pnx) at the native executable, which dispatches on the name it was run as.
func linkNative(binDir, executable string, wrapper npm.Manifest) error {
	bins, err := wrapper.Bins()
	if err != nil {
		return err
	}
	if err := os.Chmod(executable, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	link, err := filepath.Rel(binDir, executable)
	if err != nil {
		return err
	}
	for name := range bins {
		if err := os.Symlink(link, filepath.Join(binDir, name)); err != nil {
			return err
		}
	}
	return nil
}

// requested returns the highest priority version request in the plan.
func requested(entries []packit.BuildpackPlanEntry) (spec, source string) {
	best := len(versionSources) + 1
	for _, e := range entries {
		if e.Name != PlanPNPM {
			continue
		}
		v, _ := e.Metadata["version"].(string)
		if v == "" {
			continue
		}
		src, _ := e.Metadata["version-source"].(string)
		rank := len(versionSources)
		for i, s := range versionSources {
			if s == src {
				rank = i
			}
		}
		if rank < best {
			best, spec, source = rank, v, src
		}
	}
	return spec, source
}

func pinnedPackageManager(entries []packit.BuildpackPlanEntry) (PackageManager, bool) {
	for _, e := range entries {
		if v, _ := e.Metadata["package-manager"].(string); e.Name == PlanPNPM && v != "" {
			hash, _ := e.Metadata["package-manager-hash"].(string)
			return PackageManager{Version: v, Hash: hash}, true
		}
	}
	return PackageManager{}, false
}

// checkNode fails early, with a clear message, when the pnpm release does
// not support the Node.js the build uses (pnpm 11 needs Node.js 22, for
// example); pnpm itself would otherwise fail later with a cryptic error.
func checkNode(m npm.Manifest, nodeVersion NodeVersion) error {
	want := m.Engine("node")
	if want == "" {
		return nil
	}
	have, err := nodeVersion()
	if err != nil || have == "" {
		return nil
	}
	c, err := semver.NewConstraint(want)
	if err != nil {
		return nil
	}
	v, err := semver.NewVersion(strings.TrimPrefix(have, "v"))
	if err != nil {
		return nil
	}
	if !c.Check(v) {
		return fmt.Errorf("pnpm %s requires Node.js %s, but the build uses Node.js %s; set BP_NODE_VERSION or engines.node to a compatible version, or choose another pnpm with BP_PNPM_VERSION", m.Version, want, v)
	}
	return nil
}

// NodeOnPath runs `node --version` from PATH.
func NodeOnPath() (string, error) {
	path, err := exec.LookPath("node")
	if errors.Is(err, exec.ErrNotFound) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	out, err := exec.Command(path, "--version").Output()
	return strings.TrimSpace(string(out)), err
}

func defaultVersion(cnbPath string) (string, error) {
	var bp struct {
		Metadata struct {
			DefaultVersions map[string]string `toml:"default-versions"`
		} `toml:"metadata"`
	}
	if _, err := toml.DecodeFile(filepath.Join(cnbPath, "buildpack.toml"), &bp); err != nil {
		return "", fmt.Errorf("reading buildpack.toml: %w", err)
	}
	if v := bp.Metadata.DefaultVersions[PlanPNPM]; v != "" {
		return v, nil
	}
	return "", errors.New("buildpack.toml has no default pnpm version")
}
