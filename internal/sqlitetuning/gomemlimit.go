package sqlitetuning

import (
	"os"
	"strconv"
	"strings"
)

// DefaultMemoryLimit is the GOMEMLIMIT used when no cgroup limit can be
// read — a bare-metal or non-container run, where the daemon is not the
// only thing on the box but also is not boxed in.
const DefaultMemoryLimit int64 = 512 << 20

// memLimitHeadroom is the fraction of what remains AFTER the SQLite
// cache that the Go soft limit may claim.
//
// The subtraction is the point, and a test caught its absence: an
// earlier draft took 70 % of the whole cgroup, which on a 128 MiB limit
// is 89 MiB — and 89 MiB of heap plus 70 MiB of page cache is 159 MiB
// in a 128 MiB box. The two policies contradicted each other because
// modernc's cache maps OUTSIDE the Go heap, so GOMEMLIMIT never sees it.
const memLimitHeadroom = 0.9

// minDerivedLimit floors the derived value. A cgroup barely larger than
// the cache budget would otherwise yield a soft limit so small the GC
// thrashes — worse than not deriving one at all.
const minDerivedLimit int64 = 32 << 20

// cgroup v2 then v1. Read in that order because a v2 host exposes the v1
// path as a compatibility mount on some systems, and v2 is authoritative
// where both exist.
var cgroupLimitPaths = []string{
	"/sys/fs/cgroup/memory.max",
	"/sys/fs/cgroup/memory/memory.limit_in_bytes",
}

// MemoryLimit returns the GOMEMLIMIT to install: a fraction of the
// cgroup memory limit when one is readable, else DefaultMemoryLimit.
//
// wad hardcoded 512 MiB regardless of container limits, so on the
// 128 MiB wa-burocracy cgroup the Go soft limit was 4x the ceiling and
// could not protect it — the GC had no reason to collect before the OOM
// killer arrived (issue #359).
//
// Falls back rather than failing: a daemon that refuses to start because
// it could not parse a cgroup file would be a worse bug than a soft
// limit that is merely not optimal.
func MemoryLimit() int64 {
	for _, p := range cgroupLimitPaths {
		raw, err := os.ReadFile(p) //nolint:gosec // fixed cgroup paths, not caller input
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(raw))
		// cgroup v2 writes "max" for unlimited; v1 writes a sentinel so
		// large it is meaningless. Both mean "no limit here".
		if text == "" || text == "max" {
			continue
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil || n <= 0 || n >= 1<<62 {
			continue
		}
		// Reserve the configured SQLite page cache first: it is real
		// resident memory the GC cannot account for or reclaim.
		avail := n - int64(TotalConfiguredCacheKiB)*1024
		limited := int64(float64(avail) * memLimitHeadroom)
		if limited < minDerivedLimit {
			// The cache alone nearly fills this cgroup. Deriving a tiny
			// limit would thrash; leave the default and let the operator
			// see the mismatch rather than GC-storm quietly.
			continue
		}
		return limited
	}
	return DefaultMemoryLimit
}
