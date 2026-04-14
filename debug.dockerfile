# Dockerfile

FROM golang:1.25.0 AS builder

# allow this step access to build arg
ARG VERSION
ARG VERSION_PKG="github.com/reportportal/reportportal-mcp-server/internal/config"
# Set the working directory
WORKDIR /build

RUN go env -w GOMODCACHE=/root/.cache/go-build
RUN go install github.com/go-delve/delve/cmd/dlv@latest

# Install dependencies
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build go mod download

COPY . ./
# Build the server
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -gcflags "all=-N -l" -ldflags="-X ${VERSION_PKG}.Version=${VERSION} -X ${VERSION_PKG}.Commit=$(git rev-parse HEAD) -X ${VERSION_PKG}.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o reportportal-mcp-server cmd/reportportal-mcp-server/main.go

# Final runtime image
FROM gcr.io/distroless/base-debian12

COPY --from=builder /build/reportportal-mcp-server /app/server
COPY --from=builder /go/bin/dlv /usr/local/bin/dlv

WORKDIR /app

# Expose debug port and default HTTP port
EXPOSE 52202 8080

ENTRYPOINT ["dlv", "exec", "/app/server", "--headless", "--listen=:52202", "--api-version=2", "--log", "--accept-multiclient"]