.PHONY: generate build vet test tidy

OAPI_CODEGEN_VERSION := v2.7.2
MODULE := github.com/hduhelp/hduhelp-neo-sdk-go
NORMALIZED := $(CURDIR)/.openapi.normalized.yaml

# Regenerate the whole SDK from the vendored openapi.yaml, in three steps:
#   1. Normalize the spec (drop the spurious inherited path parameters that
#      thrift-gen-http-swagger emits, which the generators otherwise reject).
#   2. oapi-codegen emits the plain model structs into package models.
#   3. The custom generator (internal/gen) emits the Feishu-style layer:
#      namespaced services, fluent request builders, and typed response wrappers,
#      plus the top-level client.gen.go.
#
# Requires python3 + pyyaml and oapi-codegen $(OAPI_CODEGEN_VERSION) on PATH:
#   go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)
generate:
	python3 scripts/normalize_openapi.py openapi.yaml $(NORMALIZED)
	oapi-codegen -config models/config.yaml $(NORMALIZED)
	gofmt -w models/models.gen.go
	cd internal/gen && go run . $(NORMALIZED) $(MODULE) $(CURDIR)
	rm -f $(NORMALIZED)
	gofmt -w models service client.gen.go

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

tidy:
	go mod tidy
	cd internal/gen && go mod tidy
