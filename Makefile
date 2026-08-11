.PHONY: test vet lint lint-cross-platform verify-named-types sonar-local sonar-local-up sonar-local-down \
        ci-local ci-actionlint ci-zizmor ci-gitleaks ci-osv ci-test-race ci-repro \
        bench bench-canonical pgo-capture goreleaser-check

# Local SonarQube — see docker-compose.sonar.yml.
SONAR_LOCAL_URL ?= http://localhost:9000
SONAR_LOCAL_TOKEN ?= $(shell cat .sonar-local-token 2>/dev/null)

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

# lint-cross-platform runs golangci-lint twice — once with GOOS=linux,
# once with GOOS=darwin — so build-tag-gated files (`//go:build linux`,
# `//go:build darwin`) are linted in both contexts.
#
# Why: PR #95 hit a CI-only failure because `cmd/wad/service_linux.go`
# had a stale `//nolint:gosec` that local lint on darwin couldn't see
# (the file was excluded by the build tag). Future PRs should run this
# target before pushing if they touch any `service_*.go` or other
# GOOS-gated files.
lint-cross-platform:
	@echo "→ golangci-lint GOOS=linux"
	GOOS=linux golangci-lint run ./...
	@echo "→ golangci-lint GOOS=darwin"
	GOOS=darwin golangci-lint run ./...

# verify-named-types runs the build-tag-gated cross-type-assignment test
# in internal/domain/ids_compile_gate.go and asserts the build FAILS with
# exactly four "cannot use ... as ..." errors. This is the mechanical
# proof that MessageID and EventID are distinct named types and not
# aliases — see contracts/domain.md §ids.go and spec CHK023.
verify-named-types:
	@out=$$(go build -tags never ./internal/domain/ 2>&1); \
	count=$$(echo "$$out" | grep -c 'cannot use'); \
	if [ "$$count" != "4" ]; then \
		echo "verify-named-types: expected 4 'cannot use' errors, got $$count"; \
		echo "$$out"; \
		exit 1; \
	fi; \
	echo "verify-named-types: OK (4 expected compile errors)"

# sonar-local-up boots the docker-compose SonarQube stack and waits for
# the API status to flip to UP. Idempotent — re-running on a healthy
# stack is a no-op.
sonar-local-up:
	@docker compose -f docker-compose.sonar.yml up -d
	@echo "Waiting for SonarQube /api/system/status=UP ..."
	@until curl -fs $(SONAR_LOCAL_URL)/api/system/status | grep -q '"status":"UP"'; do sleep 3; done
	@echo "SonarQube is UP at $(SONAR_LOCAL_URL)"
	@echo "First boot: log in at $(SONAR_LOCAL_URL) (admin/admin), rotate the password,"
	@echo "create a project token at My Account > Security, and write it to .sonar-local-token"

# sonar-local runs a full scan against the local stack. Same scanner
# arguments as the CI job, only `sonar.host.url` and the token differ.
sonar-local: sonar-local-up
	@test -n "$(SONAR_LOCAL_TOKEN)" || (echo "Set SONAR_LOCAL_TOKEN or write a token to .sonar-local-token"; exit 1)
	@command -v sonar-scanner >/dev/null || (echo "sonar-scanner not installed: brew install sonar-scanner"; exit 1)
	CGO_ENABLED=0 go test -race -shuffle=on -count=1 -coverprofile=cover.out -json ./... > test-report.json || true
	sonar-scanner \
	  -Dsonar.host.url=$(SONAR_LOCAL_URL) \
	  -Dsonar.token=$(SONAR_LOCAL_TOKEN) \
	  -Dsonar.go.coverage.reportPaths=cover.out \
	  -Dsonar.go.tests.reportPaths=test-report.json
	@BRANCH=$$(git branch --show-current); \
	 echo "Quality gate: $(SONAR_LOCAL_URL)/dashboard?id=yolo-labz_wa&branch=$$BRANCH"

sonar-local-down:
	@docker compose -f docker-compose.sonar.yml down

# ----------------------------------------------------------------------
# ci-local — run every GitHub Actions gate on the local host. Mirrors
# the required-check matrix on `main` so a contributor can pre-flight
# every change before pushing. Each sub-target soft-skips if its tool
# is missing — install via `./scripts/install-dev-tools.sh`.
# ----------------------------------------------------------------------

ci-local: ci-actionlint ci-zizmor ci-gitleaks ci-test-race goreleaser-check
	@echo
	@echo "ci-local: ALL GREEN ✓"
	@echo "  Optional next steps:"
	@echo "    make ci-osv       # OSV-Scanner dep vuln check"
	@echo "    make ci-repro     # double-build byte-identity"
	@echo "    LOCAL_SONAR=1 make sonar-local"

