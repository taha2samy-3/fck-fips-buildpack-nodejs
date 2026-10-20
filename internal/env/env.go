// Package env reads buildpack configuration from environment variables.
package env

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Bool reads a boolean variable. Unset or empty is false; a value that is
// not a boolean is an error, so a typo cannot silently disable a setting
// such as FIPS mode.
func Bool(name string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s=%q is not a boolean (use true or false)", name, raw)
	}
	return v, nil
}

// BoolDefault is Bool with a value for when the variable is unset or empty.
func BoolDefault(name string, def bool) (bool, error) {
	if strings.TrimSpace(os.Getenv(name)) == "" {
		return def, nil
	}
	return Bool(name)
}

// String returns the variable, or def when it is unset or empty.
func String(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// Variables the buildpacks pass to each other. BPI_ marks them as internal:
// they are set by a buildpack, not by users.
const (
	// FIPSConf is the OpenSSL configuration that enables the FIPS provider,
	// exported to later buildpacks by openssl-fips.
	FIPSConf = "BPI_OPENSSL_FIPS_CONF"
	// FIPSModules is the directory holding fips.so, exported by openssl-fips.
	FIPSModules = "BPI_OPENSSL_FIPS_MODULES"
	// NodeForceFIPS tells node-engine's launch helper to add --force-fips.
	NodeForceFIPS = "BPI_NODE_FORCE_FIPS"
)
