# Gates locales de screen-operator. Los usan todos los loops (ver docs/requirements.json).
GO ?= go

.PHONY: build gate fmt-check vet test test-epic clean

build:
	mkdir -p bin
	$(GO) build -o bin/ ./cmd/agent ./cmd/harness ./cmd/rfbprobe

# gate: gofmt -l sin archivos listados (fuera de .claude/, donde viven los worktrees), go vet y go test. Sale con 0 solo si los tres pasan.
gate: fmt-check vet test

fmt-check:
	@out="$$(find . -path ./.claude -prune -o -name '*.go' -type f -exec gofmt -l {} +)"; \
	if [ -n "$$out" ]; then echo "gofmt -l lista archivos sin formato:"; echo "$$out"; exit 1; fi; \
	echo "gofmt -l: ok"

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

# test-epic: suite de una epica. Uso: make test-epic E=3
test-epic:
	@if [ -z "$(E)" ]; then echo "uso: make test-epic E=<n>"; exit 2; fi
	$(GO) test ./... -run '^TestE$(E)_' -count=1

clean:
	rm -rf bin
