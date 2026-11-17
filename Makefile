VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
TARGETS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64

.PHONY: build test check
build:
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/plumbline-$$os-$$arch ./cmd/plumbline || exit 1; \
	done
	@ls -lh dist

test:
	go vet ./...
	go test ./...

check: test build
	claude plugin validate .
