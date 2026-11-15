# Template for the cask that installs the menu bar app and the kamui CLI inside
# it. scripts/update-homebrew-tap.rb fills in the version and archive checksum
# and publishes it to https://github.com/narasaka/homebrew-tap as Casks/kamui.rb.
cask "kamui" do
  version "0.0.0"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"

  url "https://github.com/narasaka/kamui/releases/download/v#{version}/Kamui_#{version}_macos.zip"
  name "Kamui"
  desc "Menu bar app and CLI for mirroring a remote SSH host's loopback services"
  homepage "https://github.com/narasaka/kamui"

  depends_on macos: :ventura

  # The app updates itself from its window (ADR 0017), and that also updates
  # the kamui linked below.
  auto_updates true

  app "Kamui.app"
  binary "#{appdir}/Kamui.app/Contents/Helpers/kamui"

  uninstall quit: "io.github.narasaka.kamui"

  zap trash: "~/Library/Preferences/io.github.narasaka.kamui.plist"
end
