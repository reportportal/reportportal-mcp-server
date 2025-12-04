# Build stage
FROM golang:1.24.4 AS builder

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

# Build the binary
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" -o reportportal-mcp-server ./cmd/reportportal-mcp-server

# Final stage
FROM gcr.io/distroless/base-debian12

WORKDIR /server

# Copy the binary from builder
COPY --from=builder /build/reportportal-mcp-server .

# Command to run the server
CMD ["./reportportal-mcp-server"]
