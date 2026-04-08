# 2026 Architecture Audit: whatsmeow Secondary Adapter

## 1. whatsmeow library choice — CONFIRMED CORRECT

`go.mau.fi/whatsmeow` remains the only production-grade Go WhatsApp multi-device library in 2026. The repo at [github.com/tulir/whatsmeow](https://github.com/tulir/whatsmeow) shows continuous commits (multiple per week), MPL-2.0, ~3k+ stars, and is funded via Beeper. No fork or competitor has emerged; `Rhymen/go-whatsapp` (legacy web protocol) has been archived for years. The project's commit-pinning strategy via `go.sum` pseudo-version is correct because upstream still publishes no semver tags. No action.

## 2. CGO-free SQLite driver — MINOR IMPROVEMENT (consider, do not replace)

`modernc.org/sqlite` (transpiled from SQLite C via `ccgo`) is still the default CGO-free choice and tracks SQLite upstream within weeks. The newer contender [`ncruces/go-sqlite3`](https://github.com/ncruces/go-sqlite3) compiles SQLite to WASM and runs it via `wazero`; benchmarks in its README show it is often 2-4x faster than `modernc.org/sqlite` on read-heavy workloads and the binary footprint is smaller. However: (a) ncruces uses a different driver API surface and FTS5 is opt-in via build tag, (b) modernc has far more production mileage (used by `caddy`, `gitea`, etc.), (c) the perf delta is irrelevant for ~10 RPS daemon traffic. **Verdict: keep `modernc.org/sqlite`**, but `research.md` should explicitly cite the ncruces comparison as the rejected alternative (ADR completeness rule #19).

## 3. FTS5 in modernc.org/sqlite — REVISIT

FTS5 has been compiled in by default in `modernc.org/sqlite` since v1.21 (early 2023); the `sqlite_fts5` tag is **no longer required**. Verify against the current `go.mod` minor. SQLite 3.35+ introduced **`contentless-delete`** FTS5 tables (`content=''` plus `contentless_delete=1`), which let you `DELETE` from a contentless FTS index without the trigger-on-content-table dance. SQLite 3.44 (late 2023) made it the recommended pattern for write-heavy ingestion paths. The plan's "trigger-based content-table sync" is the older, heavier pattern. **Action: spec should pick one explicitly with rationale**, or note `contentless_delete=1` as the considered-and-rejected alternative. See [sqlite.org/fts5.html#contentless_delete_tables](https://sqlite.org/fts5.html#contentless_delete_tables).

## 4. flock primitive — REPLACE

`syscall.Flock` is darwin/linux-only and Go is steadily deprecating direct `syscall` usage in favour of `golang.org/x/sys/unix`. More importantly, [`rogpeppe/go-internal/lockedfile`](https://pkg.go.dev/github.com/rogpeppe/go-internal/lockedfile) is the exact code the Go toolchain itself uses for module-cache locking — it handles the darwin `O_EXLOCK` quirks, the linux `flock` semantics, and Windows `LockFileEx` if cross-compilation ever matters. [`gofrs/flock`](https://github.com/gofrs/flock) is a thinner wrapper but lacks the Go-toolchain pedigree. **Recommendation: use `rogpeppe/go-internal/lockedfile`** — it is already a transitive dep through `testscript` (which the v0 testing strategy mandates).

## 5. sync.Map for request-ID-keyed channels — MINOR IMPROVEMENT

Go 1.22's Swiss-table runtime map is a general-map perf win but does not change `sync.Map`'s semantics. For the on-demand history routing (write-once, read-once, delete) `sync.Map` is fine. [`puzpuzpuz/xsync`](https://github.com/puzpuzpuz/xsync) `MapOf[K,V]` is typed (no `interface{}` boxing), 2-5x faster, and is now widely adopted (used by `cockroachdb`, `dgraph`). For ~tens of in-flight requests the perf is irrelevant; the **type safety** is the real win and removes a class of cast bugs. **Recommendation: `xsync.MapOf[string, chan historyResponse]`** for clarity, not speed.

## 6. Mocks: hand-rolled vs mockery vs gomock vs testify — CONFIRMED CORRECT

Hand-rolled fakes against a small extracted interface remain the 2026 idiom for hexagonal Go code (cf. ThreeDotsLabs wild-workouts, Watermill internals). [`go.uber.org/mock`](https://github.com/uber-go/mock) is the maintained fork of `golang/mock` since 2023 and is fine for gRPC-shaped surfaces, but generated mocks become a maintenance burden when the interface is small and stable. `mockery v3` (2025) added type-parameter support but the same critique applies. The CLAUDE.md anti-pattern #5 ("Mock-everything tests. Prefer in-memory fakes") is explicit: keep hand-rolled. No action.

## 7. //go:embed for SQL schema — CONFIRMED CORRECT

`//go:embed schema.sql` remains canonical. `sqlc` (next item) does not subsume embedding for migrations.

## 8. sqlc vs hand-rolled — MINOR IMPROVEMENT (lean keep hand-rolled)

[`sqlc`](https://sqlc.dev) generates type-safe Go from `.sql` files and supports `modernc.org/sqlite` via the `sql/database` driver since v1.20 (2024). For a ~10-query adapter, sqlc adds a build-step dependency (`sqlc generate` in `lefthook` and CI), a `sqlc.yaml`, and removes ~150 LoC of `rows.Scan` boilerplate. The break-even is usually ~20 queries. **Verdict: hand-rolled is defensible**, but `research.md` should name sqlc as the rejected alternative with reason ("query count below break-even, avoids extra codegen toolchain").

## 9. The 12 whatsmeow client flags from FR-009 — REVISIT (verify against HEAD)

These flag names are correct as of the late-2025 whatsmeow surface, but the upstream is unversioned and renames happen. Concretely audit by `grep`-ing the pinned commit:
- `EnableDecryptedEventBuffer`, `ManualHistorySyncDownload`, `SendReportingTokens`, `AutomaticMessageRerequestFromPhone`, `SynchronousAck` — all confirmed present in late-2025 commits.
- `AddEventHandlerWithSuccessStatus` — added 2024, stable.
- `InitialAutoReconnect`, `UseRetryMessageStore` — present, stable.

**Action: `research.md` must cite the exact pinned commit SHA and a `grep` snippet** so a future Renovate bump can re-verify in one command. This is the only way to defend rule #11 (cite file:line). Reference upstream: [github.com/tulir/whatsmeow/blob/main/client.go](https://github.com/tulir/whatsmeow/blob/main/client.go).

## 10. Pairing UX / terminal QR — CONFIRMED CORRECT

[`mdp/qrterminal/v3`](https://github.com/mdp/qrterminal) `GenerateHalfBlock` remains the standard half-block renderer and is what `mautrix-whatsapp` and `signal-cli`-adjacent tools use. `skip2/go-qrcode` produces images, not terminal output. `boombuler/barcode` is a generic barcode library, overkill. No action.

## 11. errors.Join — MINOR IMPROVEMENT

`errors.Join` (Go 1.20) is exactly right for the dual-flock `Close()` case: when shutting down, both `session.db` and `messages.db` flocks must be released and you want to surface both failures. Current `fmt.Errorf("%w: …")` only carries one. **Recommendation: use `errors.Join` in `Close()`** and any cleanup paths releasing multiple resources. The single-error wrap stays correct on the happy paths. See [pkg.go.dev/errors#Join](https://pkg.go.dev/errors#Join).

## 12. testing/synctest — MINOR IMPROVEMENT

[`testing/synctest`](https://pkg.go.dev/testing/synctest) graduated from experiment to stable in **Go 1.24** (Feb 2025) and is the canonical way to test code with `time.After`, `time.Sleep`, `context.WithTimeout`. The 30-second on-demand history timeout test is a textbook synctest use case: replaces a real 30-second sleep with virtual time, makes the test deterministic and ~instant. **Action: rewrite that test under `synctest.Run`**. Requires Go 1.24+, which the project's `go 1.26.1` satisfies.

## 13. slog for whatsmeow's logger — CONFIRMED CORRECT (verify API name)

whatsmeow's `waLog.Logger` interface is its own minimal type (`Debugf/Infof/Warnf/Errorf/Sub`). It does **not** ship a `waLog.Slog(...)` constructor as of the late-2025 commits — verify against the pinned SHA. The right pattern is a small adapter `type slogWALog struct{ *slog.Logger }` implementing the four `*f` methods by delegating to `slog.LogAttrs` with formatted messages. CLAUDE.md already mandates `log/slog` + `lmittmann/tint`; this just bridges them. **Action: data-model.md should name the bridge type explicitly** so it is not invented twice.

## 14. Renovate vs Dependabot — CONFIRMED CORRECT

Dependabot still treats Go pseudo-versions as opaque strings and does not fetch upstream changelogs across commit ranges. Renovate's `fetchChangeLogs: branch` and per-package schedules (already configured per CLAUDE.md governance row) remain the only way to get a meaningful PR body for a whatsmeow bump. No action.

## 15. Two-database split (session.db + messages.db) — CONFIRMED CORRECT

Two physical files with two flocks is what `mautrix-whatsapp` does and it has six-figure-scale production miles. `ATTACH DATABASE` would let you query across both from one connection but: (a) it merges the locking domains, defeating the point of a separate flock for the message history, (b) it complicates the hexagonal split (two adapters → one connection holder), (c) WAL mode interacts oddly with attached databases. **Keep two files**. Reference: [github.com/mautrix/whatsapp](https://github.com/mautrix/whatsapp) `database/` layout.

---

## Checklist items for `/speckit:checklist architecture.md`

- Is `go.mau.fi/whatsmeow` chosen with evidence that no superior multi-device WhatsApp Go library exists in 2026? [Coverage] [research.md §library]
- Is the pinned whatsmeow commit SHA documented in `research.md` together with a one-line `grep` proving the 12 client flags exist on that SHA? [Coverage] [research.md §FR-009]
- Is `modernc.org/sqlite` chosen with `ncruces/go-sqlite3` named as the rejected alternative and the rejection reason cited? [ADR completeness] [research.md §sqlite]
- Is the FTS5 strategy explicitly either "trigger-synced content table" OR "contentless_delete=1", with the other named as the rejected alternative? [Coverage] [data-model.md §fts5]
- Is the file-locking primitive chosen with `rogpeppe/go-internal/lockedfile` and `gofrs/flock` named as rejected alternatives? [Coverage] [research.md §flock]
- Is the in-flight request map chosen (`sync.Map` vs `xsync.MapOf`) with the type-safety trade-off documented? [Coverage] [plan.md §history-routing]
- Is the test-double strategy ("hand-rolled fake against extracted interface") defended against `mockery`, `go.uber.org/mock`, and `testify/mock` with current rationale? [Coverage] [research.md §testing]
- Is `sqlc` named as a rejected alternative for the ~10-query adapter with the break-even reason cited? [ADR completeness] [research.md §sql]
- Does `Close()` use `errors.Join` to surface both flock-release failures rather than dropping one? [Correctness] [contracts/whatsmeow-adapter.md §lifecycle]
- Is the 30-second on-demand history timeout test written under `testing/synctest` (Go 1.24+) rather than wall-clock sleep? [Determinism] [contracts/historystore.md §on-demand]
- Is the `waLog.Logger` → `log/slog` bridge type named in `data-model.md` so it is not reinvented per file? [Coverage] [data-model.md §logging]
- Is the two-database split (`session.db` + `messages.db`) defended against the single-file `ATTACH DATABASE` alternative with the locking-domain rationale? [ADR completeness] [research.md §storage]
- Does `research.md` cite the mautrix-whatsapp `database/` layout as prior art for the two-flock pattern? [Evidence] [research.md §storage]
- Is `mdp/qrterminal/v3` chosen with at least one rejected terminal-QR alternative named? [ADR completeness] [research.md §pairing]
- Does the Renovate config still carry the `whatsmeow` package rule with `fetchChangeLogs: branch` and is that documented in `research.md`? [Coverage] [research.md §governance]

Sources:
- [github.com/tulir/whatsmeow](https://github.com/tulir/whatsmeow)
- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)
- [github.com/ncruces/go-sqlite3](https://github.com/ncruces/go-sqlite3)
- [sqlite.org/fts5.html#contentless_delete_tables](https://sqlite.org/fts5.html#contentless_delete_tables)
- [pkg.go.dev/github.com/rogpeppe/go-internal/lockedfile](https://pkg.go.dev/github.com/rogpeppe/go-internal/lockedfile)
- [github.com/gofrs/flock](https://github.com/gofrs/flock)
- [github.com/puzpuzpuz/xsync](https://github.com/puzpuzpuz/xsync)
- [github.com/uber-go/mock](https://github.com/uber-go/mock)
- [sqlc.dev](https://sqlc.dev)
- [pkg.go.dev/errors#Join](https://pkg.go.dev/errors#Join)
- [pkg.go.dev/testing/synctest](https://pkg.go.dev/testing/synctest)
- [github.com/mdp/qrterminal](https://github.com/mdp/qrterminal)
- [github.com/mautrix/whatsapp](https://github.com/mautrix/whatsapp)
