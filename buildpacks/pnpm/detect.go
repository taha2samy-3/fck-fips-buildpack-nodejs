// Package pnpm installs the pnpm CLI from an npm registry.
package pnpm

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/paketo-buildpacks/libnodejs"
	"github.com/paketo-buildpacks/packit/v2"
)

// PlanPNPM is the build plan name this buildpack provides.
const PlanPNPM = "pnpm"

// Detect provides pnpm and records the version the app asks for. Whether
// pnpm is installed at all is decided by the buildpacks that require it
// (pnpm-install).
//
// Version sources, highest priority first:
//
//  1. BP_PNPM_VERSION: a version, range or dist-tag;
//  2. "packageManager": "pnpm@x.y.z[+sha512.<hex>]" in package.json;
//  3. lockfileVersion in pnpm-lock.yaml, for lockfiles only one pnpm major
//     writes (5.x -> 7, 6.x -> 8). Lockfile 9.0 is written by pnpm 9, 10
//     and 11 alike, so it says nothing and the buildpack default applies.
func Detect() packit.DetectFunc {
	return func(context packit.DetectContext) (packit.DetectResult, error) {
		projectPath, err := libnodejs.FindProjectPath(context.WorkingDir)
		if err != nil {
			return packit.DetectResult{}, err
		}

		plan := packit.BuildPlan{Provides: []packit.BuildPlanProvision{{Name: PlanPNPM}}}
		require := func(version, source string, extra map[string]any) {
			md := map[string]any{"version": version, "version-source": source}
			for k, v := range extra {
				md[k] = v
			}
			plan.Requires = append(plan.Requires, packit.BuildPlanRequirement{Name: PlanPNPM, Metadata: md})
		}

		if v := strings.TrimSpace(os.Getenv("BP_PNPM_VERSION")); v != "" {
			require(v, "BP_PNPM_VERSION", nil)
		}

		pm, err := readPackageManager(projectPath)
		if err != nil {
			return packit.DetectResult{}, err
		}
		if pm.Version != "" {
			extra := map[string]any{"package-manager": pm.Version}
			if pm.Hash != "" {
				extra["package-manager-hash"] = pm.Hash
			}
			require(pm.Version, "package.json", extra)
		}

		lockVersion, err := lockfileVersion(projectPath)
		if err != nil {
			return packit.DetectResult{}, err
		}
		if major := majorForLockfile(lockVersion); major != "" {
			require(major, "pnpm-lock.yaml", nil)
		}

		return packit.DetectResult{Plan: plan}, nil
	}
}

// PackageManager is the pnpm entry of package.json's "packageManager".
type PackageManager struct {
	Version string // "9.15.9"
	Hash    string // "sha512.<hex>", optional
}

func readPackageManager(projectPath string) (PackageManager, error) {
	raw, err := os.ReadFile(filepath.Join(projectPath, "package.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return PackageManager{}, nil
	} else if err != nil {
		return PackageManager{}, err
	}
	var pkg struct {
		PackageManager string `json:"packageManager"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return PackageManager{}, fmt.Errorf("package.json: %w", err)
	}
	return ParsePackageManager(pkg.PackageManager), nil
}

// ParsePackageManager reads "pnpm@9.15.9+sha512.<hex>". Other package
// managers yield an empty result.
func ParsePackageManager(field string) PackageManager {
	spec, ok := strings.CutPrefix(strings.TrimSpace(field), "pnpm@")
	if !ok {
		return PackageManager{}
	}
	version, hash, _ := strings.Cut(spec, "+")
	return PackageManager{Version: version, Hash: hash}
}

// lockfileVersion reads the top-level lockfileVersion of pnpm-lock.yaml
// without parsing the whole (possibly huge) file.
func lockfileVersion(projectPath string) (string, error) {
	f, err := os.Open(filepath.Join(projectPath, "pnpm-lock.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if v, ok := strings.CutPrefix(scanner.Text(), "lockfileVersion:"); ok {
			return strings.Trim(strings.TrimSpace(v), `'"`), nil
		}
	}
	return "", scanner.Err()
}

func majorForLockfile(v string) string {
	switch major, _, _ := strings.Cut(v, "."); major {
	case "5":
		return "7"
	case "6":
		return "8"
	}
	return ""
}
