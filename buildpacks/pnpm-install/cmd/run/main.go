// Command run is the detect and build executable of the pnpm-install
// buildpack.
package main

import (
	"log"
	"os"

	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/pexec"
	"github.com/paketo-buildpacks/packit/v2/sbom"
	"github.com/paketo-buildpacks/packit/v2/scribe"
	"github.com/paketo-buildpacks/packit/v2/servicebindings"

	pnpminstall "github.com/taha2samy-3/fck-fips-buildpacks-nodejs/buildpacks/pnpm-install"
	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/env"
)

type sbomGenerator struct{}

func (sbomGenerator) Generate(dir string) (sbom.SBOM, error) { return sbom.Generate(dir) }

func main() {
	logger := scribe.NewEmitter(os.Stdout).WithLevel(os.Getenv("BP_LOG_LEVEL"))
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	relax, err := env.Bool(pnpminstall.MismatchEnv)
	if err != nil {
		log.Fatal(err)
	}
	installer := pnpminstall.NewInstaller(pexec.NewExecutable("pnpm"), pexec.NewExecutable("node"), logger, relax)

	packit.Run(
		pnpminstall.Detect(),
		pnpminstall.Build(
			servicebindings.NewResolver(),
			installer,
			sbomGenerator{},
			pnpminstall.Paths{Home: home, Tmp: os.TempDir()},
			chronos.DefaultClock,
			logger,
		),
	)
}
