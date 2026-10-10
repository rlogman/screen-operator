# Gates locales de screen-operator. Los usan todos los loops (ver docs/requirements.json).
GO ?= go

.PHONY: build gate fmt-check vet test test-epic test-desktop test-live clean

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

# test-desktop: escenarios contra el escritorio real (build tag desktop). Es la
# UNICA forma soportada de correrlos: -p 1 serializa los paquetes, porque todos
# comparten el mismo escritorio y en paralelo se pisan. Carga el entorno desde
# ENV_FILE (por defecto .env) si existe, sin imprimir sus valores.
# Uso: make test-desktop [ENV_FILE=ruta] [DESKTOP_RUN=regex] [DESKTOP_ARGS=-count=10]
ENV_FILE ?= .env
DESKTOP_RUN ?= ^TestE2_(2_S5|6_S5|7_S[124])_
DESKTOP_ARGS ?= -count=1
test-desktop:
	@if [ -f "$(ENV_FILE)" ]; then set -a; . "$(ENV_FILE)"; set +a; echo "entorno cargado desde $(ENV_FILE)"; fi; \
	echo "$(GO) test -tags desktop -p 1 ./... -run '$(DESKTOP_RUN)' $(DESKTOP_ARGS)"; \
	$(GO) test -tags desktop -p 1 ./... -run '$(DESKTOP_RUN)' $(DESKTOP_ARGS)

# test-live: escenarios con llamadas reales al modelo (build tag live). Carga
# el entorno desde ENV_FILE sin imprimir sus valores. Cada corrida gasta.
# Uso: make test-live LIVE_RUN=regex [ENV_FILE=ruta]
LIVE_RUN ?= ^TestE8_1_S3_
test-live:
	@if [ -f "$(ENV_FILE)" ]; then set -a; . "$(ENV_FILE)"; set +a; echo "entorno cargado desde $(ENV_FILE)"; fi; \
	echo "$(GO) test -tags live ./... -run '$(LIVE_RUN)' -count=1 -v"; \
	$(GO) test -tags live ./... -run '$(LIVE_RUN)' -count=1 -v

clean:
	rm -rf bin
