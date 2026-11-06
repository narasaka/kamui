//! Finds the kamui CLI and the environment it should run with.
//!
//! The app prefers the kamui that the user's terminal runs, so both use one
//! build and one controller. It falls back to the copy inside the app, which
//! lets the app work without a separately installed CLI (ADR 0015).
//!
//! Apps launched from Finder or at login receive a minimal PATH. The CLI and
//! the controller it may start should see the same PATH and SSH agent as the
//! user's login shell, so the app adopts those values from a login shell.

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::time::Duration;

use super::runner::{CommandIo, CommandRunner};

pub const FALLBACK_DIRECTORIES: [&str; 4] = [
    "/opt/homebrew/bin",
    "/usr/local/bin",
    "~/go/bin",
    "~/.local/bin",
];
const IMPORTED_VARIABLES: [&str; 2] = ["PATH", "SSH_AUTH_SOCK"];

/// Where the build script places kamui inside the app bundle. The app
/// executable's own name differs only in case, so the CLI cannot share
/// `Contents/MacOS` on a case-insensitive volume.
pub fn bundled_executable(bundle: &Path) -> PathBuf {
    bundle.join("Contents/Helpers/kamui")
}

pub fn is_executable(path: &Path) -> bool {
    use std::os::unix::fs::PermissionsExt;
    std::fs::metadata(path).is_ok_and(|m| m.is_file() && m.permissions().mode() & 0o111 != 0)
}

/// Returns `base` with PATH and SSH_AUTH_SOCK taken from the login shell when
/// it reports them.
///
/// The login shell execs a POSIX shell to print the exported values, so fish
/// and other shells report a colon-separated PATH. A random marker separates
/// the values from anything the shell's startup files print.
pub fn login_environment(
    runner: &dyn CommandRunner,
    base: &HashMap<String, String>,
) -> HashMap<String, String> {
    let shell = base
        .get("SHELL")
        .filter(|s| !s.is_empty())
        .map_or("/bin/zsh", String::as_str);
    let marker = format!("kamui-environment-{}", random_token());
    let fields = std::iter::once("$0".to_owned())
        .chain(IMPORTED_VARIABLES.iter().map(|v| format!("${v}")))
        .map(|f| format!("\"{f}\""))
        .collect::<Vec<_>>()
        .join(" ");
    let script = format!("exec /bin/sh -c 'printf \"%s\\0\" {fields}' {marker}");
    let result = runner.run(
        Path::new(shell),
        &["-l".into(), "-c".into(), script],
        base,
        Duration::from_secs(10),
        CommandIo::default(),
    );
    let mut environment = base.clone();
    if let Ok(result) = result
        && result.exit_status == 0
        && let Some(imported) = parse(&result.stdout, &marker)
    {
        environment.extend(imported);
    }
    environment
}

fn random_token() -> String {
    use std::hash::{BuildHasher, RandomState};
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |d| d.as_nanos());
    format!(
        "{:016x}{:x}",
        RandomState::new().hash_one(nanos),
        std::process::id()
    )
}

/// Extracts the variables printed after `marker`. Returns None when the
/// payload is missing, truncated, or reports an unusable PATH.
pub fn parse(output: &[u8], marker: &str) -> Option<HashMap<String, String>> {
    let start = [marker.as_bytes(), b"\0"].concat();
    let position = output
        .windows(start.len())
        .rposition(|w| w == start.as_slice())?;
    let values: Vec<&[u8]> = output[position + start.len()..]
        .split(|&b| b == 0)
        .collect();
    if values.len() != IMPORTED_VARIABLES.len() + 1 || !values.last()?.is_empty() {
        return None;
    }
    let imported: HashMap<String, String> = IMPORTED_VARIABLES
        .iter()
        .zip(&values)
        .filter(|(_, value)| !value.is_empty())
        .map(|(name, value)| {
            (
                (*name).to_owned(),
                String::from_utf8_lossy(value).into_owned(),
            )
        })
        .collect();
    let path = imported.get("PATH")?;
    if path.contains(['\n', '\r']) || !path.split(':').any(|d| d.starts_with('/')) {
        return None;
    }
    Some(imported)
}

/// Returns an explicit executable when one is configured, otherwise the first
/// kamui on PATH or in a common installation directory, otherwise the bundled
/// copy. An explicit choice never falls back.
pub fn locate(
    override_path: Option<&str>,
    environment: &HashMap<String, String>,
    bundled: Option<&Path>,
    is_executable: impl Fn(&Path) -> bool,
) -> Option<PathBuf> {
    if let Some(path) = override_path.filter(|p| !p.is_empty()) {
        let path = PathBuf::from(expand_tilde(path, environment));
        return is_executable(&path).then_some(path);
    }
    let search = environment.get("PATH").map_or("", String::as_str);
    for directory in search
        .split(':')
        .filter(|d| !d.is_empty())
        .chain(FALLBACK_DIRECTORIES)
    {
        let path = PathBuf::from(format!("{}/kamui", expand_tilde(directory, environment)));
        if is_executable(&path) {
            return Some(path);
        }
    }
    bundled.filter(|b| is_executable(b)).map(Path::to_path_buf)
}

