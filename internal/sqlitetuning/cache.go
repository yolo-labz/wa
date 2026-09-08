// Package sqlitetuning holds the SQLite page-cache budgets every store
// shares, so the numbers live in one reviewable place with the
// measurement that produced them.
//
// The old values were 64 MiB for messages.db and 32 MiB for each of the
// five side stores — 224 MiB of configured cache per daemon, against a
// 128 MiB cgroup on wa-burocracy. modernc's allocator maps those pages
// outside the Go heap, so they showed up as ~54 MiB of generic anonymous
// RSS that `wa.heap.bytes` (7.4 MiB) could not explain (issue #359).
package sqlitetuning

// HistoryCachePragma is messages.db's page cache: 32 MiB.
//
// Measured, not picked. Against the 100 000-row / 34.4 MiB bench fixture
// — larger than the 24.1 MiB messages.db in production — the SC-03
// messages.search p99 is indistinguishable between 64 MiB and 32 MiB:
//
//	64 MiB → 323 ms   (budget 500 ms)
//	32 MiB → 324 ms, 349 ms on a repeat
//	16 MiB → 473 ms   ← degrades, and close to the budget
//	 8 MiB → 409 ms   ← the inversion vs 16 MiB is run-to-run noise,
//	                    which is itself the reason not to go there
//
// SC-02 thread.get is 862 µs at 64 MiB and 927 µs at 32 MiB, against a
// 200 ms budget — never the constraint. So 32 MiB is where the curve is
// still flat, and it still exceeds the real database, meaning the hot
// set continues to fit entirely.
//
// This deliberately does NOT follow the 64 MiB WAL ceiling. PR #137 said
// the two "pair naturally"; they do not — checkpointing is correct at
// any cache size, and the pairing was aesthetic rather than required.
const HistoryCachePragma = "&_pragma=cache_size(-32000)"

// SideStoreCachePragma is the shared budget for events, webhooks,
// contacts, drafts and schedules: 8 MiB each.
//
// Sized from the real files rather than a guess. On wa-personal:
// webhooks.db 7.2 MiB, events.db 3.4 MiB, contacts.db 0.8 MiB, and
// drafts.db / scheduled.db under 0.1 MiB. 8 MiB therefore still holds
// the LARGEST of them entirely, with the rest far inside it — while
// dropping the five stores' configured budget from 160 MiB to 40 MiB.
//
// These stores have no p99 gate because none of them is on a latency
// path that has one; the justification is that the cache still covers
// the whole database, which is the only thing the old 32 MiB bought.
const SideStoreCachePragma = "&_pragma=cache_size(-8000)"

// TotalConfiguredCacheKiB is the sum of every store's page cache, in KiB:
// 32 MiB for messages.db plus 8 MiB for each of the five side stores.
// MemoryLimit subtracts it from the cgroup limit because this memory is
// resident but invisible to the Go GC, and TestTotalConfiguredCacheWithinBudget
// checks it against the pragmas above so the two cannot drift.
const TotalConfiguredCacheKiB = 32000 + 5*8000

// SideStoreDSN builds the connection string every side store uses:
// events, webhooks, contacts, drafts and schedules.
//
// All five were byte-identical apart from the path, and the clone
// ratchet flagged it the moment #359 made them share a cache constant —
// correctly. Five copies of a pragma set is five places to forget a
// pragma, and a store that silently missed `busy_timeout` or
// `journal_mode(WAL)` would look fine until it deadlocked under
// concurrency.
//
// messages.db keeps its own DSN: it carries a different cache budget and
// pragmas this set does not have, so folding it in here would mean
// parameterising away the thing that makes it different.
func SideStoreDSN(dbPath string) string {
	return "file:" + dbPath +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=busy_timeout(5000)" +
		SideStoreCachePragma +
		"&_pragma=temp_store(MEMORY)" +
		"&_txlock=immediate"
}
