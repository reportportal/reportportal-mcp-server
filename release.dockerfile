# Build stage
FROM golang:1.25.0 AS builder

WORKDIR /build

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build arguments for version info
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG VERSION_PKG="github.com/reportportal/reportportal-mcp-server/internal/config"

# Build the binary
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X ${VERSION_PKG}.Version=${VERSION} -X ${VERSION_PKG}.Commit=${COMMIT} -X ${VERSION_PKG}.Date=${BUILD_DATE}" -o reportportal-mcp-server ./cmd/reportportal-mcp-server

# Final stage
FROM gcr.io/distroless/base-debian12

WORKDIR /server

# Copy the binary from builder
COPY --from=builder /build/reportportal-mcp-server .

# Command to run the server
CMD ["./reportportal-mcp-server"]
