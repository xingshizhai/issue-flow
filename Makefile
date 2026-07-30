.PHONY: build test fmt vet check

build:
	go build ./cmd/issue-flow

test:
	go test ./...

fmt:
	gofmt -w $$(find . -name '*.go' -type f)

vet:
	go vet ./...

check:
	test -z "$$(gofmt -l .)"
	go test ./...
	go vet ./...
