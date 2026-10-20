// Package keys embeds the public keys the buildpacks trust for apk
// repositories.
package keys

import (
	_ "embed"
	"fmt"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/apk"
)

// Wolfi signs https://packages.wolfi.dev/os.
//
//go:embed wolfi-signing.rsa.pub
var Wolfi []byte

// FIPS signs the OpenSSL FIPS provider repository at
// https://taha2samy-3.github.io/node/apk.
//
//go:embed taha2samy-3-fips.rsa.pub
var FIPS []byte

const (
	WolfiName = "wolfi-signing.rsa.pub"
	FIPSName  = "taha2samy-3-fips.rsa.pub"
)

// WolfiKeyring trusts only the Wolfi key.
func WolfiKeyring() apk.Keyring { return mustKeyring(WolfiName, Wolfi) }

// FIPSKeyring trusts only the FIPS provider repository key.
func FIPSKeyring() apk.Keyring { return mustKeyring(FIPSName, FIPS) }

func mustKeyring(name string, pem []byte) apk.Keyring {
	k := apk.Keyring{}
	if err := k.Add(name, pem); err != nil {
		panic(fmt.Sprintf("embedded key %s: %v", name, err))
	}
	return k
}
