# Imagen del agente screen-operator (historia 4.1).
#
# Etapa 1 compila un binario estatico (CGO_ENABLED=0). Etapa 2 es distroless
# static: sin shell, sin toolchain de Go, con certificados CA (wss:// y
# OpenRouter usan TLS) y usuario nonroot. La configuracion se copia a
# /app/config y el agente la lee por SO_CONFIG_DIR (decision 1.2).
#
# Ningun secreto entra a la imagen: TEAM_TOKEN y OPENROUTER_API_KEY llegan como
# variables de entorno del servicio (ver docs/deploy.md y .dockerignore).

FROM golang:1.27.1-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd/ cmd/
COPY internal/ internal/
RUN go build -trimpath -ldflags="-s -w -buildid=" -o /out/agent ./cmd/agent

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/agent /app/agent
COPY config/ /app/config/
ENV SO_CONFIG_DIR=/app/config \
    PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/agent"]
