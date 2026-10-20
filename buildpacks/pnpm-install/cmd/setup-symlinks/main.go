// Command setup-symlinks is an exec.d helper. The app's node_modules is a
// link to $TMPDIR/node_modules, which does not survive export; at launch
// this re-creates that link so it points at the launch-modules layer the
// helper itself lives in (<layer>/exec.d/<n>-setup-symlinks).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/buildpacks/pnpm-install/symlinks"
)

func main() {
	exe, err := filepath.Abs(os.Args[0])
	if err != nil {
		fail(err)
	}
	project := os.Getenv("NODE_PROJECT_PATH")
	if project == "" {
		if project, err = os.Getwd(); err != nil {
			fail(err)
		}
	}
	if err := symlinks.Restore(filepath.Dir(filepath.Dir(exe)), project); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "setup-symlinks: %v\n", err)
	os.Exit(1)
}
