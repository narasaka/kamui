package scripts_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	tapTestCommit     = "0123456789abcdef0123456789abcdef01234567"
	tapTestBuildDate  = "2026-11-14T09:30:00-05:00"
	tapTestSourceSHA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tapTestAppArchive = "Kamui test archive"
)

// testdata/homebrew-tap holds the tap as v0.0.16 published it: a kamui
// formula and a kamui-app cask that depends on it.
func TestUpdateHomebrewTapReplacesKamuiFormulaWithAppCask(t *testing.T) {
	tap := copyTapFixture(t)
	writeFile(t, filepath.Join(tap, "other.rb"), "class Other < Formula\nend\n")
	writeFile(t, filepath.Join(tap, "cask_renames.json"), `{"other-old": "other"}`+"\n")

	output, err := runUpdateHomebrewTap(t, tap, tapTestCommit)
	if err != nil {
		t.Fatalf("update-homebrew-tap.rb: %v: %s", err, output)
	}

	for _, removed := range []string{"kamui.rb", filepath.Join("Casks", "kamui-app.rb")} {
		if _, err := os.Stat(filepath.Join(tap, removed)); !os.IsNotExist(err) {
			t.Errorf("%s still exists (err %v); a kamui formula would shadow the kamui cask", removed, err)
		}
	}
	if got := readFile(t, filepath.Join(tap, "other.rb")); got != "class Other < Formula\nend\n" {
		t.Errorf("unrelated formula changed:\n%s", got)
	}

	formula := readFile(t, filepath.Join(tap, "kamui-cli.rb"))
	for _, want := range []string{
		"class KamuiCli < Formula",
		`url "https://github.com/narasaka/kamui/archive/refs/tags/v1.2.3.tar.gz"`,
		`sha256 "` + tapTestSourceSHA + `"`,
		"version.Commit=" + tapTestCommit + " ",
		"version.BuildDate=" + tapTestBuildDate + `"`,
		`std_go_args(output: bin/"kamui"`,
	} {
		if !strings.Contains(formula, want) {
			t.Errorf("kamui-cli.rb lacks %q:\n%s", want, formula)
		}
	}
	if strings.HasPrefix(formula, "#") {
		t.Errorf("kamui-cli.rb keeps the template comment:\n%s", formula)
	}

	archiveSum := sha256.Sum256([]byte(tapTestAppArchive))
	cask := readFile(t, filepath.Join(tap, "Casks", "kamui.rb"))
	for _, want := range []string{
		`cask "kamui" do`,
		`version "1.2.3"`,
		`sha256 "` + hex.EncodeToString(archiveSum[:]) + `"`,
		`binary "#{appdir}/Kamui.app/Contents/Helpers/kamui"`,
	} {
		if !strings.Contains(cask, want) {
			t.Errorf("Casks/kamui.rb lacks %q:\n%s", want, cask)
		}
	}
	if strings.HasPrefix(cask, "#") {
		t.Errorf("Casks/kamui.rb keeps the template comment:\n%s", cask)
	}

	var renames map[string]string
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(tap, "cask_renames.json"))), &renames); err != nil {
		t.Fatal(err)
	}
	if renames["kamui-app"] != "kamui" || renames["other-old"] != "other" || len(renames) != 2 {
		t.Errorf("cask_renames.json = %v, want kamui-app renamed to kamui and other entries kept", renames)
	}
	for _, name := range []string{"formula_renames.json", "tap_migrations.json"} {
		if _, err := os.Stat(filepath.Join(tap, name)); !os.IsNotExist(err) {
			t.Errorf("%s exists (err %v); it would send kamui to the formula or Linux users to the cask", name, err)
		}
	}

	// A second release finds the tap already migrated.
	if output, err := runUpdateHomebrewTap(t, tap, tapTestCommit); err != nil {
		t.Fatalf("update-homebrew-tap.rb on a migrated tap: %v: %s", err, output)
	}
	if got := readFile(t, filepath.Join(tap, "kamui-cli.rb")); got != formula {
		t.Errorf("second run changed kamui-cli.rb:\n%s", got)
	}
}

func TestUpdateHomebrewTapRejectsMissingBuildCommit(t *testing.T) {
	tap := copyTapFixture(t)
	if output, err := runUpdateHomebrewTap(t, tap, ""); err == nil {
		t.Fatalf("update-homebrew-tap.rb accepted an empty commit: %s", output)
	}
	if _, err := os.Stat(filepath.Join(tap, "kamui.rb")); err != nil {
		t.Fatalf("rejected update changed the tap: %v", err)
	}
}

func runUpdateHomebrewTap(t *testing.T, tap, commit string) ([]byte, error) {
	t.Helper()
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("ruby is not installed")
	}
	archive := filepath.Join(t.TempDir(), "Kamui_1.2.3_macos.zip")
	writeFile(t, archive, tapTestAppArchive)
	command := exec.Command("ruby", filepath.Join(mustGetwd(t), "update-homebrew-tap.rb"), tap, "1.2.3", tapTestSourceSHA, archive)
	command.Env = append(os.Environ(), "KAMUI_BUILD_COMMIT="+commit, "KAMUI_BUILD_DATE="+tapTestBuildDate)
	return command.CombinedOutput()
}

func copyTapFixture(t *testing.T) string {
	t.Helper()
	tap := t.TempDir()
	if err := os.CopyFS(tap, os.DirFS(filepath.Join(mustGetwd(t), "testdata", "homebrew-tap"))); err != nil {
		t.Fatal(err)
	}
	return tap
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
