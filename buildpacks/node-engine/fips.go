package nodeengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/paketo-buildpacks/packit/v2/scribe"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/env"
)

// fipsCheck runs inside node under the FIPS configuration. It fails unless
// FIPS mode is on, an approved digest works and MD5 is refused, which only
// happens when the FIPS provider is the one serving the default properties.
const fipsCheck = `
const crypto = require('crypto');
if (crypto.getFips() !== 1) throw new Error('crypto.getFips() is not 1');
crypto.createHash('sha256').update('fips').digest();
let md5 = true;
try { crypto.createHash('md5'); } catch (e) { md5 = false; }
if (md5) throw new Error('MD5 is available, so a non-FIPS provider is active');
console.log(JSON.stringify({ fips: crypto.getFips(), openssl: process.versions.openssl }));
`

// verifyFIPS proves at build time that the installed node runs in FIPS mode
// with the configuration the openssl-fips buildpack contributed, so an
// image that would not start, or would start without FIPS, is never exported.
func verifyFIPS(nodeBin string, environ []string, runNode NodeRunner, logger scribe.Emitter) error {
	conf, modules := os.Getenv(env.FIPSConf), os.Getenv(env.FIPSModules)
	if conf == "" || modules == "" {
		return errors.New("BP_OPENSSL_FIPS is set but the openssl-fips buildpack did not run before node-engine; check the builder's order")
	}

	logger.Process("Verifying FIPS mode")
	environ = append(environ, "OPENSSL_CONF="+conf, "OPENSSL_MODULES="+modules)
	out, err := runNode(nodeBin, []string{"--force-fips", "-e", fipsCheck}, environ)
	if err != nil {
		return fmt.Errorf("node does not run in FIPS mode with %s: %w\n%s", conf, err, out)
	}

	var result struct {
		FIPS    int    `json:"fips"`
		OpenSSL string `json:"openssl"`
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &result); err != nil {
		return fmt.Errorf("unexpected output from the FIPS check: %s", out)
	}
	logger.Subprocess("crypto.getFips() = %d, OpenSSL %s, MD5 refused", result.FIPS, result.OpenSSL)
	logger.Subprocess("NODE_OPTIONS will include --force-fips at launch")
	logger.Break()
	return nil
}

// satisfies reports whether version meets an npm style range. Ranges this
// package cannot parse count as satisfied; they only drive warnings.
func satisfies(version, constraint string) bool {
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return true
	}
	v, err := semver.NewVersion(version)
	if err != nil {
		return true
	}
	return c.Check(v)
}
