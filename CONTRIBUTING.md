# Contributing

Run `make check` before submitting a change. It checks formatting, installs and
runs the repository-pinned golangci-lint release, runs `go vet`, and runs the
test suite with the race detector. Tests describe observable behavior through
package interfaces and mock only operating-system or external-process seams.

On pull requests, CI runs only the jobs whose paths changed;
`scripts/ci-jobs.sh` maps paths to jobs. A change to a workflow, to that
script, or to a path no filter covers runs every job. Pushes to `main` always
run every job. To add a job, give it a filter line in that script, gate it on
its group with `needs: changes` and `if:` like the other jobs, and list it in
`CI passed`. A job that builds the docs site, for example, needs
`site/* docs/* *.md`, since the site renders `docs/` and checks links into other
Markdown files.

Normal commit subjects use Scoped Commits:

```text
scope: concise description
```

Keep networking, SSH, browser, controller, and CLI concerns in their respective
packages. Do not log URL paths, query strings, HTTP headers, cookies, or bodies.
All proxy TCP listeners must bind to local loopback only.
