// Package pnpminstall installs an app's dependencies with pnpm into build
// and launch layers.
package pnpminstall

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/paketo-buildpacks/libnodejs"
	"github.com/paketo-buildpacks/packit/v2"

	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/env"
)

const (
	PlanNodeModules = "node_modules"
	PlanNode        = "node"
	PlanPNPM        = "pnpm"
)

// Detect passes for apps with a pnpm-lock.yaml. It provides node_modules
// and requires node and pnpm at build time; pnpm is also put in the launch
// image only with BP_PNPM_IN_LAUNCH=true, since the start command does not
// need it.
func Detect() packit.DetectFunc {
	return func(context packit.DetectContext) (packit.DetectResult, error) {
		projectPath, err := libnodejs.FindProjectPath(context.WorkingDir)
		if err != nil {
			return packit.DetectResult{}, err
		}

		if _, err := os.Stat(filepath.Join(projectPath, "pnpm-lock.yaml")); errors.Is(err, fs.ErrNotExist) {
			return packit.DetectResult{}, packit.Fail.WithMessage("no pnpm-lock.yaml in %s", projectPath)
		} else if err != nil {
			return packit.DetectResult{}, err
		}

		raw, err := os.ReadFile(filepath.Join(projectPath, "package.json"))
		if errors.Is(err, fs.ErrNotExist) {
			return packit.DetectResult{}, packit.Fail.WithMessage("no package.json in %s", projectPath)
		} else if err != nil {
			return packit.DetectResult{}, err
		}
		var pkg struct {
			Engines map[string]any `json:"engines"`
		}
		if err := json.Unmarshal(raw, &pkg); err != nil {
			return packit.DetectResult{}, fmt.Errorf("package.json: %w", err)
		}

		pnpmLaunch, err := env.Bool("BP_PNPM_IN_LAUNCH")
		if err != nil {
			return packit.DetectResult{}, err
		}

		node := packit.BuildPlanRequirement{Name: PlanNode, Metadata: map[string]any{"build": true}}
		if v, _ := pkg.Engines["node"].(string); v != "" {
			node.Metadata = map[string]any{"build": true, "version": v, "version-source": "package.json"}
		}

		return packit.DetectResult{
			Plan: packit.BuildPlan{
				Provides: []packit.BuildPlanProvision{{Name: PlanNodeModules}},
				Requires: []packit.BuildPlanRequirement{
					node,
					{Name: PlanPNPM, Metadata: map[string]any{"build": true, "launch": pnpmLaunch}},
				},
			},
		}, nil
	}
}
