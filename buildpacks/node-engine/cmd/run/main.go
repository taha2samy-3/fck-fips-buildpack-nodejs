// Command run is the detect and build executable of the node-engine
// buildpack; packit dispatches on the name it is invoked as.
package main

import (
	"os"

	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/scribe"

	nodeengine "github.com/taha2samy-3/fck-fips-buildpacks-nodejs/buildpacks/node-engine"
)

func main() {
	logger := scribe.NewEmitter(os.Stdout).WithLevel(os.Getenv("BP_LOG_LEVEL"))
	packit.Run(
		nodeengine.Detect(),
		nodeengine.Build(nodeengine.NewSources, nodeengine.RunNode, chronos.DefaultClock, logger),
	)
}
