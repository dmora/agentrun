.PHONY: qa lint test test-race check cover vet tidy-check vulncheck bench fuzz fmt tidy examples-build

# ---- Composite targets ----

qa: tidy-check lint test-race vet vulncheck examples-build  ## Full quality gate

check: lint test  ## Fast check (CI default)

# ---- Linting ----

lint:
	golangci-lint run ./...

# ---- Testing ----

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

# ---- Coverage ----

cover:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

# ---- Static Analysis ----

vet:
	go vet ./...

vulncheck:
	govulncheck ./...

tidy-check:  ## Verify go.mod/go.sum are clean
	@cp go.mod go.mod.bak && \
	(cp go.sum go.sum.bak 2>/dev/null || true) && \
	go mod tidy && \
	diff go.mod go.mod.bak && \
	(diff go.sum go.sum.bak 2>/dev/null || true); \
	STATUS=$$?; \
	rm -f go.mod.bak go.sum.bak; \
	exit $$STATUS

# ---- Benchmarks (separate from qa) ----

bench:
	go test -bench=. -benchmem ./...

# ---- Fuzz (separate from qa, run on schedule) ----

fuzz:
	go test -fuzz=FuzzResolveOptions -fuzztime=30s .
	go test -fuzz=FuzzMessageJSON -fuzztime=30s .

# ---- Formatting ----

fmt:
	gofmt -w .

tidy:
	go mod tidy

# ---- Examples ----

examples-build:
	cd examples && go build ./...
