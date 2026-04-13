# Changelog

All notable changes to this project are documented here.
## [1.1.0] - 2026-04-13


### Bug Fixes
- Wrap []byte params as json.RawMessage to prevent base64 encoding (#21)
- Enable history sync by setting ManualHistorySyncDownload=false (#20)

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

