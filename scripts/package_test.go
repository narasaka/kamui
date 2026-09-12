package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageBuildsEverySupportedUnixTarget(t *testing.T) {
	scriptsRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot := filepath.Dir(scriptsRoot)
	outputRoot := t.TempDir()
	command := exec.Command(filepath.Join(scriptsRoot, "package.sh"), "test")
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(),
		"KAMUI_OUTPUT_ROOT="+outputRoot,
		"KAMUI_BUILD_COMMIT=test-commit",
		"KAMUI_BUILD_DATE=2026-09-12T00:00:00Z",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("package release: %v: %s", err, output)
	}

	targets := []string{
		"kamui_test_darwin_arm64",
		"kamui_test_darwin_amd64",
		"kamui_test_linux_arm64",
		"kamui_test_linux_amd64",
	}
	checksums, err := os.ReadFile(filepath.Join(outputRoot, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		info, err := os.Stat(filepath.Join(outputRoot, target))
		if err != nil {
			t.Errorf("release target %s: %v", target, err)
			continue
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("release target %s is not executable", target)
		}
		if !strings.Contains(string(checksums), target) {
			t.Errorf("checksums omit %s", target)
		}
	}
}

func TestGoInstallBuildsRunnableCommand(t *testing.T) {
	scriptsRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot := filepath.Dir(scriptsRoot)
	binaryRoot := t.TempDir()
	command := exec.Command("go", "install", "./cmd/kamui")
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(), "GOBIN="+binaryRoot)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go install: %v: %s", err, output)
	}
	output, err := exec.Command(filepath.Join(binaryRoot, "kamui"), "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("run go-installed kamui: %v: %s", err, output)
	}
	if strings.TrimSpace(string(output)) == "" {
		t.Fatal("go-installed kamui printed no version")
	}
}
