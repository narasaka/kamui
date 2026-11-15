#!/usr/bin/env ruby
# frozen_string_literal: true

# Writes a release's formula and cask into a checkout of
# https://github.com/narasaka/homebrew-tap from this repository's templates.
#
# The tap publishes `kamui` as a cask (menu bar app and CLI) and `kamui-cli` as
# a formula (CLI only). Homebrew prefers a formula over a tap's cask of the same
# name, so the old `kamui` formula is removed rather than renamed: a rename
# would make `brew install narasaka/tap/kamui` resolve to `kamui-cli`.

require "digest"
require "fileutils"
require "json"

usage = "usage: scripts/update-homebrew-tap.rb TAP_DIRECTORY VERSION SOURCE_SHA256 APP_ARCHIVE"
abort usage unless ARGV.length == 4

tap, version, source_sha, archive = ARGV
commit = ENV.fetch("KAMUI_BUILD_COMMIT", "")
build_date = ENV.fetch("KAMUI_BUILD_DATE", "")

abort "VERSION must be MAJOR.MINOR.PATCH without a leading v" unless version.match?(/\A\d+\.\d+\.\d+\z/)
abort "SOURCE_SHA256 must be 64 lowercase hex digits" unless source_sha.match?(/\A\h{64}\z/)
abort "KAMUI_BUILD_COMMIT must be a full commit hash" unless commit.match?(/\A\h{40}\z/)
abort "KAMUI_BUILD_DATE is required" if build_date.empty? || build_date.match?(/[\s"\\]/)
abort "#{tap} is not a directory" unless File.directory?(tap)
abort "#{archive} does not exist" unless File.file?(archive)

repository = File.expand_path("..", __dir__)

def render(template_path, replacements)
  contents = File.read(template_path).sub(/\A(#.*\n)+/, "")
  replacements.each do |pattern, replacement|
    matches = contents.scan(pattern).length
    abort "expected one #{pattern.inspect} in #{template_path}, found #{matches}" unless matches == 1
    contents = contents.sub(pattern) { replacement }
  end
  contents
end

formula = render(
  File.join(repository, "Formula", "kamui-cli.rb"),
  {
    /^  url ".*"$/ => %(  url "https://github.com/narasaka/kamui/archive/refs/tags/v#{version}.tar.gz"),
    /^  sha256 ".*"$/ => %(  sha256 "#{source_sha}"),
    /version\.Commit=[^ "]+/ => "version.Commit=#{commit}",
    /version\.BuildDate=[^ "]+/ => "version.BuildDate=#{build_date}",
  },
)
cask = render(
  File.join(repository, "Casks", "kamui.rb"),
  {
    /^  version ".*"$/ => %(  version "#{version}"),
    /^  sha256 ".*"$/ => %(  sha256 "#{Digest::SHA256.file(archive).hexdigest}"),
  },
)

File.write(File.join(tap, "kamui-cli.rb"), formula)
FileUtils.mkdir_p(File.join(tap, "Casks"))
File.write(File.join(tap, "Casks", "kamui.rb"), cask)
[File.join(tap, "kamui.rb"), File.join(tap, "Casks", "kamui-app.rb")].each do |old|
  File.delete(old) if File.exist?(old)
end

# Moves existing kamui-app installations to the kamui cask on `brew update`.
renames_path = File.join(tap, "cask_renames.json")
renames = File.exist?(renames_path) ? JSON.parse(File.read(renames_path)) : {}
renames["kamui-app"] = "kamui"
File.write(renames_path, "#{JSON.pretty_generate(renames.sort.to_h)}\n")
