// Package nodeengine installs Node.js from the Wolfi apk repository, and npm
// from the npm registry, into build and launch layers.
package nodeengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/paketo-buildpacks/libnodejs"
	"github.com/paketo-buildpacks/packit/v2"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/env"
)

// Build plan names. "node" and "npm" match the Paketo Node.js buildpacks, so
// npm-install, yarn-install, node-start and friends work unchanged.
const (
	PlanNode        = "node"
	PlanNPM         = "npm"
	PlanOpenSSLFIPS = "openssl-fips"
)

// Detect always passes: whether Node.js is needed is decided by the
// buildpacks that require "node". It records the versions the app asks for
// and, when FIPS mode is requested, requires the openssl-fips buildpack so a
// builder without it fails loudly instead of producing a non-FIPS image.
func Detect() packit.DetectFunc {
	return func(context packit.DetectContext) (packit.DetectResult, error) {
		projectPath, err := libnodejs.FindProjectPath(context.WorkingDir)
		if err != nil {
			return packit.DetectResult{}, err
		}

		var requires []packit.BuildPlanRequirement
		addVersion := func(raw, source string) error {
			v, err := NormalizeVersion(raw)
			if err != nil {
				return fmt.Errorf("%s: %w", source, err)
			}
			if v != "" {
				requires = append(requires, packit.BuildPlanRequirement{
					Name:     PlanNode,
					Metadata: map[string]any{"version": v, "version-source": source},
				})
			}
			return nil
		}

		if err := addVersion(os.Getenv("BP_NODE_VERSION"), "BP_NODE_VERSION"); err != nil {
			return packit.DetectResult{}, err
		}
		for _, file := range []string{".nvmrc", ".node-version"} {
			raw, err := os.ReadFile(filepath.Join(projectPath, file))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			} else if err != nil {
				return packit.DetectResult{}, err
			}
			if err := addVersion(string(raw), file); err != nil {
				return packit.DetectResult{}, err
			}
		}
		engines, err := enginesNode(projectPath)
		if err != nil {
			return packit.DetectResult{}, err
		}
		if err := addVersion(engines, "package.json"); err != nil {
			return packit.DetectResult{}, err
		}

		fips, err := env.Bool("BP_OPENSSL_FIPS")
		if err != nil {
			return packit.DetectResult{}, err
		}
		if fips {
			requires = append(requires, packit.BuildPlanRequirement{Name: PlanOpenSSLFIPS})
		}

		return packit.DetectResult{
			Plan: packit.BuildPlan{
				Provides: []packit.BuildPlanProvision{{Name: PlanNode}},
				Requires: requires,
				Or: []packit.BuildPlan{{
					Provides: []packit.BuildPlanProvision{{Name: PlanNode}, {Name: PlanNPM}},
					Requires: requires,
				}},
			},
		}, nil
	}
}

// enginesNode reads engines.node from package.json, if there is one. It is
// parsed here rather than with libnodejs so that an unrelated
// BP_NODE_START_SCRIPT setting cannot fail detection.
func enginesNode(projectPath string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(projectPath, "package.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	var pkg struct {
		Engines map[string]any `json:"engines"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return "", fmt.Errorf("package.json: %w", err)
	}
	v, _ := pkg.Engines["node"].(string)
	return v, nil
}
