package opensslfips

import (
	"context"

	"github.com/paketo-buildpacks/packit/v2"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/apk"
	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/fetch"
	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/keys"
)

type liveRepository struct{ repo apk.Repository }

// NewRepository returns the signed provider repository at url. Whatever the
// url, only the embedded repository key is trusted, so a mirror has to serve
// the packages exactly as they were signed.
func NewRepository(url, cacheDir string, info packit.BuildpackInfo) Repository {
	client := fetch.New(fetch.UserAgent(info.ID, info.Version))
	client.CacheDir = cacheDir
	return liveRepository{repo: apk.Repository{URL: url, Keys: keys.FIPSKeyring(), Client: client}}
}

func (r liveRepository) Index(ctx context.Context, arch string) (*apk.Index, bool, error) {
	return r.repo.Index(ctx, arch)
}

func (r liveRepository) Install(ctx context.Context, arch string, pkgs []apk.Package, dest string) error {
	return r.repo.Install(ctx, arch, pkgs, dest, apk.UsrMerge)
}
