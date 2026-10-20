// Command run is the detect and build executable of the pnpm buildpack.
package main

import (
	"os"

	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/scribe"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/buildpacks/pnpm"
	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/env"
	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/fetch"
	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/npm"
)

func main() {
	logger := scribe.NewEmitter(os.Stdout).WithLevel(os.Getenv("BP_LOG_LEVEL"))
	registry := npm.Registry{
		URL:    env.String("BP_NPM_REGISTRY", npm.DefaultRegistry),
		Client: fetch.New(fetch.UserAgent("fckfips/pnpm", "")),
	}
	packit.Run(pnpm.Detect(), pnpm.Build(registry, pnpm.NodeOnPath, chronos.DefaultClock, logger))
}
