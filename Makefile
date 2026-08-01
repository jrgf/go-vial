COVERAGE_MIN ?= 80
COVERAGE_PROFILE ?= .vial/coverage.out
GOEXE := $(shell go env GOEXE)
VIAL_BIN ?= bin/vial$(GOEXE)

.PHONY: test coverage race vet examples build install dev check

test:
	go test ./...

coverage:
	mkdir -p $(dir $(COVERAGE_PROFILE))
	go test -coverprofile=$(COVERAGE_PROFILE) ./...
	@go tool cover -func=$(COVERAGE_PROFILE) | awk -v min="$(COVERAGE_MIN)" '/^total:/ { found=1; gsub(/%/, "", $$3); printf "coverage: %.1f%% (minimum %.1f%%)\n", $$3, min; if ($$3 + 0 < min) failed=1 } END { exit (!found || failed) }'

race:
	go test -race ./...

vet:
	go vet ./...

examples:
	cd examples/securecookie && go test ./...

build:
	mkdir -p $(dir $(VIAL_BIN))
	go build -o $(VIAL_BIN) ./cmd/vial

install:
	go install ./cmd/vial

dev: build
	$(VIAL_BIN) dev ./examples/hello

check: coverage race vet examples build
