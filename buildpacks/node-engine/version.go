package nodeengine

import (
	"bufio"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/paketo-buildpacks/packit/v2"

	"github.com/taha2samy-3/fck-fips-buildpacks-nodejs/internal/apk"
)

// ltsCodenames maps release codenames, as used in .nvmrc ("lts/iron"), to
// their major version.
var ltsCodenames = map[string]string{
	"argon": "4", "boron": "6", "carbon": "8", "dubnium": "10",
	"erbium": "12", "fermium": "14", "gallium": "16", "hydrogen": "18",
	"iron": "20", "jod": "22", "krypton": "24",
}

// newestLTS is what "lts/*" means. Update it when a new line enters LTS.
const newestLTS = "24"

// versionSources lists where a version can come from, highest priority first.
var versionSources = []string{"BP_NODE_VERSION", "package.json", ".nvmrc", ".node-version"}

// NormalizeVersion turns what users write in .nvmrc, .node-version,
// engines.node or BP_NODE_VERSION into a semver constraint. It returns ""
// for an empty input and an error for one that is not a version or range.
func NormalizeVersion(raw string) (string, error) {
	v := firstLine(raw)
	if v == "" {
		return "", nil
	}

	lower := strings.ToLower(v)
	switch {
	case lower == "node" || lower == "latest" || lower == "current" || lower == "stable" || lower == "*":
		return "*", nil
	case lower == "lts/*" || lower == "lts":
		return newestLTS, nil
	case strings.HasPrefix(lower, "lts/"):
		major, ok := ltsCodenames[strings.TrimPrefix(lower, "lts/")]
		if !ok {
			return "", fmt.Errorf("unknown LTS codename %q", v)
		}
		return major, nil
	}

	// "v22.11.0" -> "22.11.0"; ranges keep their own operators.
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if _, err := semver.NewConstraint(v); err != nil {
		return "", fmt.Errorf("%q is not a Node.js version or range", raw)
	}
	return v, nil
}

// firstLine returns the first line that is neither blank nor a comment.
func firstLine(s string) string {
	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		line := scanner.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return strings.TrimSpace(s)
}

// requestedVersion picks the node version constraint from the plan: the
// highest priority entry that carries a version. It returns "" when no
// entry asks for a specific version.
func requestedVersion(entries []packit.BuildpackPlanEntry) (version, source string) {
	best := len(versionSources) + 1
	for _, e := range entries {
		if e.Name != PlanNode {
			continue
		}
		v, _ := e.Metadata["version"].(string)
		if v == "" {
			continue
		}
		src, _ := e.Metadata["version-source"].(string)
		rank := len(versionSources)
		for i, s := range versionSources {
			if s == src {
				rank = i
			}
		}
		if rank < best {
			best, version, source = rank, v, src
		}
	}
	return version, source
}

var nodePackage = regexp.MustCompile(`^nodejs-(\d+)$`)

// SelectNode returns the newest nodejs-<major> package whose upstream
// version satisfies constraint.
func SelectNode(idx *apk.Index, constraint string) (apk.Package, error) {
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return apk.Package{}, fmt.Errorf("invalid Node.js version constraint %q: %w", constraint, err)
	}

	var (
		best      apk.Package
		bestVer   *semver.Version
		available = map[int]bool{}
	)
	for _, p := range idx.Packages() {
		m := nodePackage.FindStringSubmatch(p.Name)
		if m == nil {
			continue
		}
		major, _ := strconv.Atoi(m[1])
		available[major] = true

		v, err := upstreamVersion(p.Version)
		if err != nil || !c.Check(v) {
			continue
		}
		if bestVer == nil || v.GreaterThan(bestVer) || v.Equal(bestVer) && apk.CompareVersions(p.Version, best.Version) > 0 {
			best, bestVer = p, v
		}
	}

	if bestVer == nil {
		return apk.Package{}, fmt.Errorf("no Node.js release in Wolfi satisfies %q (majors available: %s)", constraint, majors(available))
	}
	return best, nil
}

// upstreamVersion strips the apk revision ("24.11.1-r2" -> 24.11.1).
func upstreamVersion(v string) (*semver.Version, error) {
	if i := strings.LastIndex(v, "-r"); i >= 0 {
		v = v[:i]
	}
	return semver.StrictNewVersion(v)
}

func majors(set map[int]bool) string {
	var out []string
	for _, m := range slices.Sorted(maps.Keys(set)) {
		out = append(out, strconv.Itoa(m))
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ", ")
}
