package pnpminstall

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	packitfs "github.com/paketo-buildpacks/packit/v2/fs"
	"github.com/paketo-buildpacks/packit/v2/pexec"
	"github.com/paketo-buildpacks/packit/v2/scribe"
)

// Executable runs a program found on PATH.
type Executable interface {
	Execute(pexec.Execution) error
}

// Options are the inputs that shape an install.
type Options struct {
	// StoreDir is the content-addressable store kept in a cache layer.
	StoreDir string
	// AllowBuilds passes --dangerously-allow-all-builds (pnpm 10+) when the
	// project does not configure build approval itself.
	AllowBuilds bool
}

// Installer drives pnpm.
type Installer struct {
	pnpm   Executable
	node   Executable
	logger scribe.Emitter
	env    []string
}

// NewInstaller returns an Installer. relaxVersionCheck must be true when the
// pnpm buildpack installed a different pnpm than "packageManager" pins:
// otherwise pnpm refuses to run, or (pnpm 11) downloads the pinned version,
// for every command, not just install.
func NewInstaller(pnpm, node Executable, logger scribe.Emitter, relaxVersionCheck bool) Installer {
	i := Installer{pnpm: pnpm, node: node, logger: logger, env: os.Environ()}
	if relaxVersionCheck {
		i.env = append(i.env, versionCheckOff...)
	}
	return i
}

// Versions reports the pnpm and node versions in use.
func (i Installer) Versions(dir string) (pnpmVersion, nodeVersion string, err error) {
	if pnpmVersion, err = i.output(i.pnpm, dir, "--version"); err != nil {
		return "", "", fmt.Errorf("running pnpm --version: %w", err)
	}
	if nodeVersion, err = i.output(i.node, dir, "--version"); err != nil {
		return "", "", fmt.Errorf("running node --version: %w", err)
	}
	return pnpmVersion, nodeVersion, nil
}

// ConfiguredStore returns the store-dir the user configured (in .npmrc or a
// binding) and whether it exists. An existing, explicitly configured store is
// treated as a pre-populated offline store, as in air-gapped builds.
func (i Installer) ConfiguredStore(dir string) (string, bool, error) {
	out, err := i.output(i.pnpm, dir, "config", "get", "store-dir")
	if err != nil {
		return "", false, fmt.Errorf("running pnpm config get store-dir: %w", err)
	}
	if out == "" || out == "undefined" {
		return "", false, nil
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(dir, out)
	}
	info, err := os.Stat(out)
	if errors.Is(err, fs.ErrNotExist) {
		return out, false, nil
	} else if err != nil {
		return "", false, err
	}
	return out, info.IsDir(), nil
}

