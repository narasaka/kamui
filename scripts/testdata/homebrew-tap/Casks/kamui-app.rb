cask "kamui-app" do
  version "0.0.16"
  sha256 "bd752e71603ce1f928cbb1f50d126c4637adddc37359527819b2cf0af5ae2154"

  url "https://github.com/narasaka/kamui/releases/download/v#{version}/Kamui_#{version}_macos.zip"
  name "Kamui"
  desc "Menu bar app for mirroring a remote SSH host's loopback services"
  homepage "https://github.com/narasaka/kamui"

  depends_on formula: "narasaka/tap/kamui"
  depends_on macos: :ventura

  app "Kamui.app"

  uninstall quit: "io.github.narasaka.kamui"

  zap trash: "~/Library/Preferences/io.github.narasaka.kamui.plist"
end
