# Quickstart: Verify feature 001 in five minutes

**Branch**: `001-research-bootstrap` · **Plan**: [`plan.md`](./plan.md) · **Spec**: [`spec.md`](./spec.md)

This is the executable form of [`checklists/requirements.md`](./checklists/requirements.md) items CHK026–CHK033 and the validation procedures in [`contracts/`](./contracts/). A fresh contributor with `git`, `gh`, and Go installed should be able to clone, run every block below, and finish with green output in under five minutes.

## 0. Prerequisites

```sh
command -v git  >/dev/null || { echo "install git"; exit 1; }
command -v gh   >/dev/null || { echo "install gh and run 'gh auth login'"; exit 1; }
command -v go   >/dev/null || { echo "install Go (>=1.22)"; exit 1; }
command -v jq   >/dev/null || { echo "install jq"; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "run 'gh auth login'"; exit 1; }
```

## 1. Clone

```sh
git clone https://github.com/yolo-labz/wa.git /tmp/wa-quickstart
cd /tmp/wa-quickstart
git fetch origin 001-research-bootstrap
git checkout 001-research-bootstrap
```

Expected: branch checks out cleanly, working tree is clean (`git status -s` prints nothing).

## 2. Verify the repository state contract

```sh
gh repo view yolo-labz/wa --json visibility,defaultBranchRef,description \
  | jq -e '.visibility=="PUBLIC" and .defaultBranchRef.name=="main"'
gh api repos/yolo-labz/wa/branches \
  | jq -e 'map(.name) | contains(["main","001-research-bootstrap"])'
gh api repos/yolo-labz/wa/license | jq -e '.license.spdx_id=="apache-2.0"'
```

Expected: each `jq -e` exits 0 and prints `true`. Validates [`contracts/repository-state.md`](./contracts/repository-state.md) and CHK028.

## 3. Verify the scaffold contract

```sh
required_dirs=(
  cmd/wa cmd/wad
  internal/domain internal/app
  internal/adapters/primary/socket
  internal/adapters/secondary/whatsmeow
  internal/adapters/secondary/sqlitestore
  internal/adapters/secondary/memory
  internal/adapters/secondary/slogaudit
)
for d in "${required_dirs[@]}"; do
  test -d "$d" -a -f "$d/.gitkeep" || { echo "FAIL: $d"; exit 1; }
done

required_files=(LICENSE README.md SECURITY.md CLAUDE.md .gitignore .editorconfig go.mod)
for f in "${required_files[@]}"; do
  test -f "$f" || { echo "FAIL: missing $f"; exit 1; }
done

head -1 LICENSE | grep -q 'Apache License'
head -1 go.mod  | grep -qF 'module github.com/yolo-labz/wa'
grep -q 'Apache-2.0' CLAUDE.md
grep -q 'Channels'   CLAUDE.md
grep -q '## Threat'  SECURITY.md

# Forbidden contents must be absent
test "$(git ls-files cmd internal | grep -Ev '\.gitkeep$' | wc -l)" -eq 0
test "$(git ls-files | grep -E '^(wa|wad|dist/|vendor/)' | wc -l)" -eq 0
test "$(git ls-files | grep -E '\.(p8|p12|env)$|session\.db' | wc -l)" -eq 0

echo "scaffold ok"
```

Expected: prints `scaffold ok`. Validates [`contracts/scaffold-tree.md`](./contracts/scaffold-tree.md), CHK029, CHK030.

## 4. Verify the Go module is sane

```sh
go mod tidy
go vet ./...
```

Expected: both exit 0. With zero `.go` files you will see `go: warning: "./..." matched no packages` and `no packages to vet` — that is correct, not a failure. Validates CHK031.

## 5. Verify the research.md contract

```sh
spec_md=specs/001-research-bootstrap/research.md

# At least 5 OPEN-Q sections
test "$(grep -c '^## OPEN-Q' "$spec_md")" -ge 5

# Every OPEN-Q section has at least one https citation
awk '
  /^## OPEN-Q/  { if (s && c==0) { print "FAIL: " s; rc=1 }
                  s=$0; c=0; next }
  /https?:\/\// { c++ }
  END           { if (s && c==0) { print "FAIL: " s; rc=1 }
                  exit rc }
' "$spec_md"

# No NEEDS CLARIFICATION markers
! grep -q '\[NEEDS CLARIFICATION' "$spec_md"

# Sources section present and non-empty
grep -A1 '^## Sources' "$spec_md" | grep -q '^- '

echo "research ok"
```

Expected: prints `research ok`. Validates [`contracts/research-md.md`](./contracts/research-md.md), CHK026, CHK027.

## 6. Verify the spec checklist is fully checked

```sh
unchecked=$(grep -c '^- \[ \]' specs/001-research-bootstrap/checklists/requirements.md || true)
test "$unchecked" -eq 0 || { echo "FAIL: $unchecked unchecked items"; exit 1; }
echo "checklist 33/33 ok"
```

Expected: prints `checklist 33/33 ok`. Validates CHK033.

## 7. Read the project in 15 minutes (SC-003 rehearsal)

A fresh reader should now be able to write a credible one-paragraph summary of the project after reading these four files in this order:

```sh
$EDITOR README.md
$EDITOR CLAUDE.md
$EDITOR specs/001-research-bootstrap/spec.md
$EDITOR specs/001-research-bootstrap/research.md
```

If the summary cannot be produced in under 15 minutes, file an issue against feature 001 (`docs: README/CLAUDE.md hard to onboard from`). This is the qualitative half of SC-003 and is not automatable; quickstart cannot test it for you.

## 8. (Optional) Tear down the quickstart clone

```sh
cd / && rm -rf /tmp/wa-quickstart
```

## What this quickstart does NOT cover

- **Building binaries** — there are no `.go` files; `go build` has nothing to do. Reach this step in feature 005 (`wa CLI client`).
- **Running `wad`** — the daemon does not exist yet. Reach this step in feature 004 (`Daemon and JSON-RPC socket`).
- **Pairing a WhatsApp number** — gated behind feature 003 (`whatsmeow secondary adapter`) and requires `wad` to exist.
- **Sending a test message** — gated behind feature 005 and requires a paired number.
- **Running the future CI workflow** — `.github/workflows/ci.yml` does not exist yet; it lands in feature 002 along with the governance dossier files.

This quickstart deliberately stops at "the repository is shaped correctly and the research is complete." Anything beyond that belongs to a later feature's quickstart.
