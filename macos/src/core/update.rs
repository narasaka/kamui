//! Finds a newer release of the app on GitHub and installs it in place
//! (ADR 0017). Everything runs through a [`CommandRunner`]: `curl` fetches,
//! `ditto` unpacks, and `codesign` checks that the download is Kamui, signed
//! with Kamui's Developer ID, before it replaces the running app.
//!
//! Every call blocks; the app makes them off the UI thread.

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use serde::Deserialize;
use url::Url;

use super::runner::{CommandIo, CommandResult, CommandRunner, ProcessRunner};

/// GitHub's latest published, non-prerelease release.
pub const LATEST_RELEASE: &str = "https://api.github.com/repos/narasaka/kamui/releases/latest";

/// The Apple Developer team that signs release builds of the app.
pub const TEAM_ID: &str = "SWGJKN7CUD";

pub const BUNDLE_ID: &str = "io.github.narasaka.kamui";

/// What a downloaded app must satisfy before it replaces this one: Kamui's
/// bundle identifier, signed with a Developer ID certificate that Apple
/// issued to Kamui's team. `--deep` covers the bundled kamui as well.
pub fn requirement() -> String {
    format!(
        "anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] and certificate leaf[field.1.2.840.113635.100.6.1.13] and certificate leaf[subject.OU] = \"{TEAM_ID}\" and identifier \"{BUNDLE_ID}\""
    )
}

/// A release newer than the running app.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Release {
    /// MAJOR.MINOR.PATCH, without the tag's `v`.
    pub version: String,
    /// The release's page on GitHub, with its notes.
    pub page: Url,
    /// The app's zip archive.
    pub archive: Url,
}

/// A release version. Only MAJOR.MINOR.PATCH counts; anything else, such as
/// a development build's `0.0.0` or a prerelease suffix, is not a release.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub struct Version(u64, u64, u64);

impl Version {
    pub fn parse(text: &str) -> Option<Self> {
        let text = text.strip_prefix('v').unwrap_or(text);
        let mut parts = text.split('.');
        let mut next = || -> Option<u64> {
            let part = parts.next()?;
            if part.is_empty() || !part.bytes().all(|b| b.is_ascii_digit()) {
                return None;
            }
            part.parse().ok()
        };
        let version = Self(next()?, next()?, next()?);
        parts.next().is_none().then_some(version)
    }
}

/// Whether `version` is a build that can update: a release, not a
/// development build.
pub fn updatable(version: &str) -> bool {
    Version::parse(version).is_some_and(|v| v != Version(0, 0, 0))
}

/// The name of the app's archive in a release.
pub fn archive_name(version: &str) -> String {
    format!("Kamui_{version}_macos.zip")
}

#[derive(Deserialize)]
struct LatestRelease {
    tag_name: String,
    html_url: String,
    #[serde(default)]
    draft: bool,
    #[serde(default)]
    prerelease: bool,
    #[serde(default)]
    assets: Vec<Asset>,
}

#[derive(Deserialize)]
struct Asset {
    name: String,
    browser_download_url: String,
}

/// Reads GitHub's latest release and returns it when it is newer than
/// `current` and carries the app. A release without the app, which the
/// release workflow publishes when signing is not set up, is no update.
pub fn decode_latest(json: &[u8], current: &str) -> Result<Option<Release>, String> {
    let latest: LatestRelease = serde_json::from_slice(json)
        .map_err(|e| format!("GitHub returned a release Kamui could not read: {e}"))?;
    if latest.draft || latest.prerelease {
        return Ok(None);
    }
    let Some(version) = Version::parse(&latest.tag_name) else {
        return Ok(None);
    };
    let current = Version::parse(current).unwrap_or(Version(0, 0, 0));
    if version <= current {
        return Ok(None);
    }
    let name = latest.tag_name.trim_start_matches('v').to_owned();
    let Some(asset) = latest.assets.iter().find(|a| a.name == archive_name(&name)) else {
        return Ok(None);
    };
    let page = https(&latest.html_url)?;
    let archive = https(&asset.browser_download_url)?;
    Ok(Some(Release {
        version: name,
        page,
        archive,
    }))
}

fn https(text: &str) -> Result<Url, String> {
    match Url::parse(text) {
        Ok(url) if url.scheme() == "https" => Ok(url),
        _ => Err(format!(
            "GitHub returned a release link that isn't HTTPS: {text}"
        )),
    }
}

