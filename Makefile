.PHONY: build vet test tidy

# This SDK is generated and published by the upstream hduhelp-neo repo (its
# cmd/hduhelp-sdk-gen generator + script/generate_sdk.sh). These targets cover
# local development of the hand-written core/ package and the tests.

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

tidy:
	go mod tidy
