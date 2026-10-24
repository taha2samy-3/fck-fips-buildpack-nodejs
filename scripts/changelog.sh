#!/usr/bin/env bash
# Prints the CHANGELOG.md section of a version, without its heading.
# Fails when the section is missing or empty, so a release cannot go out
# undocumented.
#
#   scripts/changelog.sh 1.0.1
set -euo pipefail

version="${1:?usage: changelog.sh <version>}"
changelog="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/CHANGELOG.md"

notes="$(awk -v v="${version}" '
  $0 ~ "^## \\[" v "\\]" { found = 1; next }
  found && /^## \[/       { exit }
  found && /^\[[^]]+\]: / { exit }
  found                   { print }
' "${changelog}")"

if [[ -z "${notes//[[:space:]]/}" ]]; then
  echo "CHANGELOG.md has no entry for ${version}; add a '## [${version}] - YYYY-MM-DD' section" >&2
  exit 1
fi
printf '%s\n' "${notes}"
