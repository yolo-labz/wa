# Changelog

All notable changes to this project are documented here.
## [1.2.1] - 2026-04-21


### Bug Fixes
- Wire MediaStore + feature-017 ports in wad
- Target Formula/ directory for homebrew tap publication (#28)
## [1.2.0] - 2026-04-21


### Bug Fixes
- Dereference tag-object SHAs to commit SHAs (scorecard imposter) (#26)
- Round-3 main-workflow repair (scorecard commit SHA, repro buildvcs) (#25)
- Round-2 main-workflow repair (homebrew skip, scorecard env) (#24)
- Repair post-merge main workflows (Reproducibility, Scorecard) (#23)

### Chore
- Regenerate CHANGELOG.md for v1.2.0 (#27)

### Features
- Agent experience three-tier release (feature 017) (#22)
## [1.1.0] - 2026-04-13


### Bug Fixes
- Wrap []byte params as json.RawMessage to prevent base64 encoding (#21)
- Enable history sync by setting ManualHistorySyncDownload=false (#20)

### Chore
- Update CHANGELOG.md for v1.1.0

### Features
- History sync and message persistence (feature 009) (#18)

### Refactor
- Code quality audit & modernization (feature 016) (#19)
## [1.0.1] - 2026-04-13


### Bug Fixes
- Enable WAL mode + busy_timeout on session.db (#17)
- GOTOOLCHAIN=local so nix build accepts nixpkgs Go 1.26.1
## [1.0.0] - 2026-04-12


### Features
- Wa — WhatsApp automation CLI + daemon

