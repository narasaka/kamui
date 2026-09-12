class Kamui < Formula
  desc "Use a remote SSH host's loopback services in a development browser"
  homepage "https://github.com/narasaka/kamui"
  license "MIT"
  head "https://github.com/narasaka/kamui.git", branch: "main"

  depends_on "go" => :build

  def install
    ldflags = "-s -w -X github.com/narasaka/kamui/internal/version.Version=#{version}"
    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/kamui"
  end

  test do
    assert_match "kamui version", shell_output("#{bin}/kamui --version")
  end
end