// CacheKey summarises everything that decides the content of node_modules,
// so a layer is reinstalled exactly when one of them changes. The Node.js
// version is part of it because native addons are built against its ABI.
func (i Installer) CacheKey(dir string, extra ...string) (string, error) {
	h := sha256.New()
	for _, name := range []string{"pnpm-lock.yaml", "package.json", "pnpm-workspace.yaml", ".npmrc", ".pnpmfile.cjs"} {
		f, err := os.Open(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(h, "%s: absent\n", name)
			continue
		} else if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s:\n", name)
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}
	}

	config, err := i.output(i.pnpm, dir, "config", "list")
	if err != nil {
		return "", fmt.Errorf("running pnpm config list: %w", err)
	}
	fmt.Fprintf(h, "config:\n%s\n", config)
	for _, e := range extra {
		fmt.Fprintf(h, "%s\n", e)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Install runs `pnpm install` in dir and moves the resulting node_modules
// into layerDir, leaving a symlink behind. prod drops devDependencies.
func (i Installer) Install(dir, layerDir, pnpmVersion string, prod bool, opts Options, offlineStore bool) error {
	nodeModules := filepath.Join(dir, "node_modules")
	if err := os.RemoveAll(nodeModules); err != nil {
		return fmt.Errorf("removing node_modules: %w", err)
	}

	args := []string{"install", "--frozen-lockfile"}
	if offlineStore {
		args = append(args, "--offline")
	} else {
		args = append(args, "--store-dir", opts.StoreDir, "--prefer-offline")
	}
	if prod {
		args = append(args, "--prod")
	}

	major := majorOf(pnpmVersion)
	if opts.AllowBuilds && major >= 10 {
		args = append(args, "--dangerously-allow-all-builds")
	}
	environ := append(slices.Clone(i.env),
		// Non-interactive: never stop at a "remove node_modules?" prompt.
		"CI=true",
		"PATH="+os.Getenv("PATH")+string(os.PathListSeparator)+filepath.Join(nodeModules, ".bin"),
	)
	if !prod {
		environ = append(environ, "NODE_ENV=development")
	}

	i.logger.Subprocess("Running 'pnpm %s'", strings.Join(args, " "))
	err := i.pnpm.Execute(pexec.Execution{
		Args:   args,
		Dir:    dir,
		Env:    environ,
		Stdout: i.logger.ActionWriter,
		Stderr: i.logger.ActionWriter,
	})
	if err != nil {
		return fmt.Errorf("pnpm install failed: %w", err)
	}

	dest := filepath.Join(layerDir, "node_modules")
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(layerDir, 0o755); err != nil {
		return err
	}
	if _, err := os.Lstat(nodeModules); errors.Is(err, fs.ErrNotExist) {
		// No dependencies at all: pnpm creates nothing.
		return os.MkdirAll(dest, 0o755)
	}
	if err := packitfs.Move(nodeModules, dest); err != nil {
		return fmt.Errorf("moving node_modules into %s: %w", layerDir, err)
	}
	return nil
}

// versionCheckOff relaxes pnpm's check that its own version matches
// "packageManager". The setting moved between releases, and an unknown CLI
// flag is fatal to pnpm while an unknown environment variable is ignored, so
// all generations are covered through the environment:
//
//   - pnpm 9 and 10: package-manager-strict, package-manager-strict-version
//     and manage-package-manager-versions (read from npm_config_*);
//   - pnpm 11+: pmOnFail (read from pnpm_config_*; npm_config_* is ignored).
var versionCheckOff = []string{
	"npm_config_package_manager_strict=false",
	"npm_config_package_manager_strict_version=false",
	"npm_config_manage_package_manager_versions=false",
	"pnpm_config_pm_on_fail=warn",
}

func (i Installer) output(exe Executable, dir string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	err := exe.Execute(pexec.Execution{Args: args, Dir: dir, Env: i.env, Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		return "", fmt.Errorf("%w\n%s%s", err, stdout.String(), stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

func majorOf(version string) int {
	major, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")
	n, _ := strconv.Atoi(major)
	return n
}

// buildApprovalKeys are the pnpm settings with which a project decides
// itself which dependencies may run install scripts.
var buildApprovalKeys = []string{
	"onlyBuiltDependencies", "onlyBuiltDependenciesFile", "neverBuiltDependencies",
	"ignoredBuiltDependencies", "allowBuilds", "dangerouslyAllowAllBuilds",
}

// ConfiguresBuilds reports whether the project sets any build approval
// setting, in package.json ("pnpm" field) or pnpm-workspace.yaml.
func ConfiguresBuilds(dir string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err == nil {
		var pkg struct {
			PNPM map[string]json.RawMessage `json:"pnpm"`
		}
		if err := json.Unmarshal(raw, &pkg); err != nil {
			return false, fmt.Errorf("package.json: %w", err)
		}
		for _, k := range buildApprovalKeys {
			if _, ok := pkg.PNPM[k]; ok {
				return true, nil
			}
		}
	}

	f, err := os.Open(filepath.Join(dir, "pnpm-workspace.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		for _, k := range buildApprovalKeys {
			// Top-level keys only: no indentation.
			if strings.HasPrefix(line, k+":") {
				return true, nil
			}
		}
	}
	return false, scanner.Err()
}
