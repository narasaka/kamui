.PHONY: build check package

build:
	go build -trimpath -o bin/kamui ./cmd/kamui

check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test -race ./...

package:
	@test -n "$(VERSION)" || (echo "VERSION is required" >&2; exit 2)
	scripts/package.sh "$(VERSION)"
