.PHONY: lint test check fmt tidy coverage vulncheck examples-build

lint:
	golangci-lint run ./...

test:
	go test -race -count=1 ./...

check: lint test

fmt:
	gofmt -w .

tidy:
	go mod tidy

coverage:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

vulncheck:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

examples-build:
	cd examples && go build ./...
