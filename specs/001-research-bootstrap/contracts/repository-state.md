# Contract: GitHub repository state

**Applies to**: `github.com/yolo-labz/wa`
**Enforced by**: spec FR-004, FR-013; checklist CHK028, CHK032.

This contract pins the remote-state preconditions for declaring feature 001 complete and for unblocking features 002+. Each row is independently verifiable from any machine with `gh` authenticated.

## Required state

| Field | Required value | Verification |
|---|---|---|
| Owner | `yolo-labz` | `gh api orgs/yolo-labz` returns HTTP 200 |
| Name | `wa` | — |
| URL | `https://github.com/yolo-labz/wa` | `gh repo view yolo-labz/wa --json url` |
| Visibility | `PUBLIC` | `gh repo view yolo-labz/wa --json visibility` |
| Default branch | `main` | `gh repo view yolo-labz/wa --json defaultBranchRef` |
| Branches present | at least `main` and `001-research-bootstrap` | `gh api repos/yolo-labz/wa/branches \| jq -r '.[].name'` |
| Description | non-empty, contains the words `WhatsApp` and `CLI` | `gh repo view yolo-labz/wa --json description` |
| License (SPDX) | `Apache-2.0` | `gh api repos/yolo-labz/wa/license \| jq -r .license.spdx_id` |
| Contains LICENSE at root | yes, 202 lines | `git cat-file -p HEAD:LICENSE \| wc -l` (on `main` branch) |
| Contains CLAUDE.md at root | yes, references `Apache-2.0` and `Channels` | `grep -c 'Apache-2.0\|Channels' CLAUDE.md` ≥ 2 |
| Contains spec at known path | `specs/001-research-bootstrap/spec.md` | `git ls-tree -r main --name-only \| grep -F specs/001-research-bootstrap/spec.md` |
| Contains research at known path | `specs/001-research-bootstrap/research.md` | same |
| Active token has `repo` scope | yes | `gh auth status \| grep -F repo` |

## Forbidden state

- **No committed binaries** (`*.exe`, `wa`, `wad`, `dist/`). Verified by: `git ls-files \| grep -E '^(wa\|wad\|dist/)$'` returns empty.
- **No committed session DB or secrets** (`session.db*`, `*.env`, `*.p12`, `*.p8`). Verified by: `git ls-files \| grep -E '\.(db\|env\|p8\|p12)$'` returns empty.
- **No vendored third-party Go source** (`vendor/`). Verified by: `git ls-files \| grep -E '^vendor/'` returns empty.
- **No premature `*.go` files in `internal/` or `cmd/`**. The repo is *intentionally* code-free at v0; the only allowed entries are `.gitkeep` placeholders. Verified by: `git ls-files cmd internal \| grep -v '\.gitkeep$'` returns empty.

## State transitions

```text
nonexistent
   │ gh repo create yolo-labz/wa --public ...
   ▼
created  (visible to gh repo view, no commits)
   │ git push -u origin main
   ▼
populated  (default branch has CLAUDE.md, LICENSE, README)
   │ git push -u origin 001-research-bootstrap
   ▼
feature-branch-pushed  ◀── current state
```

## Edge cases (and what to do)

| Scenario | Action |
|---|---|
| `yolo-labz/wa` already exists | Reuse it; do not create `wa-2`. The `gh repo create` invocation in this feature was idempotent because it is wrapped in a check. |
| Token lacks `repo` scope | Run `gh auth refresh -h github.com -s repo` and retry. Document in plan.md, do not silently downgrade to a personal repo. |
| `yolo-labz` org has been renamed | Surface in research.md as a "Contradicts blueprint" row and ask the maintainer; do not auto-rename. |
| Default branch is anything other than `main` | Run `gh repo edit yolo-labz/wa --default-branch main` and re-verify. The `cmd/wa` binary name being `wa` is a homonym we accept; the *branch* default must be `main`. |
| Branch `001-research-bootstrap` was force-pushed by an outside agent | Do nothing automatically. Surface to the maintainer. Force-pushes to feature branches are not part of this feature's workflow. |

## Verification one-liner

```sh
gh repo view yolo-labz/wa --json visibility,defaultBranchRef,description \
  | jq -e '.visibility=="PUBLIC" and .defaultBranchRef.name=="main"' \
  && gh api repos/yolo-labz/wa/branches \
  | jq -e 'map(.name) | contains(["main","001-research-bootstrap"])'
```

A passing run exits 0 and prints `true` twice. This is the literal "is the repo where we said it would be" check.
