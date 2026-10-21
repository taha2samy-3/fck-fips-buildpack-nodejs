// Command run is the detect and build executable of the openssl-fips
// buildpack.
package main

import (
	"os"

	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/scribe"

	opensslfips "github.com/taha2samy-3/fck-fips-buildpack-nodejs/buildpacks/openssl-fips"
)

func main() {
	logger := scribe.NewEmitter(os.Stdout).WithLevel(os.Getenv("BP_LOG_LEVEL"))
	packit.Run(opensslfips.Detect(), opensslfips.Build(opensslfips.NewRepository, chronos.DefaultClock, logger))
}