/// Checks for and installs updates.
pub struct Updater {
    pub runner: Arc<dyn CommandRunner>,
    pub environment: HashMap<String, String>,
    pub feed: String,
}

const CURL: &str = "/usr/bin/curl";
const DITTO: &str = "/usr/bin/ditto";
const CODESIGN: &str = "/usr/bin/codesign";
const PLUTIL: &str = "/usr/bin/plutil";

impl Updater {
    pub fn new(environment: HashMap<String, String>) -> Self {
        Self {
            runner: Arc::new(ProcessRunner),
            environment,
            feed: LATEST_RELEASE.into(),
        }
    }

    pub fn with_runner(mut self, runner: Arc<dyn CommandRunner>) -> Self {
        self.runner = runner;
        self
    }

    fn run(
        &self,
        executable: &str,
        arguments: &[&str],
        timeout: Duration,
    ) -> Result<CommandResult, String> {
        let arguments: Vec<String> = arguments.iter().map(|a| (*a).to_owned()).collect();
        self.runner
            .run(
                Path::new(executable),
                &arguments,
                &self.environment,
                timeout,
                CommandIo::default(),
            )
            .map_err(|e| e.to_string())
    }

    /// Asks GitHub for the latest release. Returns it when it is newer than
    /// `current`.
    pub fn check(&self, current: &str) -> Result<Option<Release>, String> {
        let agent = format!("User-Agent: Kamui/{current}");
        let result = self.run(
            CURL,
            &[
                "--fail",
                "--silent",
                "--show-error",
                "--location",
                "--proto",
                "=https",
                "--max-time",
                "30",
                "--header",
                "Accept: application/vnd.github+json",
                "--header",
                &agent,
                "--",
                &self.feed,
            ],
            Duration::from_secs(40),
        )?;
        if result.exit_status != 0 {
            return Err(format!(
                "Kamui could not reach GitHub to check for updates: {}",
                first_line(&result.stderr)
            ));
        }
        decode_latest(&result.stdout, current)
    }

    /// Downloads `release`, checks it, and puts it where `app` is. The app
    /// that runs now keeps running from its open files until it relaunches.
    /// Any failure leaves `app` as it was.
    pub fn install(&self, release: &Release, app: &Path) -> Result<(), String> {
        let folder = app
            .parent()
            .ok_or_else(|| format!("Kamui can't update itself at {}.", app.display()))?;
        // Staged beside the app, so the swap is a rename on one volume.
        let staging = tempfile::Builder::new()
            .prefix(".kamui-update-")
            .tempdir_in(folder)
            .map_err(|e| {
                format!(
                    "Kamui can't write to {} to update itself ({e}). Download the update instead.",
                    folder.display()
                )
            })?;
        let archive = staging.path().join(archive_name(&release.version));
        let archive_text = archive.to_string_lossy().into_owned();
        let download = self.run(
            CURL,
            &[
                "--fail",
                "--silent",
                "--show-error",
                "--location",
                "--proto",
                "=https",
                "--max-time",
                "600",
                "--output",
                &archive_text,
                "--",
                release.archive.as_str(),
            ],
            Duration::from_secs(620),
        )?;
        if download.exit_status != 0 {
            return Err(format!(
                "Kamui could not download {}: {}",
                release.version,
                first_line(&download.stderr)
            ));
        }
        let unpacked = staging.path().join("unpacked");
        let unpacked_text = unpacked.to_string_lossy().into_owned();
        let unzip = self.run(
            DITTO,
            &["-x", "-k", &archive_text, &unpacked_text],
            Duration::from_secs(120),
        )?;
        if unzip.exit_status != 0 {
            return Err(format!(
                "Kamui could not unpack the update: {}",
                first_line(&unzip.stderr)
            ));
        }
        let new_app = unpacked.join("Kamui.app");
        self.verify(&new_app, &release.version)?;
        swap(app, &new_app, &staging.path().join("previous.app"))
    }

