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

// `brew install narasaka/tap/kamui` installs the menu bar app, so a release
// must not publish the tap without a signed app.
func TestReleaseWorkflowRequiresSignedMenuBarApp(t *testing.T) {
	workflow := readRepositoryFile(t, ".github", "workflows", "release.yml")
	var requirement string
	for _, step := range strings.Split(workflow, "\n      - ")[1:] {
		if strings.Contains(step, "if:") && !strings.HasPrefix(step, "name: Remove signing keychain") {
			t.Fatalf("release step is conditional, so a release can skip part of the tap:\n%s", step)
		}
		if strings.HasPrefix(step, "name: Require menu bar app signing credentials") {
			requirement = step
		}
	}
	if !strings.Contains(requirement, "exit 1") {
		t.Fatal("release workflow does not fail without the menu bar app signing secrets")
	}
	if strings.Index(workflow, "name: Require menu bar app signing credentials") > strings.Index(workflow, "run: make check") {
		t.Fatal("release workflow checks signing secrets only after the slow checks")
	}
	if !strings.Contains(workflow, "KAMUI_CODESIGN_IDENTITY: ${{ steps.keychain.outputs.identity }}") {
		t.Fatal("release workflow does not sign the menu bar app with the imported Developer ID identity")
	}
	if !strings.Contains(workflow, "scripts/update-homebrew-tap.rb homebrew-tap") {
		t.Fatal("release workflow does not write the tap with scripts/update-homebrew-tap.rb")
	}
}

func TestCaskTemplateMatchesMenuBarAppBundle(t *testing.T) {
	cask := readRepositoryFile(t, "Casks", "kamui.rb")
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
	if !strings.Contains(buildScript, `"${app}/Contents/Helpers/kamui"`) {
		t.Fatal("build-macos-app.sh does not bundle kamui where the cask links it from")
	}
	for _, want := range []string{
		`cask "kamui" do`,
		`app "Kamui.app"`,
		`binary "#{appdir}/Kamui.app/Contents/Helpers/kamui"`,
		`quit: "` + bundleID + `"`,
		bundleID + `.plist"`,
	} {
		if !strings.Contains(cask, want) {
			t.Fatalf("cask template missing %q", want)
		}
	}
	// The cask carries its own kamui; depending on the formula would install a
	// second, separately versioned copy.
	if strings.Contains(cask, "depends_on formula:") {
		t.Fatal("cask template depends on a formula")
	}
}

// The app's bundled kamui must have the same build identity as the release's
// formula and binaries, or the two would replace each other's controller.
func TestMenuBarAppBundlesCLIWithReleaseBuildIdentity(t *testing.T) {
	buildScript := readRepositoryFile(t, "scripts", "build-macos-app.sh")
	packageScript := readRepositoryFile(t, "scripts", "package.sh")
	workflow := readRepositoryFile(t, ".github", "workflows", "release.yml")

	for _, want := range []string{
		`build_date="${KAMUI_BUILD_DATE:-unknown}"`,
		"version.Version=${version} -X github.com/narasaka/kamui/internal/version.Commit=${commit} -X github.com/narasaka/kamui/internal/version.BuildDate=${build_date}",
	} {
		if !strings.Contains(buildScript, want) || !strings.Contains(packageScript, want) {
			t.Fatalf("build-macos-app.sh and package.sh do not both contain %q", want)
		}
	}
	if !strings.Contains(buildScript, `"${app}/Contents/Helpers/kamui"`) {
		t.Fatal("build-macos-app.sh does not bundle kamui in Contents/Helpers")
	}
	var appStep string
	for _, step := range strings.Split(workflow, "\n      - ")[1:] {
		if strings.Contains(step, "build-macos-app.sh") {
			appStep = step
		}
	}
	for _, want := range []string{
		"KAMUI_BUILD_COMMIT: ${{ steps.release.outputs.commit }}",
		"KAMUI_BUILD_DATE: ${{ steps.release.outputs.build_date }}",
	} {
		if !strings.Contains(appStep, want) {
			t.Fatalf("release workflow's app build step lacks %q", want)
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
