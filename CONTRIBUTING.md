# Contributing

Run `gofmt`, `go vet ./...`, and `go test -race ./...` before submitting a
change. Tests describe observable behavior through package interfaces and mock
only operating-system or external-process seams.

Normal commit subjects use Scoped Commits:

```text
scope: concise description
```

Keep networking, SSH, browser, controller, and CLI concerns in their respective
packages. Do not log URL paths, query strings, HTTP headers, cookies, or bodies.
All proxy TCP listeners must bind to local loopback only.
