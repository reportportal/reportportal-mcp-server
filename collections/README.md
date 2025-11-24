# Postman Collections

This directory contains Postman Collection v2.1.0 format files used exclusively by the Go integration tests (`TestIntegrationFromPostmanCollection`).

## Purpose

These files are **completely separate** from test fixtures:
- **Test fixtures** in `testdata/` directory → verified against a live MCP server by `verify-testdata` tool
- **Postman collections** in this `collections/` directory → parsed by integration tests to generate test cases

## Format

Files here follow the [Postman Collection v2.1.0 schema](https://schema.postman.com/json/collection/v2.1.0/collection.json).

## Current Status

⚠️ **Note:** The Postman collection test case extraction is not yet fully implemented. Tests using these files are currently skipped. See the TODO comment in `internal/integration/integration_test.go` for details on what needs to be implemented.

## Adding New Collections

Place Postman Collection JSON exports here. They will be automatically discovered by the integration test suite but currently skipped until the extraction logic is implemented.