    /// Fails unless `app` is Kamui `version`, signed by Kamui's team.
    pub fn verify(&self, app: &Path, version: &str) -> Result<(), String> {
        let path = app.to_string_lossy().into_owned();
        let requirement = format!("-R={}", requirement());
        let signature = self.run(
            CODESIGN,
            &["--verify", "--deep", "--strict", &requirement, &path],
            Duration::from_secs(120),
        )?;
        if signature.exit_status != 0 {
            return Err(format!(
                "The downloaded update isn't signed by Kamui's developer, so Kamui didn't install it: {}",
                first_line(&signature.stderr)
            ));
        }
        let plist = app
            .join("Contents/Info.plist")
            .to_string_lossy()
            .into_owned();
        let read = self.run(
            PLUTIL,
            &[
                "-extract",
                "CFBundleShortVersionString",
                "raw",
                "-o",
                "-",
                &plist,
            ],
            Duration::from_secs(10),
        )?;
        let found = String::from_utf8_lossy(&read.stdout).trim().to_owned();
        if read.exit_status != 0 || found != version {
            return Err(format!(
                "The downloaded update is version {found:?}, not {version}, so Kamui didn't install it."
            ));
        }
        Ok(())
    }
}

/// Moves `new_app` to `app`, keeping the old one at `previous` (inside the
/// staging folder, which is deleted afterwards). Puts the old app back if
/// the new one can't take its place.
fn swap(app: &Path, new_app: &Path, previous: &Path) -> Result<(), String> {
    std::fs::rename(app, previous)
        .map_err(|e| format!("Kamui could not move itself aside to update ({e})."))?;
    if let Err(error) = std::fs::rename(new_app, app) {
        let _ = std::fs::rename(previous, app);
        return Err(format!("Kamui could not install the update ({error})."));
    }
    Ok(())
}

/// The app bundle that contains `executable`, which is
/// `Kamui.app/Contents/MacOS/Kamui` in a bundle.
pub fn bundle_of(executable: &Path) -> Option<PathBuf> {
    let bundle = executable.ancestors().nth(3)?;
    let macos = executable.parent()?;
    (bundle.extension().is_some_and(|e| e == "app")
        && macos.file_name().is_some_and(|n| n == "MacOS"))
    .then(|| bundle.to_path_buf())
}

/// A command that waits for process `pid` to exit, then opens `app`. The
/// app runs it detached just before it quits.
pub fn relaunch_command(pid: u32, app: &Path) -> (PathBuf, Vec<String>) {
    (
        PathBuf::from("/bin/sh"),
        vec![
            "-c".into(),
            "while /bin/kill -0 \"$1\" 2>/dev/null; do /bin/sleep 0.2; done; exec /usr/bin/open -n \"$2\""
                .into(),
            "kamui-relaunch".into(),
            pid.to_string(),
            app.to_string_lossy().into_owned(),
        ],
    )
}

