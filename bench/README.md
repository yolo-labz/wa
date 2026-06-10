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

## Reference numbers

Measured 10/06/2026, commit `2f80ed3`, Go 1.26.3, Linux 6.12,
Xeon E5-2699 v4 @ 2.20 GHz (benchmarks are single-goroutine):

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
