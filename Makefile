.PHONY: test vet lint verify-named-types sonar-local sonar-local-up sonar-local-down

# Local SonarQube — see docker-compose.sonar.yml.
SONAR_LOCAL_URL ?= http://localhost:9000
SONAR_LOCAL_TOKEN ?= $(shell cat .sonar-local-token 2>/dev/null)

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

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
