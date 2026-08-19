VERSION ?= 0.2.0-dev
BUILD_COMMIT ?= unknown
DIST_DIR ?= dist
LDFLAGS := -X main.version=$(VERSION) -X main.buildCommit=$(BUILD_COMMIT)

.PHONY: build snapshot verify-dist test test-race fmt vet check

build:
	go build -ldflags "$(LDFLAGS)" ./cmd/issue-flow

snapshot:
	mkdir -p "$(DIST_DIR)"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/issue-flow_linux_amd64" ./cmd/issue-flow
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/issue-flow_darwin_arm64" ./cmd/issue-flow
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/issue-flow_windows_amd64.exe" ./cmd/issue-flow
	cd "$(DIST_DIR)" && sha256sum issue-flow_linux_amd64 issue-flow_darwin_arm64 issue-flow_windows_amd64.exe > checksums.txt

verify-dist:
	./scripts/verify-dist.sh

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
