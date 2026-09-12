package browser_test

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/narasaka/kamui/internal/browser"
)

func TestChromiumPreparesIsolatedProfileAndLaunchesWithLoopbackProxyOverride(t *testing.T) {
	t.Parallel()

	launcher := &recordingLauncher{}
	adapter := browser.NewChromiumAdapter("chrome", []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}, launcher)
	session := browser.Session{
		Key:         "39e99aa3ca8d405a122b8c92c14bbe69e08c61fade009846f56b218410cbb84c",
		Proxy:       netip.MustParseAddrPort("127.0.0.1:52144"),
		ProfileRoot: t.TempDir(),
	}
	installation := browser.Installation{ID: "chrome", Executable: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}

	profile, err := adapter.PrepareProfile(context.Background(), session, installation)
	if err != nil {
		t.Fatalf("PrepareProfile returned error: %v", err)
	}
	info, err := os.Stat(profile.Path)
	if err != nil {
		t.Fatalf("profile path: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("profile permissions = %o, want user-only", info.Mode().Perm())
	}
	wantProfile := filepath.Join(session.ProfileRoot, session.Key, "chrome")
	if profile.Path != wantProfile {
		t.Fatalf("profile path = %q, want %q", profile.Path, wantProfile)
	}

	urls := []string{"http://localhost:3000", "http://localhost:3003"}
	if err := adapter.Launch(context.Background(), profile, urls); err != nil {
		t.Fatalf("Launch returned error: %v", err)
	}
	wantArgs := []string{
		"--user-data-dir=" + wantProfile,
		"--proxy-server=http://127.0.0.1:52144",
		"--proxy-bypass-list=<-loopback>",
		"--no-first-run",
		"http://localhost:3000",
		"http://localhost:3003",
	}
	if launcher.path != installation.Executable || !reflect.DeepEqual(launcher.args, wantArgs) {
		t.Fatalf("launch = %q %#v, want %q %#v", launcher.path, launcher.args, installation.Executable, wantArgs)
	}
}

type recordingLauncher struct {
	path string
	args []string
}

func (l *recordingLauncher) Launch(_ context.Context, path string, args []string) error {
	l.path = path
	l.args = append([]string(nil), args...)
	return nil
}
