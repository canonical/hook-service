CGO_ENABLED?=0
GOOS?=linux
GO_BIN?=app
GO?=go
GOFLAGS?=-ldflags=-w -ldflags=-s -a -buildvcs
UI_FOLDER?=
MICROK8S_REGISTRY_FLAG?=SKAFFOLD_DEFAULT_REPO=localhost:32000
SKAFFOLD?=skaffold
CONFIGMAP?=deployments/kubectl/configMap.yaml


.EXPORT_ALL_VARIABLES:

mocks: vendor
	$(GO) install go.uber.org/mock/mockgen@v0.3.0
	# generate gomocks
	$(GO) generate ./...
.PHONY: mocks

test: mocks vet
	$(GO) test ./... -cover -coverprofile coverage_source.out
	# this will be cached, just needed to the test.json
	$(GO) test ./... -cover -coverprofile coverage_source.out -json > test_source.json
	cat coverage_source.out | grep -v "mock_*" | tee coverage.out
	cat test_source.json | grep -v "mock_*" | tee test.json
.PHONY: test

vet:
	$(GO) vet ./...
.PHONY: vet

vendor:
	$(GO) mod vendor
.PHONY: vendor

build:
	$(GO) build -o $(GO_BIN) ./
.PHONY: build

dev:
	./start.sh
