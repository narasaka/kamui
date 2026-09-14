package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserCompatibilityWorkflowAvoidsRemovedHomebrewOptions(t *testing.T) {
	scriptsRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	workflowPath := filepath.Join(filepath.Dir(scriptsRoot), ".github", "workflows", "browser-compatibility.yml")
	workflow, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(workflow), "--no-quarantine") {
		t.Fatal("browser compatibility workflow uses Homebrew's removed --no-quarantine option")
	}
}
