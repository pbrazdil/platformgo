SHELL := /usr/bin/env bash
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
GOVULNCHECK := go run golang.org/x/vuln/cmd/govulncheck@v1.6.0

.PHONY: policy port-map-complete fmt fmt-check lint test test-race test-repeat vuln verify

policy:
	python3 ./scripts/check-agent-runtime.py
	./scripts/policy-check.sh
	python3 ./scripts/check-port-map.py
	python3 ./scripts/test-check-port-map.py
	python3 ./scripts/check-dependencies.py

port-map-complete:
	python3 ./scripts/check-port-map.py --complete

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

fmt-check:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))" || \
	  (echo 'gofmt required:'; gofmt -l $$(find . -name '*.go' -not -path './vendor/*'); exit 1)

lint:
	go vet ./...
	@packages=""; \
	while IFS=, read -r path classification _; do \
	  if [ -d "$$path" ] && [ "$$classification" != "ported-compatibility" ] && [ "$$classification" != "test-placeholder" ]; then \
	    packages="$$packages ./$$path/..."; \
	  fi; \
	done < <(tail -n +2 policy/go-package-policy.csv); \
	  $(GOLANGCI_LINT) run $$packages

test:
	go test ./...

test-race:
	go test -race ./...

test-repeat:
	@if [ -d internal ] || [ -d testkit ]; then \
	  pkgs="$$(go list ./internal/... ./testkit/... 2>/dev/null || true)"; \
	  if [ -n "$$pkgs" ]; then go test $$pkgs -count=20; fi; \
	fi

vuln:
	$(GOVULNCHECK) ./...

verify: policy fmt-check lint test test-race test-repeat vuln
