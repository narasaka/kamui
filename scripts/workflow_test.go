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

func TestReleaseWorkflowPublishesMenuBarAppOnlyWhenSigned(t *testing.T) {
	workflow := readRepositoryFile(t, ".github", "workflows", "release.yml")
	const guard = "if: steps.app_signing.outputs.enabled == 'true'"
	guarded := 0
	for _, step := range strings.Split(workflow, "\n      - ")[1:] {
		if !strings.Contains(step, "build-macos-app.sh") && !strings.Contains(step, "homebrew-tap/Casks/kamui-app.rb") {
			continue
		}
		if !strings.Contains(step, guard) {
			t.Fatalf("release step can publish an unsigned menu bar app without %q:\n%s", guard, step)
		}
		guarded++
	}
	if guarded != 2 {
		t.Fatalf("found %d guarded menu bar app release steps, want build and cask steps", guarded)
	}
	if !strings.Contains(workflow, "KAMUI_CODESIGN_IDENTITY: ${{ steps.keychain.outputs.identity }}") {
		t.Fatal("release workflow does not sign the menu bar app with the imported Developer ID identity")
	}
}

func TestCaskTemplateMatchesMenuBarAppBundle(t *testing.T) {
	cask := readRepositoryFile(t, "Casks", "kamui-app.rb")
	infoPlist := readRepositoryFile(t, "macos", "Resources", "Info.plist")
	buildScript := readRepositoryFile(t, "scripts", "build-macos-app.sh")

	if !strings.Contains(buildScript, `archive="${output_root}/Kamui_${version}_macos.zip"`) ||
		!strings.Contains(cask, `/v#{version}/Kamui_#{version}_macos.zip"`) {
		t.Fatal("cask URL does not name the archive produced by scripts/build-macos-app.sh")
	}
	const bundleID = "io.github.narasaka.kamui"
	if !strings.Contains(infoPlist, "<string>"+bundleID+"</string>") {
		t.Fatalf("Info.plist does not use bundle identifier %s", bundleID)
	}
	if !strings.Contains(infoPlist, "<key>CFBundleIconFile</key>\n\t<string>AppIcon</string>") ||
		!strings.Contains(buildScript, `"${app}/Contents/Resources/AppIcon.icns"`) {
		t.Fatal("app bundle does not ship and declare AppIcon.icns")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(mustGetwd(t)), "macos", "Resources", "AppIcon.icns")); err != nil {
		t.Fatalf("app icon is missing: %v", err)
	}
	for _, want := range []string{`app "Kamui.app"`, `quit: "` + bundleID + `"`, bundleID + `.plist"`, `depends_on formula: "narasaka/tap/kamui"`} {
		if !strings.Contains(cask, want) {
			t.Fatalf("cask template missing %q", want)
		}
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func readRepositoryFile(t *testing.T, elements ...string) string {
	t.Helper()
	scriptsRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(append([]string{filepath.Dir(scriptsRoot)}, elements...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
