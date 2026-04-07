.PHONY: test vet lint verify-named-types

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
