.PHONY: build test test-race fmt vet check

build:
	go build ./cmd/issue-flow

test:
	go test ./...

test-race:
	go test -race ./...

fmt:
	gofmt -w $$(find . -name '*.go' -type f)

vet:
	go vet ./...

check:
	test -z "$$(gofmt -l .)"
	go test ./...
	go test -race ./...
	go vet ./...
