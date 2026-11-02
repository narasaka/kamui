package scripts_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var allCIGroups = []string{"go", "macos-app", "browser-smoke"}

func TestCIJobsSelectsGroupsForChangedPaths(t *testing.T) {
	for _, test := range []struct {
		name  string
		paths []string
		want  []string
	}{
		{name: "nothing changed", want: []string{}},
		{name: "docs only", paths: []string{"docs/cli.md", "README.md", "assets/logo.svg"}, want: []string{}},
		{name: "Go change in internal", paths: []string{"internal/proxy/proxy.go"}, want: []string{"go", "browser-smoke"}},
		{name: "CLI contract used by the menu bar app", paths: []string{"internal/cliapp/status_json.go"}, want: allCIGroups},
		{name: "status fixture read by the menu bar app tests", paths: []string{"internal/cliapp/testdata/status.json"}, want: allCIGroups},
		{name: "menu bar app source", paths: []string{"macos/Sources/KamuiMenuBar/AppModel.swift"}, want: []string{"macos-app"}},
		{name: "menu bar app bundle resources checked by Go tests", paths: []string{"macos/Resources/Info.plist"}, want: []string{"go", "macos-app"}},
		{name: "lint configuration", paths: []string{".golangci.yml"}, want: []string{"go"}},
		{name: "workflow", paths: []string{"docs/cli.md", ".github/workflows/ci.yml"}, want: allCIGroups},
		{name: "job selection script", paths: []string{"scripts/ci-jobs.sh"}, want: allCIGroups},
		{name: "path without a filter", paths: []string{"docs/cli.md", "tools/generate.sh"}, want: allCIGroups},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := runCIJobs(t, strings.Join(test.paths, "\n")); !slices.Equal(got, test.want) {
				t.Fatalf("ci-jobs.sh selected %q for %q, want %q", got, test.paths, test.want)
			}
		})
	}

	if got := runCIJobs(t, "", "--all"); !slices.Equal(got, allCIGroups) {
		t.Fatalf("ci-jobs.sh --all selected %q, want %q", got, allCIGroups)
	}
}

func TestCIWorkflowGatesEveryJobOnItsGroup(t *testing.T) {
	workflow := readRepositoryFile(t, ".github", "workflows", "ci.yml")
	_, jobsSection, found := strings.Cut(workflow, "\njobs:\n")
	if !found {
		t.Fatal("ci.yml has no jobs section")
	}
	header := regexp.MustCompile(`(?m)^  ([a-z0-9-]+):\n`)
	bounds := header.FindAllStringSubmatchIndex(jobsSection, -1)
	gate := regexp.MustCompile(`(?m)^    if: contains\(fromJSON\(needs\.changes\.outputs\.jobs\), '([a-z0-9-]+)'\)$`)

	var gated []string
	referenced := map[string]bool{}
	for index, bound := range bounds {
		job := jobsSection[bound[2]:bound[3]]
		end := len(jobsSection)
		if index+1 < len(bounds) {
			end = bounds[index+1][0]
		}
		body := jobsSection[bound[0]:end]
		switch job {
		case "changes":
			continue
		case "ci-passed":
			continue
		}
		match := gate.FindStringSubmatch(body)
		if match == nil || !strings.Contains(body, "\n    needs: changes\n") {
			t.Fatalf("CI job %s does not run only for its path group:\n%s", job, body)
		}
		if !slices.Contains(allCIGroups, match[1]) {
			t.Fatalf("CI job %s uses group %q, which scripts/ci-jobs.sh does not define", job, match[1])
		}
		referenced[match[1]] = true
		gated = append(gated, job)
	}
	for _, group := range allCIGroups {
		if !referenced[group] {
			t.Fatalf("no CI job runs for group %q", group)
		}
	}

	wantNeeds := "    needs: [changes, " + strings.Join(gated, ", ") + "]\n"
	if !strings.Contains(jobsSection, "  ci-passed:\n") || !strings.Contains(jobsSection, wantNeeds) {
		t.Fatalf("ci-passed job must use %q so one check covers every job", strings.TrimSpace(wantNeeds))
	}
}

func runCIJobs(t *testing.T, paths string, arguments ...string) []string {
	t.Helper()
	command := exec.Command(filepath.Join(mustGetwd(t), "ci-jobs.sh"), arguments...)
	command.Stdin = strings.NewReader(paths)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("ci-jobs.sh failed: %v\n%s", err, output)
	}
	value, found := strings.CutPrefix(strings.TrimSpace(string(output)), "jobs=")
	if !found {
		t.Fatalf("ci-jobs.sh printed %q, want a jobs= output", output)
	}
	var groups []string
	if err := json.Unmarshal([]byte(value), &groups); err != nil {
		t.Fatalf("ci-jobs.sh printed invalid JSON %q: %v", value, err)
	}
	return groups
}
