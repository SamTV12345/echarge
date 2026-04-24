# syntax=docker/dockerfile:1.7

# ----- Build stage ------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

# BuildKit injects these when building multi-arch; declare them so the
# RUN step below can see them in the shell.
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

# git is required for `go install` below; ca-certs for HTTPS module fetches.
RUN apk add --no-cache git ca-certificates && \
    go install github.com/a-h/templ/cmd/templ@v0.3.960

# Prime the module cache separately so source changes don't invalidate it.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Generate templ Go files, then build a static binary.
# CGO_ENABLED=0 keeps the binary free of libc so it runs on scratch/alpine.
RUN templ generate ./internal/web/templates && \
    CGO_ENABLED=0 \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH:-amd64} \
    go build \
        -trimpath \
        -ldflags="-s -w" \
        -o /out/echarge \
        .

# ----- Runtime stage ----------------------------------------------------------
FROM alpine:3.20

# ca-certificates for OSRM HTTPS calls; tzdata for local time logging.
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && \
    adduser -S -G app -h /app app

WORKDIR /app

COPY --from=builder /out/echarge /app/echarge
# esbuild (compiled into the Go binary) bundles web/src/main.ts on startup,
# so the TS source tree must be present at runtime.
COPY --from=builder --chown=app:app /src/web /app/web

USER app
EXPOSE 8080
ENTRYPOINT ["/app/echarge"]
CMD ["-addr", ":8080"]