ci-actionlint:
	@if command -v actionlint >/dev/null 2>&1; then \
	  echo "→ actionlint"; actionlint -color; \
	else \
	  echo "(skipping actionlint; install via ./scripts/install-dev-tools.sh)"; \
	fi

ci-zizmor:
	@if command -v zizmor >/dev/null 2>&1; then \
	  echo "→ zizmor (auditor)"; zizmor --persona=auditor --format=plain .github/workflows/ || true; \
	else \
	  echo "(skipping zizmor; install via ./scripts/install-dev-tools.sh)"; \
	fi

ci-gitleaks:
	@if command -v gitleaks >/dev/null 2>&1; then \
	  echo "→ gitleaks"; gitleaks git --redact --no-banner --pre-commit; \
	else \
	  echo "(skipping gitleaks; install via ./scripts/install-dev-tools.sh)"; \
	fi

ci-osv:
	@if command -v osv-scanner >/dev/null 2>&1; then \
	  echo "→ OSV-Scanner"; osv-scanner --recursive .; \
	else \
	  echo "(skipping osv-scanner; install via 'brew install osv-scanner' or curl release)"; \
	fi

ci-test-race:
	@echo "→ go test -race -shuffle=on"
	go test -race -shuffle=on -count=1 ./...

# goreleaser-check validates `.goreleaser.yaml` syntax + schema in
# under a second, way faster than the snapshot build the
# Reproducibility workflow does. Catches schema drift the moment a
# breaking deprecation lands so a botched commit doesn't surface only
# at tag-push time. Soft-skip if goreleaser is not installed locally.
goreleaser-check:
	@if command -v goreleaser >/dev/null 2>&1; then \
	  echo "→ goreleaser check"; goreleaser check; \
	else \
	  echo "(skipping goreleaser check; install via: brew install goreleaser)"; \
	fi

# ci-repro double-builds via goreleaser snapshot and asserts byte-identity
# of the resulting tarballs. Mirrors the Reproducibility CI workflow.
ci-repro:
	@set -eu; \
	 command -v goreleaser >/dev/null 2>&1 || \
	   { echo "goreleaser not installed: brew install goreleaser"; exit 1; }; \
	 tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' 0; \
	 export GOCACHE="$$tmp/go-build"; \
	 SOURCE_DATE_EPOCH=$$(git log -1 --format=%ct); export SOURCE_DATE_EPOCH; \
	 first="$$tmp/repro-build-1-checksums.txt"; \
	 second="$$tmp/repro-build-2-checksums.txt"; \
	 echo "→ goreleaser snapshot — first build"; \
	 goreleaser release --snapshot --clean --skip=sign,homebrew >/dev/null; \
	 cp dist/checksums.txt "$$first"; \
	 echo "→ goreleaser snapshot — second build (cold go cache)"; \
	 go clean -cache; \
	 goreleaser release --snapshot --clean --skip=sign,homebrew >/dev/null; \
	 cp dist/checksums.txt "$$second"; \
	 digest1=$$(sha256sum "$$first"); sha1=$${digest1%% *}; \
	 digest2=$$(sha256sum "$$second"); sha2=$${digest2%% *}; \
	 echo "build1: $$sha1"; echo "build2: $$sha2"; \
	 if [ "$$sha1" != "$$sha2" ]; then \
	   echo "FAIL: checksums differ"; diff "$$first" "$$second" || true; exit 1; \
	 fi; \
	 echo "ci-repro: PASS — byte-identical builds"

# ----------------------------------------------------------------------
# bench / pgo — performance helpers. The canonicaljson hot-path baseline
# is at ~/Documents/Notes/wa-improvement-loop/profiling/baseline-002.md.
# ----------------------------------------------------------------------

bench: bench-canonical
	@echo "All benches done."

bench-canonical:
	@echo "→ canonicaljson bench (count=10, benchtime=1s)"
	go test -run=NONE -bench='Canonical' -benchmem -count=10 ./internal/app/

# pgo-capture runs every package-level benchmark, captures CPU profiles,
# and merges them into cmd/wad/default.pgo. The Go toolchain auto-
# detects default.pgo next to main.go and uses it for PGO when building
# ./cmd/wad/... — no flag needed. Re-run quarterly to keep the profile
# representative as new hot paths emerge.
pgo-capture:
	@bash scripts/pgo-capture.sh
