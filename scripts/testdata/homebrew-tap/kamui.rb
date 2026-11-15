class Kamui < Formula
  desc "Use a remote SSH host's loopback services in a development browser"
  homepage "https://github.com/narasaka/kamui"
  url "https://github.com/narasaka/kamui/archive/refs/tags/v0.0.16.tar.gz"
  sha256 "964d31491d78af0afa729db8f4b9c0aa6b5c25e6d1e65af3c672040c1dc16d36"
  license "MIT"

  depends_on "go" => :build

  def install
    ldflags = "-buildid= " \
              "-X github.com/narasaka/kamui/internal/version.Version=#{version} " \
              "-X github.com/narasaka/kamui/internal/version.Commit=7569986f643b99ddb8031aad9ce54d08b3796208 " \
              "-X github.com/narasaka/kamui/internal/version.BuildDate=2026-11-08T21:25:45-05:00"
    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/kamui"
  end

  test do
    assert_match "kamui version #{version}", shell_output("#{bin}/kamui --version")
  end
end
