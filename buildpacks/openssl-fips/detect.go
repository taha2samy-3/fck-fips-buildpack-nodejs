// Package opensslfips contributes the OpenSSL FIPS 140-3 provider and an
// OpenSSL configuration that runs every OpenSSL 3+ consumer in the image,
// Node.js included, in approved mode.
package opensslfips

import (
	"github.com/paketo-buildpacks/packit/v2"

	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/env"
)

// PlanOpenSSLFIPS is the build plan name this buildpack provides.
const PlanOpenSSLFIPS = "openssl-fips"

// Detect passes only when FIPS mode is requested with BP_OPENSSL_FIPS=true.
// It requires its own provision so that it can be used on its own, not only
// behind node-engine.
func Detect() packit.DetectFunc {
	return func(packit.DetectContext) (packit.DetectResult, error) {
		enabled, err := env.Bool("BP_OPENSSL_FIPS")
		if err != nil {
			return packit.DetectResult{}, err
		}
		if !enabled {
			return packit.DetectResult{}, packit.Fail.WithMessage("BP_OPENSSL_FIPS is not true")
		}
		return packit.DetectResult{
			Plan: packit.BuildPlan{
				Provides: []packit.BuildPlanProvision{{Name: PlanOpenSSLFIPS}},
				Requires: []packit.BuildPlanRequirement{{Name: PlanOpenSSLFIPS}},
			},
		}, nil
	}
}
