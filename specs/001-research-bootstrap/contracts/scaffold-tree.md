# Contract: directory scaffold and governance file set

**Applies to**: the working tree at the repository root
**Enforced by**: spec FR-005, FR-006, FR-007, FR-008, FR-009, FR-010, FR-015; checklist CHK029, CHK030, CHK031.

This contract pins the on-disk shape of the repository before any Go source file is written. It exists so that feature 002 (`domain-and-ports`) can `git checkout` a fresh clone and start typing code into known directories without first having to negotiate layout.

## Required directories

Every directory below MUST exist and MUST contain a `.gitkeep` placeholder (so empty dirs survive git) until a real file replaces the placeholder.

```text
cmd/
├── wa/.gitkeep
└── wad/.gitkeep
internal/
├── domain/.gitkeep
├── app/.gitkeep
└── adapters/
    ├── primary/
    │   └── socket/.gitkeep
    └── secondary/
        ├── whatsmeow/.gitkeep
        ├── sqlitestore/.gitkeep
        ├── memory/.gitkeep
        └── slogaudit/.gitkeep
```

**Verification**:

```sh
required=(
  cmd/wa cmd/wad
  internal/domain internal/app
  internal/adapters/primary/socket
  internal/adapters/secondary/whatsmeow
  internal/adapters/secondary/sqlitestore
  internal/adapters/secondary/memory
  internal/adapters/secondary/slogaudit
)
for d in "${required[@]}"; do
  test -d "$d" || { echo "FAIL: missing $d"; exit 1; }
  test -f "$d/.gitkeep" || { echo "FAIL: missing $d/.gitkeep"; exit 1; }
done
```

## Required governance files at root

| File | Required content (excerpt) | Size guidance | Source of truth |
|---|---|---|---|
| `LICENSE` | First non-blank line is `Apache License` | ~202 lines | <https://www.apache.org/licenses/LICENSE-2.0.txt> |
| `README.md` | < 200 lines, mentions `wa`, `wad`, `whatsmeow`, `Apache-2.0`; links to `CLAUDE.md` and `specs/001-research-bootstrap/` | < 200 lines | hand-written this feature |
| `SECURITY.md` | Has a `## Threat model` section enumerating ≥ 5 threats T1..Tn with mitigations | < 200 lines | hand-written this feature |
| `CLAUDE.md` | Contains "Apache-2.0" in the locked-decisions table; contains "Channels" in the plugin section; references `specs/001-research-bootstrap/research.md` | ~250 lines | hand-written, blueprint |
| `.gitignore` | Includes `session.db`, `/wa`, `/wad`, `/dist/`, `*.swp`, `.DS_Store`, `.direnv/`, `result*` | < 100 lines | hand-written |
| `.editorconfig` | Sets `end_of_line = lf`, `*.go` uses tabs | < 50 lines | hand-written |
| `go.mod` | First line is `module github.com/yolo-labz/wa`; `go` directive present | 3 lines | `go mod init` output |

**Verification**:

```sh
for f in LICENSE README.md SECURITY.md CLAUDE.md .gitignore .editorconfig go.mod; do
  test -f "$f" || { echo "FAIL: missing $f"; exit 1; }
done

head -1 LICENSE | grep -q 'Apache License' || { echo "FAIL: LICENSE not Apache"; exit 1; }
head -1 go.mod | grep -qF 'module github.com/yolo-labz/wa' || { echo "FAIL: go.mod path"; exit 1; }
grep -q 'Apache-2.0' CLAUDE.md || { echo "FAIL: CLAUDE.md license row stale"; exit 1; }
grep -q 'Channels' CLAUDE.md   || { echo "FAIL: CLAUDE.md missing Channels clarification"; exit 1; }
grep -q '## Threat model' SECURITY.md || { echo "FAIL: SECURITY.md missing threat model"; exit 1; }
```

## Forbidden contents at v0

| Pattern | Why forbidden | Verification |
|---|---|---|
| Any `*.go` file under `cmd/` or `internal/` | Out of spec scope (FR-015); features 002+ write source | `git ls-files cmd internal \| grep -E '\.go$' \| wc -l` must equal 0 |
| Built binaries `/wa`, `/wad`, `/dist/` | Distribution is feature 006; no committed binaries ever | `git ls-files \| grep -E '^(wa\|wad\|dist/)' \| wc -l` must equal 0 |
| `vendor/` directory | Module proxy is the source of truth; vendoring is a feature 006 packaging concern at most | `test ! -d vendor` |
| Session database `session.db*` | Contains Signal Protocol ratchets; `chmod 0600` and gitignored | `git ls-files \| grep session.db \| wc -l` must equal 0 |
| Apple notarization secrets `*.p8`, `*.p12` | Live in GitHub Actions secrets, never on disk | same |

## Go module sanity

```sh
go mod tidy && go vet ./...
```

This MUST exit 0. With zero `.go` files it will print `go: warning: "./..." matched no packages` and `no packages to vet` — both are warnings, not errors. The exit code is the gate, not the warnings.

## State transitions for the scaffold

```text
empty directory
   │ mkdir -p cmd/{wa,wad} internal/{domain,app,adapters/{primary/socket,secondary/{whatsmeow,sqlitestore,memory,slogaudit}}}
   ▼
directories present, no files
   │ touch <dir>/.gitkeep for each
   ▼
directories with placeholders
   │ go mod init github.com/yolo-labz/wa
   │ write LICENSE / README.md / SECURITY.md / .gitignore / .editorconfig
   ▼
governance complete  ◀── current state
   │ feature 002: replace internal/domain/.gitkeep with jid.go, contact.go, ...
   ▼
domain types landed (out of scope here)
```

## Why .gitkeep instead of an empty `doc.go`

A `doc.go` would be a real Go file, would invite a package comment that contradicts CLAUDE.md, and would force `go vet` to do real work. `.gitkeep` is conventional, ignored by Go, and unambiguous about intent: "this directory is intentionally empty until feature N populates it." When the first real file lands in a directory, that feature's commit deletes the `.gitkeep` in the same diff.
