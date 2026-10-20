// Package launch computes the runtime environment the node-options exec.d
// helper hands to the CNB launcher.
package launch

import (
	"os"
	"strconv"
	"strings"
)

// Order is the order variables are written in.
var Order = []string{"MEMORY_AVAILABLE", "NODE_OPTIONS"}

const defaultDebugPort = "9229"

// cgroup v1 reports "no limit" as a number close to the maximum int64.
const unlimitedV1 = 1 << 62

// Compute returns the variables to set. getenv reads the current
// environment; memoryLimit returns the container memory limit in bytes, or
// 0 when there is none.
func Compute(getenv func(string) string, memoryLimit func() int64) map[string]string {
	vars := map[string]string{}
	original := getenv("NODE_OPTIONS")
	opts := strings.Fields(original)

	has := func(prefixes ...string) bool {
		for _, o := range opts {
			for _, p := range prefixes {
				if o == p || strings.HasPrefix(o, p+"=") {
					return true
				}
			}
		}
		return false
	}

	if !has("--use-openssl-ca", "--use-bundled-ca", "--use-system-ca") {
		opts = append(opts, "--use-openssl-ca")
	}

	if isTrue(getenv("BPI_NODE_FORCE_FIPS")) && !has("--force-fips") {
		opts = append(opts, "--force-fips")
	}

	mb := memoryMB(getenv, memoryLimit)
	if mb > 0 {
		vars["MEMORY_AVAILABLE"] = strconv.FormatInt(mb, 10)
		if isTrue(getenv("OPTIMIZE_MEMORY")) && !has("--max-old-space-size", "--max_old_space_size") {
			opts = append(opts, "--max-old-space-size="+strconv.FormatInt(mb*3/4, 10))
		}
	}

	if isTrue(getenv("BPL_DEBUG_ENABLED")) && !has("--inspect", "--inspect-brk", "--inspect-wait") {
		port := getenv("BPL_DEBUG_PORT")
		if port == "" {
			port = defaultDebugPort
		}
		opts = append(opts, "--inspect=127.0.0.1:"+port)
	}

	if joined := strings.Join(opts, " "); joined != original {
		vars["NODE_OPTIONS"] = joined
	}
	return vars
}

// memoryMB prefers an explicit MEMORY_AVAILABLE (in MB) over the cgroup limit.
func memoryMB(getenv func(string) string, memoryLimit func() int64) int64 {
	if v, err := strconv.ParseInt(strings.TrimSpace(getenv("MEMORY_AVAILABLE")), 10, 64); err == nil && v > 0 {
		return v
	}
	if limit := memoryLimit(); limit > 0 {
		return limit / (1024 * 1024)
	}
	return 0
}

// CgroupMemoryLimit reads the memory limit from cgroup v2, then v1.
func CgroupMemoryLimit() int64 {
	for _, path := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil || v <= 0 || v >= unlimitedV1 {
			// "max" (v2) or the v1 sentinel: no limit.
			return 0
		}
		return v
	}
	return 0
}

func isTrue(s string) bool {
	v, err := strconv.ParseBool(strings.TrimSpace(s))
	return err == nil && v
}
