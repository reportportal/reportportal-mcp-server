# Make a stage to run the app
FROM gcr.io/distroless/base-debian12
# Set the working directory
WORKDIR /server
# Copy the binary
# see https://goreleaser.com/errors/docker-build/#do
COPY reportportal-mcp-server .
# Command to run the server
CMD ["./reportportal-mcp-server"]