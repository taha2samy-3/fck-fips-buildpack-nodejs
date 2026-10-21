package nodeengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/paketo-buildpacks/packit/v2"

	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/apk"
	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/env"
	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/fetch"
	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/keys"
	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/npm"
)

const (
	defaultWolfiRepository = "https://packages.wolfi.dev/os"
	defaultNodeDist        = "https://nodejs.org/dist"
)

type liveSources struct {
	client   *fetch.Client
	wolfi    apk.Repository
	registry npm.Registry
	dist     string
}

// NewSources returns Sources backed by the network. BP_WOLFI_REPOSITORY,
// BP_NPM_REGISTRY and BP_NODE_DIST_URL point them at mirrors; a Wolfi mirror
// must serve the index exactly as Wolfi signed it.
func NewSources(cacheDir string, info packit.BuildpackInfo) Sources {
	client := fetch.New(fetch.UserAgent(info.ID, info.Version))
	client.CacheDir = cacheDir
	return liveSources{
		client: client,
		wolfi: apk.Repository{
			URL:    env.String("BP_WOLFI_REPOSITORY", defaultWolfiRepository),
			Keys:   keys.WolfiKeyring(),
			Client: client,
		},
		registry: npm.Registry{URL: env.String("BP_NPM_REGISTRY", npm.DefaultRegistry), Client: client},
		dist:     strings.TrimSuffix(env.String("BP_NODE_DIST_URL", defaultNodeDist), "/"),
	}
}

func (s liveSources) WolfiIndex(ctx context.Context, arch string) (*apk.Index, bool, error) {
	return s.wolfi.Index(ctx, arch)
}

func (s liveSources) InstallAPKs(ctx context.Context, arch string, pkgs []apk.Package, dest string) error {
	return s.wolfi.Install(ctx, arch, pkgs, dest, apk.UsrMerge)
}

func (s liveSources) Packument(ctx context.Context, name string) (npm.Packument, error) {
	return s.registry.Packument(ctx, name)
}

func (s liveSources) InstallNPM(ctx context.Context, m npm.Manifest, dest string) error {
	return s.registry.Install(ctx, m, dest)
}

// BundledNPM looks the release up in nodejs.org/dist/index.json, which lists
// the npm version shipped with every Node.js release.
func (s liveSources) BundledNPM(ctx context.Context, nodeVersion string) (string, error) {
	url := s.dist + "/index.json"
	path, err := s.client.Cached(ctx, url)
	if err != nil && !errors.Is(err, fetch.ErrStale) {
		return "", err
	}
	if s.client.CacheDir == "" {
		defer os.Remove(path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var releases []struct {
		Version string `json:"version"`
		NPM     string `json:"npm"`
	}
	if err := json.Unmarshal(raw, &releases); err != nil {
		return "", fmt.Errorf("decoding %s: %w", url, err)
	}
	for _, r := range releases {
		if r.Version == "v"+nodeVersion && r.NPM != "" {
			return r.NPM, nil
		}
	}
	return "", fmt.Errorf("%s does not list Node.js %s", url, nodeVersion)
}

// RunNode executes a node binary and returns its combined output.
func RunNode(path string, args, environ []string) (string, error) {
	cmd := exec.Command(path, args...)
	cmd.Env = environ
	out, err := cmd.CombinedOutput()
	return string(out), err
}
