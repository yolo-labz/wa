# wa benchmarks (roadmap 2.2)

Reproducible numbers for the claims the README makes. Two kinds:

1. **Idle RSS** — what one always-on session costs:

   ```bash
   ./bench/idle-rss.sh
   ```

   Builds `wad` from the working tree, boots it unpaired into a
   throwaway XDG root, samples `VmRSS` after 5 s.

2. **Hot-path microbenchmarks** — daemon internals with the in-memory
   adapter, so they reproduce without a paired WhatsApp account
   (protocol round-trips deliberately excluded):

   ```bash
   go test ./internal/app/ -run xxx -bench 'BenchmarkEventFanout|BenchmarkChannelWrap|BenchmarkDraftCreate' -benchmem
   go test ./cmd/wad/     -run xxx -bench BenchmarkDispatcherStatus -benchmem
   ```

## Baseline numbers (single machine, 10/06/2026)

Measured 10/06/2026, commit `2f80ed3`, Go 1.26.3, Linux 6.12,
Xeon E5-2699 v4 @ 2.20 GHz (benchmarks are single-goroutine). These are
the **baseline** the CI thresholds in the next section are derived from —
single-machine references, not a controlled lab, and the numbers CI
guards today sit above them by the documented slack factors.

| Metric | Result | Meaning |
|---|---|---|
| `wad` idle RSS | **32 MiB** | one paired-session daemon, REST+MCP surface loaded |
| `BenchmarkDispatcherStatus` | 314 ns/op, 48 B/op | full method round-trip floor (~3 M req/s ceiling) |
| `BenchmarkEventFanout` | 3.2 µs/op, 1.3 KB/op | per-event cost each SSE/webhook/MCP subscriber pays (~310 k events/s) |
| `BenchmarkChannelWrap` | 2.4 µs/op | FR-005a prompt-injection envelope per inbound message |
| `BenchmarkDraftCreate` | 4.1 µs/op | MCP draft-gate primitive (store I/O excluded) |

Context for the RSS number, from competitors' own documentation: WAHA
sizes its WEBJS engine at ~20 GB RAM per 50 sessions (~400 MB each)
and its Go engine at 500+ sessions per 4 GB; Evolution API requires
external PostgreSQL + Redis before the first session. `wa` is a single
~12 MB static binary with embedded SQLite.

Numbers are single-machine references, not a controlled lab: rerun the
commands above on your hardware before quoting them anywhere new.

## CI job (bench.yml)

`.github/workflows/bench.yml` runs **both harnesses on every PR and on
every push to main**: it builds `wad` from the working tree, runs
`./bench/idle-rss.sh` (unpaired boot, no account) plus the two `go test
-bench` invocations above with `-count=1 -benchmem -vet=off`, compares
every result against the committed thresholds below, and fails the job
on regression. Raw output (`idle-rss.txt`, `bench-app.txt`,
`bench-cmd.txt`) is uploaded as the `bench-results` artifact
(retention 7 days) — download it from the Actions run to quote numbers.

The job is **not a required check** and carries **no secrets and no
write permission**: it is a trend signal, not a merge gate — the
benchmarks share two self-hosted runners with the rest of CI, so
their absolute values are noisy by design.

## Threshold policy

Every threshold = 10/06/2026 baseline × documented slack factor
(baseline commit `2f80ed3`, see table above):

| Metric | Baseline | Factor | CI threshold |
|---|---|---|---|
| `wad` idle RSS | 32 MiB | 2.0× | ≤ 64 MiB |
| `BenchmarkDispatcherStatus` | 314 ns/op | 3.2× | ≤ 1005 ns/op |
| `BenchmarkEventFanout` | 3.2 µs/op | 3.1× | ≤ 10000 ns/op |
| `BenchmarkChannelWrap` | 2.4 µs/op | 3.3× | ≤ 8000 ns/op |
| `BenchmarkDraftCreate` | 4.1 µs/op | 3.2× | ≤ 13120 ns/op |

Factors: the benchmarks are single-goroutine microbenchmarks sharing
two self-hosted runners with the rest of CI — interleaved jobs shift
ns/op by tens of percent between runs (a same-machine rerun on the
baseline CPU with Go 1.26.5 measured 26 % above the 10/06 table). 2×
on RSS and 3× on ns/op is the floor for a gate that catches material
regressions (a doubling of the daemon's memory footprint, a hot path
that triples) without flaking on runner noise. A threshold at factor
1.0 would be a tautology and is rejected by review policy.

The thresholds live as env vars at the top of `bench.yml` with their
derivation commented — updating them is a one-line change in the same
PR that moves the needle. When you raise a threshold, bump the
baseline table above to the new reference run (commit + date) so the
two stay traceable.
