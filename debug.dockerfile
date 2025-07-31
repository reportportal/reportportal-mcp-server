# Dockerfile

FROM golang:1.24.4 AS builder

# allow this step access to build arg
ARG VERSION
# Set the working directory
WORKDIR /build

RUN go env -w GOMODCACHE=/root/.cache/go-build
RUN go install github.com/go-delve/delve/cmd/dlv@latest

# Install dependencies
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build go mod download

COPY . ./
# Build the server
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -gcflags "all=-N -l" -ldflags="-X main.version=${VERSION} -X main.commit=$(git rev-parse HEAD) -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o reportportal-mcp-server cmd/reportportal-mcp-server/main.go

# Final runtime image
FROM gcr.io/distroless/base-debian12

COPY --from=builder /build/reportportal-mcp-server /app/server
COPY --from=builder /go/bin/dlv /usr/local/bin/dlv

WORKDIR /app

# Expose Delve debug port and MCP server port
EXPOSE 52202 8080

# Start Delve without immediately starting the program
ENTRYPOINT ["dlv", "exec", "/app/server", "--headless", "--listen=:52202", "--api-version=2", "--log", "--accept-multiclient", "--"]