GOLANGCI_LINT_VERSION := $(shell tr -d '\n' < .golangci-lint-version)
GOLANGCI_LINT := $(CURDIR)/bin/golangci-lint

.PHONY: build check check-format check-lint install-golangci-lint package

build:
	go build -trimpath -o bin/kamui ./cmd/kamui

check: check-format check-lint
	go vet ./...
	go test -race ./...

check-format:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "The following files need to be formatted with gofmt:" >&2; \
		echo "$$unformatted" >&2; \
		exit 1; \
	fi

check-lint: install-golangci-lint
	GOOS=darwin $(GOLANGCI_LINT) run ./...
	GOOS=linux $(GOLANGCI_LINT) run ./...

install-golangci-lint:
	@expected="$(patsubst v%,%,$(GOLANGCI_LINT_VERSION))"; \
	actual="$$("$(GOLANGCI_LINT)" version --short 2>/dev/null || true)"; \
	if [ "$$actual" = "$$expected" ]; then \
		exit 0; \
	fi; \
	curl --fail --silent --show-error --location https://golangci-lint.run/install.sh | \
		sh -s -- -b "$(CURDIR)/bin" "$(GOLANGCI_LINT_VERSION)"; \
	actual="$$("$(GOLANGCI_LINT)" version --short 2>/dev/null || true)"; \
	if [ "$$actual" != "$$expected" ]; then \
		echo "installed golangci-lint $$actual; want $$expected" >&2; \
		exit 1; \
	fi

package:
	@test -n "$(VERSION)" || (echo "VERSION is required" >&2; exit 2)
	scripts/package.sh "$(VERSION)"
