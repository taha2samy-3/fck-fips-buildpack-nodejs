package apk

import (
	"cmp"
	"strconv"
	"strings"
)

// Suffix ranks follow apk-tools: pre-release suffixes sort before a plain
// version, post-release suffixes after it.
var suffixRank = map[string]int{
	"alpha": 0, "beta": 1, "pre": 2, "rc": 3,
	"cvs": 5, "svn": 6, "git": 7, "hg": 8, "p": 9,
}

const noSuffix = 4

type suffix struct {
	rank int
	num  uint64
}

type version struct {
	nums     []uint64
	letter   byte
	suffixes []suffix
	rev      uint64
}

// CompareVersions orders two apk version strings ("24.11.1-r0") the way
// apk-tools does. It returns -1, 0 or +1. Strings that do not parse are
// compared lexically, after every valid version.
func CompareVersions(a, b string) int {
	va, okA := parseVersion(a)
	vb, okB := parseVersion(b)
	switch {
	case !okA && !okB:
		return strings.Compare(a, b)
	case !okA:
		return -1
	case !okB:
		return 1
	}

	for i := 0; i < len(va.nums) || i < len(vb.nums); i++ {
		if i >= len(va.nums) {
			return -1
		}
		if i >= len(vb.nums) {
			return 1
		}
		if c := cmp.Compare(va.nums[i], vb.nums[i]); c != 0 {
			return c
		}
	}

	if va.letter != vb.letter {
		if va.letter < vb.letter {
			return -1
		}
		return 1
	}

	for i := 0; i < len(va.suffixes) || i < len(vb.suffixes); i++ {
		sa, sb := suffix{rank: noSuffix}, suffix{rank: noSuffix}
		if i < len(va.suffixes) {
			sa = va.suffixes[i]
		}
		if i < len(vb.suffixes) {
			sb = vb.suffixes[i]
		}
		if c := cmp.Compare(sa.rank, sb.rank); c != 0 {
			return c
		}
		if c := cmp.Compare(sa.num, sb.num); c != 0 {
			return c
		}
	}

	return cmp.Compare(va.rev, vb.rev)
}

func parseVersion(s string) (version, bool) {
	var v version

	if i := strings.LastIndex(s, "-r"); i >= 0 {
		rev, err := strconv.ParseUint(s[i+2:], 10, 64)
		if err != nil {
			return v, false
		}
		v.rev = rev
		s = s[:i]
	}

	upstream, rest, _ := strings.Cut(s, "_")
	if upstream == "" {
		return v, false
	}
	if last := upstream[len(upstream)-1]; last >= 'a' && last <= 'z' {
		v.letter = last
		upstream = upstream[:len(upstream)-1]
	}
	for _, part := range strings.Split(upstream, ".") {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return v, false
		}
		v.nums = append(v.nums, n)
	}

	if rest != "" {
		for _, part := range strings.Split(rest, "_") {
			name := strings.TrimRight(part, "0123456789")
			rank, ok := suffixRank[name]
			if !ok {
				return v, false
			}
			sfx := suffix{rank: rank}
			if digits := part[len(name):]; digits != "" {
				n, err := strconv.ParseUint(digits, 10, 64)
				if err != nil {
					return v, false
				}
				sfx.num = n
			}
			v.suffixes = append(v.suffixes, sfx)
		}
	}

	return v, true
}
