// Command node-options is an exec.d helper: the CNB launcher runs it before
// the app starts and applies the environment it writes to file descriptor 3.
//
// It completes NODE_OPTIONS from the runtime environment:
//
//   - --use-openssl-ca, so certificates added by the ca-certificates
//     buildpack or the image are trusted, unless a CA option is already set;
//   - --force-fips, when the image was built with BP_OPENSSL_FIPS=true;
//   - --max-old-space-size at 75% of the container memory limit, when
//     OPTIMIZE_MEMORY=true (set at build time by BP_NODE_OPTIMIZE_MEMORY);
//   - --inspect on 127.0.0.1, when BPL_DEBUG_ENABLED=true.
//
// Options the user already put in NODE_OPTIONS are never overridden.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/buildpacks/node-engine/launch"
)

func main() {
	out := os.NewFile(3, "exec.d output")
	if out == nil {
		fmt.Fprintln(os.Stderr, "node-options: file descriptor 3 is not open; run me through the CNB launcher")
		os.Exit(1)
	}
	if err := write(out, launch.Compute(os.Getenv, launch.CgroupMemoryLimit)); err != nil {
		fmt.Fprintf(os.Stderr, "node-options: %v\n", err)
		os.Exit(1)
	}
}

// write emits the variables as TOML. A JSON string is a valid TOML basic
// string, which keeps the encoder out of this tiny binary.
func write(w io.Writer, vars map[string]string) error {
	var b strings.Builder
	for _, k := range launch.Order {
		v, ok := vars[k]
		if !ok {
			continue
		}
		quoted, err := json.Marshal(v)
		if err != nil {
			return err
		}
		b.WriteString(k + " = " + string(quoted) + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
