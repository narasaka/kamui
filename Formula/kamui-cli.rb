# Template for the CLI-only formula. scripts/update-homebrew-tap.rb fills in the
# release's source archive, checksum, commit, and build date and publishes it to
# https://github.com/narasaka/homebrew-tap as kamui-cli.rb.
class KamuiCli < Formula
  desc "Use a remote SSH host's loopback services in a development browser"
  homepage "https://github.com/narasaka/kamui"
  url "https://github.com/narasaka/kamui/archive/refs/tags/v0.0.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  license "MIT"

  depends_on "go" => :build

  def install
    ldflags = "-buildid= " \
              "-X github.com/narasaka/kamui/internal/version.Version=#{version} " \
              "-X github.com/narasaka/kamui/internal/version.Commit=0000000000000000000000000000000000000000 " \
              "-X github.com/narasaka/kamui/internal/version.BuildDate=1970-01-01T00:00:00Z"
    system "go", "build", *std_go_args(output: bin/"kamui", ldflags: ldflags), "./cmd/kamui"
  end

  def caveats
    <<~EOS
      On macOS, `brew install narasaka/tap/kamui` installs the menu bar app
      together with this CLI. Install one or the other, not both.
    EOS
  end

  test do
    assert_match "kamui version #{version}", shell_output("#{bin}/kamui --version")
  end
end