fn first_line(text: &str) -> String {
    text.lines()
        .map(str::trim)
        .find(|l| !l.is_empty())
        .unwrap_or("no details")
        .to_owned()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::core::KamuiError;
    use std::sync::Mutex;

    fn release_json(tag: &str, assets: &[&str]) -> String {
        let assets: Vec<String> = assets
            .iter()
            .map(|name| {
                format!(
                    r#"{{"name":"{name}","browser_download_url":"https://github.com/narasaka/kamui/releases/download/{tag}/{name}"}}"#
                )
            })
            .collect();
        format!(
            r#"{{"tag_name":"{tag}","html_url":"https://github.com/narasaka/kamui/releases/tag/{tag}","draft":false,"prerelease":false,"assets":[{}]}}"#,
            assets.join(",")
        )
    }

    #[test]
    fn parses_release_versions_only() {
        assert_eq!(Version::parse("v1.2.3"), Some(Version(1, 2, 3)));
        assert_eq!(Version::parse("0.0.16"), Some(Version(0, 0, 16)));
        assert_eq!(Version::parse("1.2"), None);
        assert_eq!(Version::parse("1.2.3.4"), None);
        assert_eq!(Version::parse("1.2.3-rc1"), None);
        assert_eq!(Version::parse("1.+2.3"), None);
        assert!(Version(0, 0, 10) > Version(0, 0, 9));
        assert!(Version(0, 1, 0) > Version(0, 0, 99));
        assert!(updatable("0.0.16"));
        assert!(!updatable("0.0.0"));
        assert!(!updatable("dev"));
    }

    #[test]
    fn finds_a_newer_release_with_the_app() {
        let json = release_json("v0.0.17", &["checksums.txt", "Kamui_0.0.17_macos.zip"]);
        let release = decode_latest(json.as_bytes(), "0.0.16").unwrap().unwrap();
        assert_eq!(release.version, "0.0.17");
        assert_eq!(
            release.archive.as_str(),
            "https://github.com/narasaka/kamui/releases/download/v0.0.17/Kamui_0.0.17_macos.zip"
        );
        assert_eq!(
            release.page.as_str(),
            "https://github.com/narasaka/kamui/releases/tag/v0.0.17"
        );
    }

    #[test]
    fn ignores_releases_that_are_not_newer_or_lack_the_app() {
        let same = release_json("v0.0.16", &["Kamui_0.0.16_macos.zip"]);
        assert_eq!(decode_latest(same.as_bytes(), "0.0.16"), Ok(None));
        let older = release_json("v0.0.15", &["Kamui_0.0.15_macos.zip"]);
        assert_eq!(decode_latest(older.as_bytes(), "0.0.16"), Ok(None));
        let cli_only = release_json("v0.0.17", &["kamui_0.0.17_darwin_arm64"]);
        assert_eq!(decode_latest(cli_only.as_bytes(), "0.0.16"), Ok(None));
        let odd = release_json("nightly", &["Kamui_nightly_macos.zip"]);
        assert_eq!(decode_latest(odd.as_bytes(), "0.0.16"), Ok(None));
        let pre = release_json("v0.0.17", &["Kamui_0.0.17_macos.zip"])
            .replace(r#""prerelease":false"#, r#""prerelease":true"#);
        assert_eq!(decode_latest(pre.as_bytes(), "0.0.16"), Ok(None));
    }

    #[test]
    fn rejects_links_that_are_not_https() {
        let json = release_json("v0.0.17", &["Kamui_0.0.17_macos.zip"]).replace(
            "https://github.com/narasaka/kamui/releases/download",
            "http://example.com",
        );
        assert!(decode_latest(json.as_bytes(), "0.0.16").is_err());
        assert!(decode_latest(b"not json", "0.0.16").is_err());
    }

    #[test]
    fn finds_the_bundle_of_the_app_executable() {
        assert_eq!(
            bundle_of(Path::new("/Applications/Kamui.app/Contents/MacOS/Kamui")),
            Some(PathBuf::from("/Applications/Kamui.app"))
        );
        assert_eq!(
            bundle_of(Path::new("/Users/me/kamui/macos/target/debug/kamui-app")),
            None
        );
    }

    #[test]
    fn relaunch_passes_paths_as_arguments() {
        let (program, args) = relaunch_command(42, Path::new("/Applications/Kamui $(x).app"));
        assert_eq!(program, PathBuf::from("/bin/sh"));
        assert_eq!(args[3], "42");
        assert_eq!(args[4], "/Applications/Kamui $(x).app");
        assert!(!args[1].contains("Kamui"));
    }

    /// Plays commands' parts with real files: `curl` writes a zip, `ditto`
    /// makes the app, `codesign` and `plutil` answer as told.
    struct Fake {
        calls: Mutex<Vec<Vec<String>>>,
        signed: bool,
        version: &'static str,
        check: &'static str,
    }

    impl Fake {
        fn new(signed: bool, version: &'static str) -> Arc<Self> {
            Arc::new(Self {
                calls: Mutex::default(),
                signed,
                version,
                check: "",
            })
        }
        fn programs(&self) -> Vec<String> {
            self.calls
                .lock()
                .unwrap()
                .iter()
                .map(|c| c[0].clone())
                .collect()
        }
    }

    impl CommandRunner for Fake {
        fn run(
            &self,
            executable: &Path,
            arguments: &[String],
            _environment: &HashMap<String, String>,
            _timeout: Duration,
            _io: CommandIo,
        ) -> Result<CommandResult, KamuiError> {
            let mut call = vec![executable.display().to_string()];
            call.extend(arguments.iter().cloned());
            self.calls.lock().unwrap().push(call);
            let arg = |flag: &str| {
                let at = arguments.iter().position(|a| a == flag).unwrap();
                arguments[at + 1].clone()
            };
            Ok(match executable.to_str().unwrap() {
                CURL if arguments.contains(&"--output".to_owned()) => {
                    std::fs::write(arg("--output"), b"zip").unwrap();
                    CommandResult::new(0)
                }
                CURL => CommandResult::new(0).stdout(self.check),
                DITTO => {
                    let out = PathBuf::from(&arguments[3]);
                    std::fs::create_dir_all(out.join("Kamui.app/Contents")).unwrap();
                    std::fs::write(out.join("Kamui.app/Contents/marker"), "new").unwrap();
                    CommandResult::new(0)
                }
                CODESIGN if self.signed => CommandResult::new(0),
                CODESIGN => CommandResult::new(3).stderr(
                    "test-requirement: code failed to satisfy specified code requirement(s)",
                ),
                PLUTIL => CommandResult::new(0).stdout(&format!("{}\n", self.version)),
                other => panic!("unexpected {other}"),
            })
        }
    }

    fn installed(dir: &Path) -> PathBuf {
        let app = dir.join("Kamui.app");
        std::fs::create_dir_all(app.join("Contents")).unwrap();
        std::fs::write(app.join("Contents/marker"), "old").unwrap();
        app
    }

    fn release() -> Release {
        decode_latest(
            release_json("v0.0.17", &["Kamui_0.0.17_macos.zip"]).as_bytes(),
            "0.0.16",
        )
        .unwrap()
        .unwrap()
    }

    fn marker(app: &Path) -> String {
        std::fs::read_to_string(app.join("Contents/marker")).unwrap()
    }

    fn leftovers(dir: &Path) -> Vec<String> {
        std::fs::read_dir(dir)
            .unwrap()
            .map(|e| e.unwrap().file_name().to_string_lossy().into_owned())
            .filter(|n| n != "Kamui.app")
            .collect()
    }

    #[test]
    fn installs_a_signed_update_in_place() {
        let dir = tempfile::tempdir().unwrap();
        let app = installed(dir.path());
        let fake = Fake::new(true, "0.0.17");
        let updater = Updater::new(HashMap::new()).with_runner(fake.clone());
        updater.install(&release(), &app).unwrap();
        assert_eq!(marker(&app), "new");
        assert_eq!(leftovers(dir.path()), Vec::<String>::new());
        assert_eq!(fake.programs(), [CURL, DITTO, CODESIGN, PLUTIL]);
        let calls = fake.calls.lock().unwrap();
        assert!(calls[2].contains(&format!("-R={}", requirement())));
        assert!(calls[2].iter().any(|a| a.ends_with("unpacked/Kamui.app")));
    }

    #[test]
    fn keeps_the_app_when_the_update_is_not_signed_by_kamui() {
        let dir = tempfile::tempdir().unwrap();
        let app = installed(dir.path());
        let updater = Updater::new(HashMap::new()).with_runner(Fake::new(false, "0.0.17"));
        let error = updater.install(&release(), &app).unwrap_err();
        assert!(
            error.contains("isn't signed by Kamui's developer"),
            "{error}"
        );
        assert_eq!(marker(&app), "old");
        assert_eq!(leftovers(dir.path()), Vec::<String>::new());
    }

    #[test]
    fn keeps_the_app_when_the_update_is_another_version() {
        let dir = tempfile::tempdir().unwrap();
        let app = installed(dir.path());
        let updater = Updater::new(HashMap::new()).with_runner(Fake::new(true, "0.0.15"));
        let error = updater.install(&release(), &app).unwrap_err();
        assert!(error.contains("not 0.0.17"), "{error}");
        assert_eq!(marker(&app), "old");
    }

    #[test]
    fn reports_a_folder_it_cannot_write() {
        let updater = Updater::new(HashMap::new()).with_runner(Fake::new(true, "0.0.17"));
        let error = updater
            .install(&release(), Path::new("/nonexistent-kamui/Kamui.app"))
            .unwrap_err();
        assert!(error.contains("Download the update instead"), "{error}");
    }

    #[test]
    fn checks_github_over_https() {
        let json = release_json("v0.0.17", &["Kamui_0.0.17_macos.zip"]);
        let fake = Arc::new(Fake {
            calls: Mutex::default(),
            signed: true,
            version: "",
            check: Box::leak(json.into_boxed_str()),
        });
        let updater = Updater::new(HashMap::new()).with_runner(fake.clone());
        let release = updater.check("0.0.16").unwrap().unwrap();
        assert_eq!(release.version, "0.0.17");
        let calls = fake.calls.lock().unwrap();
        assert_eq!(calls[0].last().unwrap(), LATEST_RELEASE);
        assert!(calls[0].contains(&"=https".to_owned()));
        assert!(calls[0].contains(&"User-Agent: Kamui/0.0.16".to_owned()));
    }
}