fn expand_tilde(path: &str, environment: &HashMap<String, String>) -> String {
    if path != "~" && !path.starts_with("~/") {
        return path.to_owned();
    }
    let home = environment
        .get("HOME")
        .cloned()
        .or_else(|| std::env::var("HOME").ok())
        .unwrap_or_default();
    format!("{home}{}", &path[1..])
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::core::client::tests::FakeRunner;
    use crate::core::runner::{CommandResult, ProcessRunner};

    fn env(pairs: &[(&str, &str)]) -> HashMap<String, String> {
        pairs
            .iter()
            .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
            .collect()
    }

    #[test]
    fn prefers_override_then_path_then_fallbacks() {
        let available = [
            "/custom/kamui",
            "/Users/n/go/bin/kamui",
            "/opt/homebrew/bin/kamui",
        ];
        let exists = |p: &Path| available.iter().any(|a| Path::new(a) == p);
        let environment = env(&[("PATH", "/usr/bin:/Users/n/go/bin"), ("HOME", "/Users/n")]);

        assert_eq!(
            locate(Some("/custom/kamui"), &environment, None, exists),
            Some("/custom/kamui".into())
        );
        assert_eq!(
            locate(Some("/missing/kamui"), &environment, None, exists),
            None
        );
        assert_eq!(
            locate(None, &environment, None, exists),
            Some("/Users/n/go/bin/kamui".into())
        );
        assert_eq!(
            locate(None, &env(&[("HOME", "/Users/n")]), None, exists),
            Some("/opt/homebrew/bin/kamui".into())
        );
    }

    #[test]
    fn falls_back_to_bundled_copy_only_without_an_installed_cli() {
        let bundled = bundled_executable(Path::new("/Applications/Kamui.app"));
        assert_eq!(
            bundled,
            Path::new("/Applications/Kamui.app/Contents/Helpers/kamui")
        );
        let environment = env(&[("PATH", "/usr/bin"), ("HOME", "/Users/n")]);

        let with_cli = |p: &Path| p == bundled || p == Path::new("/opt/homebrew/bin/kamui");
        assert_eq!(
            locate(None, &environment, Some(&bundled), with_cli),
            Some("/opt/homebrew/bin/kamui".into()),
            "the terminal's kamui wins, so both use one build"
        );
        assert_eq!(
            locate(bundled.to_str(), &environment, Some(&bundled), with_cli),
            Some(bundled.clone())
        );

        let alone = |p: &Path| p == bundled;
        assert_eq!(
            locate(None, &environment, Some(&bundled), alone),
            Some(bundled.clone())
        );
        assert_eq!(
            locate(Some("/missing/kamui"), &environment, Some(&bundled), alone),
            None,
            "an explicit choice never falls back"
        );
        assert_eq!(locate(None, &environment, Some(&bundled), |_| false), None);
    }

    #[test]
    fn imports_path_and_agent_from_real_login_shell_despite_startup_output() {
        let home = tempfile::tempdir().unwrap();
        std::fs::write(
            home.path().join(".profile"),
            "echo 'Welcome back'\nprintf 'no newline'\nexport PATH=/opt/kamui-test/bin:$PATH\nexport SSH_AUTH_SOCK=/tmp/agent.sock\n",
        )
        .unwrap();
        let home_path = home.path().to_str().unwrap();
        let environment = login_environment(
            &ProcessRunner,
            &env(&[
                ("SHELL", "/bin/sh"),
                ("HOME", home_path),
                ("PATH", "/usr/bin:/bin"),
            ]),
        );

        assert_eq!(
            environment.get("SSH_AUTH_SOCK").map(String::as_str),
            Some("/tmp/agent.sock")
        );
        assert_eq!(
            environment.get("PATH").and_then(|p| p.split(':').next()),
            Some("/opt/kamui-test/bin")
        );
    }

    #[test]
    fn parses_only_complete_marked_payload() {
        let marker = "kamui-environment-test";
        let parse = |text: String| parse(text.as_bytes(), marker);

        assert_eq!(
            parse(format!(
                "Welcome\n{marker}\0/opt/homebrew/bin:/usr/bin\0/tmp/agent.sock\0"
            )),
            Some(env(&[
                ("PATH", "/opt/homebrew/bin:/usr/bin"),
                ("SSH_AUTH_SOCK", "/tmp/agent.sock")
            ]))
        );
        assert_eq!(
            parse(format!("{marker}\0/usr/bin\0\0")),
            Some(env(&[("PATH", "/usr/bin")]))
        );
        assert_eq!(
            parse("/usr/bin\0/tmp/agent.sock\0".into()),
            None,
            "missing marker"
        );
        assert_eq!(
            parse(format!("{marker}\0/usr/bin\0")),
            None,
            "truncated payload"
        );
        assert_eq!(
            parse(format!(
                "{marker}\0/usr/bin:/Applications/My Tools/bin:~/.dotnet/tools\0\0"
            )),
            Some(env(&[(
                "PATH",
                "/usr/bin:/Applications/My Tools/bin:~/.dotnet/tools"
            )]))
        );
        assert_eq!(
            parse(format!("{marker}\0Welcome\n/usr/bin\0\0")),
            None,
            "PATH with startup output"
        );
        assert_eq!(parse(format!("{marker}\0\0\0")), None, "empty PATH");
    }

    #[test]
    fn keeps_base_environment_when_login_shell_fails() {
        let base = env(&[("PATH", "/usr/bin"), ("SSH_AUTH_SOCK", "/launchd/agent")]);
        let failing = FakeRunner::new(|_| CommandResult::new(1));
        assert_eq!(login_environment(failing.as_ref(), &base), base);
        let unmarked = FakeRunner::new(|_| CommandResult::new(0).stdout("/opt/homebrew/bin\0\0"));
        assert_eq!(login_environment(unmarked.as_ref(), &base), base);
    }
}
