use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use url::Url;

use super::prompt::{self, Message, PromptHandler, PromptKind, PromptResponder};
use super::runner::{CommandInput, CommandIo, CommandResult, CommandRunner, ProcessRunner};
use super::{KamuiError, StatusDocument, destination};

/// Drives the kamui CLI: the user's installed one or the copy inside the app.
/// The app never talks to the controller socket directly, so it stays
/// compatible with the CLI's controller handoff (ADR 0007).
///
/// Every call blocks; the app makes them off the UI thread.
#[derive(Clone)]
pub struct KamuiClient {
    pub executable: PathBuf,
    pub environment: HashMap<String, String>,
    pub runner: Arc<dyn CommandRunner>,
}

impl KamuiClient {
    pub fn new(executable: PathBuf, environment: HashMap<String, String>) -> Self {
        Self {
            executable,
            environment,
            runner: Arc::new(ProcessRunner),
        }
    }

    pub fn with_runner(mut self, runner: Arc<dyn CommandRunner>) -> Self {
        self.runner = runner;
        self
    }

    /// Reports sessions without starting the controller.
    pub fn status(&self) -> Result<StatusDocument, KamuiError> {
        let result = self.run(
            &["status", "--json"],
            Duration::from_secs(20),
            CommandIo::default(),
        )?;
        StatusDocument::decode(&result.stdout)
    }

    /// Starts or reuses a mirroring session and returns any warnings kamui
    /// printed, such as enabled agent forwarding. The explicit `mirror`
    /// command keeps a destination named like a subcommand, such as `status`,
    /// a destination.
    ///
    /// With a prompt handler, kamui relays OpenSSH's passphrase, password,
    /// host-key, and other prompts to it (`--json-prompts`). Answers pass
    /// through memory to kamui's standard input and are never stored or
    /// logged. Time spent waiting for an answer does not count toward the
    /// timeout. Without one, OpenSSH may not prompt (`--non-interactive`).
    ///
    /// A Tailscale SSH check prints a sign-in link and holds the connection
    /// until the user signs in. `on_sign_in` receives each such link once,
    /// while the command is still waiting.
    pub fn connect(
        &self,
        destination: &str,
        prompts: Option<Arc<dyn PromptHandler>>,
        on_sign_in: impl Fn(Url) + Send + 'static,
    ) -> Result<Vec<String>, KamuiError> {
        let destination = destination::validate(destination)?;
        let mut seen = HashSet::new();
        let mut io = CommandIo {
            on_stderr_line: Some(Box::new(move |line| {
                if let Some(url) = sign_in_url(line)
                    && seen.insert(url.clone())
                {
                    on_sign_in(url);
                }
            })),
            ..CommandIo::default()
        };
        let outstanding: Arc<Mutex<HashMap<i64, PromptResponder>>> = Arc::default();
        let mode = if let Some(prompts) = &prompts {
            let input = CommandInput::new();
            io.input = Some(input.clone());
            let prompts = prompts.clone();
            let outstanding = outstanding.clone();
            io.on_stdout_line = Some(Box::new(move |line| match prompt::message(line) {
                Some(Message::Prompt(prompt)) if prompt.kind == PromptKind::Notice => {
                    prompts.notice(&prompt.text)
                }
                Some(Message::Prompt(prompt)) => {
                    let responder = PromptResponder::new(prompt.id, input.clone());
                    outstanding
                        .lock()
                        .unwrap()
                        .insert(prompt.id, responder.clone());
                    prompts.ask(prompt, responder);
                }
                Some(Message::Cancel(id)) => {
                    if let Some(responder) = outstanding.lock().unwrap().remove(&id) {
                        responder.withdraw();
                    }
                    prompts.withdraw(id);
                }
                None => {}
            }));
            "--json-prompts"
        } else {
            "--non-interactive"
        };
        let result = self.run(
            &["mirror", mode, &destination],
            Duration::from_secs(120),
            io,
        );
        if let Some(prompts) = &prompts {
            for (id, responder) in outstanding.lock().unwrap().drain() {
                responder.withdraw();
                prompts.withdraw(id);
            }
        }
        let result = match result {
            Err(KamuiError::CommandFailed(message))
                if prompts.is_some() && rejects_json_prompts(&message) =>
            {
                return Err(KamuiError::PromptsUnsupported(
                    self.executable.display().to_string(),
                ));
            }
            other => other?,
        };
        Ok(result
            .stderr
            .lines()
            .map(str::trim)
            .filter(|line| line.starts_with("WARNING:"))
            .map(str::to_owned)
            .collect())
    }

    /// Stops a session. A session that is already gone counts as stopped:
    /// another stop, the terminal, or a controller that has exited got there
    /// first, and either way it isn't running.
    pub fn stop(&self, destination: &str) -> Result<(), KamuiError> {
        let destination = destination::validate(destination)?;
        match self.run(
            &["stop", &destination],
            Duration::from_secs(30),
            CommandIo::default(),
        ) {
            Err(KamuiError::CommandFailed(message)) if is_absent(&message, &destination) => Ok(()),
            other => other.map(drop),
        }
    }

    pub fn stop_all(&self) -> Result<(), KamuiError> {
        self.run(
            &["stop", "--all"],
            Duration::from_secs(30),
            CommandIo::default(),
        )
        .map(drop)
    }

    fn run(
        &self,
        arguments: &[&str],
        timeout: Duration,
        io: CommandIo,
    ) -> Result<CommandResult, KamuiError> {
        let arguments: Vec<String> = arguments.iter().map(|a| (*a).to_owned()).collect();
        let result =
            self.runner
                .run(&self.executable, &arguments, &self.environment, timeout, io)?;
        if result.exit_status != 0 {
            return Err(KamuiError::CommandFailed(failure_message(&result)));
        }
        Ok(result)
    }
}

/// Whether kamui's stop failed only because it had no such session.
fn is_absent(message: &str, destination: &str) -> bool {
    message.trim() == format!("no Kamui session for {destination}")
}

/// Reports whether an older kamui rejected `--json-prompts`.
fn rejects_json_prompts(message: &str) -> bool {
    message.contains("flag provided but not defined") && message.contains("json-prompts")
}

/// Finds the link in Tailscale SSH's check message, such as
/// `# To authenticate, visit: https://login.tailscale.com/a/…`. Only HTTPS
/// links are accepted.
pub fn sign_in_url(line: &str) -> Option<Url> {
    let mut text = line.trim();
    if let Some(rest) = text.strip_prefix('#') {
        text = rest.trim_start_matches(' ');
    }
    let link = text.strip_prefix("To authenticate, visit:")?.trim();
    // A URL parser repairs `https:///host` into `https://host/`; a link
    // without an authority is not one Tailscale would print.
    let authority = link
        .get(..8)
        .filter(|s| s.eq_ignore_ascii_case("https://"))
        .map(|_| &link[8..])?;
    if authority.is_empty() || authority.starts_with('/') {
        return None;
    }
    let url = Url::parse(link).ok()?;
    (url.scheme() == "https" && url.host_str().is_some_and(|h| !h.is_empty())).then_some(url)
}

/// Reports whether a sign-in link goes to Tailscale's own login service,
/// which the app opens without asking. The SSH server chooses the link, so
/// the app confirms any other host, such as a self-hosted Headscale server.
pub fn is_tailscale_login(url: &Url) -> bool {
    url.host_str()
        .is_some_and(|h| h.eq_ignore_ascii_case("login.tailscale.com"))
}

pub fn failure_message(result: &CommandResult) -> String {
    let message = result
        .stderr
        .split('\n')
        .map(|line| line.strip_prefix("✗ ").unwrap_or(line))
        .collect::<Vec<_>>()
        .join("\n");
    let message = message.trim();
    if message.is_empty() {
        format!("kamui exited with status {}.", result.exit_status)
    } else {
        message.to_owned()
    }
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use crate::core::prompt::{PromptReply, SshPrompt};
    use crate::core::{Session, SessionState};
    use std::path::Path;

    type Respond = Box<dyn Fn(&[String]) -> CommandResult + Send + Sync>;

    #[derive(Debug, Clone, PartialEq, Eq)]
    pub struct Invocation {
        pub executable: String,
        pub arguments: Vec<String>,
        pub environment: HashMap<String, String>,
    }

    /// Records each invocation and answers with canned output, passing
    /// standard error to the line handler as the real runner does.
    pub struct FakeRunner {
        respond: Respond,
        pub invocations: Mutex<Vec<Invocation>>,
    }

    impl FakeRunner {
        pub fn new(
            respond: impl Fn(&[String]) -> CommandResult + Send + Sync + 'static,
        ) -> Arc<Self> {
            Arc::new(Self {
                respond: Box::new(respond),
                invocations: Mutex::default(),
            })
        }

        pub fn arguments(&self) -> Vec<Vec<String>> {
            self.invocations
                .lock()
                .unwrap()
                .iter()
                .map(|i| i.arguments.clone())
                .collect()
        }
    }

    impl CommandRunner for FakeRunner {
        fn run(
            &self,
            executable: &Path,
            arguments: &[String],
            environment: &HashMap<String, String>,
            _timeout: Duration,
            mut io: CommandIo,
        ) -> Result<CommandResult, KamuiError> {
            self.invocations.lock().unwrap().push(Invocation {
                executable: executable.display().to_string(),
                arguments: arguments.to_vec(),
                environment: environment.clone(),
            });
            let result = (self.respond)(arguments);
            if let Some(handler) = io.on_stderr_line.as_mut() {
                result.stderr.lines().for_each(handler);
            }
            Ok(result)
        }
    }

    fn client(runner: Arc<FakeRunner>) -> KamuiClient {
        KamuiClient::new("/opt/homebrew/bin/kamui".into(), HashMap::new()).with_runner(runner)
    }

    struct Declines;
    impl PromptHandler for Declines {
        fn ask(&self, _: SshPrompt, responder: PromptResponder) {
            responder.reply(PromptReply::Decline);
        }
        fn withdraw(&self, _: i64) {}
        fn notice(&self, _: &str) {}
    }

    #[test]
    fn status_uses_json_and_environment() {
        let runner =
            FakeRunner::new(|_| CommandResult::new(0).stdout(r#"{"format":1,"sessions":[]}"#));
        let mut environment = HashMap::new();
        environment.insert("PATH".to_owned(), "/opt/homebrew/bin:/usr/bin".to_owned());
        let client = KamuiClient::new("/opt/homebrew/bin/kamui".into(), environment.clone())
            .with_runner(runner.clone());

        assert_eq!(client.status().unwrap().sessions, vec![]);
        assert_eq!(
            *runner.invocations.lock().unwrap(),
            vec![Invocation {
                executable: "/opt/homebrew/bin/kamui".into(),
                arguments: vec!["status".into(), "--json".into()],
                environment,
            }]
        );
    }

    #[test]
    fn connect_passes_destination_as_one_argument() {
        let runner = FakeRunner::new(|_| CommandResult::new(0));
        let client = client(runner.clone());

        client
            .connect(" nathan@192.168.1.50 ", None, |_| {})
            .unwrap();
        client.connect("status", None, |_| {}).unwrap();
        client
            .connect("reyna", Some(Arc::new(Declines)), |_| {})
            .unwrap();
        client.stop("nathan@192.168.1.50").unwrap();
        client.stop_all().unwrap();

        let expected: Vec<Vec<String>> = [
            vec!["mirror", "--non-interactive", "nathan@192.168.1.50"],
            vec!["mirror", "--non-interactive", "status"],
            vec!["mirror", "--json-prompts", "reyna"],
            vec!["stop", "nathan@192.168.1.50"],
            vec!["stop", "--all"],
        ]
        .iter()
        .map(|a| a.iter().map(|s| (*s).to_owned()).collect())
        .collect();
        assert_eq!(runner.arguments(), expected);
    }

    #[test]
    fn connect_returns_cli_warnings() {
        let runner = FakeRunner::new(|_| {
            CommandResult::new(0).stderr("Tailscale check passed\nWARNING: SSH agent forwarding is enabled for reyna; the remote host can access the forwarded agent.\n")
        });
        assert_eq!(
            client(runner).connect("reyna", None, |_| {}).unwrap(),
            vec![
                "WARNING: SSH agent forwarding is enabled for reyna; the remote host can access the forwarded agent."
            ]
        );
    }

    #[test]
    fn connect_opens_each_tailscale_sign_in_link_once_even_when_connection_fails() {
        let runner = FakeRunner::new(|_| {
            CommandResult::new(1).stderr(
                "# Tailscale SSH requires an additional check.\n# To authenticate, visit: https://login.tailscale.com/a/example\n# To authenticate, visit: https://login.tailscale.com/a/example\nTo authenticate, visit: https://headscale.example.com/a/other\n✗ SSH authentication or connection failed for reyna",
            )
        });
        let opened = Arc::new(Mutex::new(Vec::new()));
        let sink = opened.clone();
        let _ = client(runner).connect("reyna", None, move |url| {
            sink.lock().unwrap().push(url.to_string())
        });
        assert_eq!(
            *opened.lock().unwrap(),
            [
                "https://login.tailscale.com/a/example",
                "https://headscale.example.com/a/other"
            ]
        );
    }

    #[test]
    fn sign_in_url_accepts_only_https_links_in_tailscale_check_message() {
        let url = |line| sign_in_url(line).map(|u| u.to_string());
        assert_eq!(
            url("# To authenticate, visit: https://login.tailscale.com/a/abc123").as_deref(),
            Some("https://login.tailscale.com/a/abc123")
        );
        assert_eq!(
            url("  To authenticate, visit:  https://login.tailscale.com/a/abc123  ").as_deref(),
            Some("https://login.tailscale.com/a/abc123")
        );
        assert_eq!(
            url("# To authenticate, visit: https://login.tailscale.com/a/abc123\r").as_deref(),
            Some("https://login.tailscale.com/a/abc123")
        );
        assert_eq!(
            url("# To authenticate, visit: http://login.tailscale.com/a/abc123"),
            None
        );
        assert_eq!(
            url("# To authenticate, visit: file:///Applications/Calculator.app"),
            None
        );
        assert_eq!(url("# To authenticate, visit: https:///missing-host"), None);
        assert_eq!(url("# Tailscale SSH requires an additional check."), None);
        assert_eq!(url("Welcome! See https://example.com"), None);
    }

    #[test]
    fn only_tailscale_login_host_opens_without_confirmation() {
        let login = |s: &str| is_tailscale_login(&Url::parse(s).unwrap());
        assert!(login("https://login.tailscale.com/a/abc123"));
        assert!(login("https://LOGIN.tailscale.com/a/abc123"));
        assert!(!login("https://headscale.example.com/a/abc123"));
        assert!(!login("https://login.tailscale.com.evil.example/a/abc123"));
        assert!(!login("https://login.tailscale.com@evil.example/a/abc123"));
    }

    #[test]
    fn connect_rejects_option_like_destination_without_running_kamui() {
        let runner = FakeRunner::new(|_| CommandResult::new(0));
        assert!(
            client(runner.clone())
                .connect("-oProxyCommand=touch /tmp/pwned", None, |_| {})
                .is_err()
        );
        assert!(runner.arguments().is_empty());
    }

    #[test]
    fn failure_reports_cli_error_without_marker() {
        let runner = FakeRunner::new(|_| {
            CommandResult::new(1).stderr("✗ SSH authentication or connection failed for reyna: SSH authentication failed for reyna: exit status 255\nPermission denied (publickey).\n")
        });
        assert_eq!(
            client(runner).connect("reyna", None, |_| {}),
            Err(KamuiError::CommandFailed(
                "SSH authentication or connection failed for reyna: SSH authentication failed for reyna: exit status 255\nPermission denied (publickey).".into()
            ))
        );
        assert_eq!(
            failure_message(&CommandResult::new(3)),
            "kamui exited with status 3."
        );
    }

    #[test]
    fn stopping_a_session_that_is_already_gone_succeeds() {
        let runner = FakeRunner::new(|arguments| match arguments[1].as_str() {
            "pi" | "reyna" => CommandResult::new(1).stderr("✗ no Kamui session for pi\n"),
            _ => CommandResult::new(1).stderr("✗ the Kamui controller is unavailable\n"),
        });
        let client = client(runner);
        assert_eq!(client.stop("pi"), Ok(()));
        assert_eq!(
            client.stop("lab"),
            Err(KamuiError::CommandFailed(
                "the Kamui controller is unavailable".into()
            )),
            "other failures are still failures"
        );
        assert!(
            client.stop("reyna").is_err(),
            "another destination's missing session is not this one's"
        );
    }

    #[test]
    fn status_follows_sessions_changed_from_the_terminal() {
        // The app shows whatever the shared controller reports, so sessions
        // started or stopped from a terminal appear and disappear on the next
        // poll, and the app's stop goes to the same controller.
        let calls = Arc::new(Mutex::new(0));
        let counter = calls.clone();
        let runner = FakeRunner::new(move |arguments| {
            if arguments[0] != "status" {
                return CommandResult::new(0);
            }
            let mut calls = counter.lock().unwrap();
            *calls += 1;
            CommandResult::new(0).stdout(if *calls == 1 {
                r#"{"format":1,"sessions":[{"destination":"started-in-terminal","state":"connected","mirror":{"enabled":true,"mirroredPorts":[3000],"excludedPorts":[],"conflicts":[]}}]}"#
            } else {
                r#"{"format":1,"sessions":[]}"#
            })
        });
        let client = client(runner.clone());
        let mut expected = Session::new("started-in-terminal", SessionState::Connected);
        expected.mirror.enabled = true;
        expected.mirror.mirrored_ports = vec![3000];
        assert_eq!(client.status().unwrap().sessions, vec![expected]);
        client.stop("started-in-terminal").unwrap();
        assert_eq!(client.status().unwrap().sessions, vec![]);
        assert_eq!(runner.arguments().len(), 3);
    }

    /// A stand-in for `kamui mirror --json-prompts` that checks each reply on
    /// its standard input and, like the real CLI, may leave a background
    /// process holding its output.
    pub fn fake_kamui(body: &str) -> (tempfile::TempDir, PathBuf) {
        use std::os::unix::fs::PermissionsExt;
        let directory = tempfile::tempdir().unwrap();
        let script = directory.path().join("kamui");
        std::fs::write(&script, format!("#!/bin/sh\n{body}")).unwrap();
        std::fs::set_permissions(&script, std::fs::Permissions::from_mode(0o700)).unwrap();
        (directory, script)
    }

    #[derive(Default)]
    struct Recorder {
        asked: Mutex<Vec<SshPrompt>>,
        withdrawn: Mutex<Vec<i64>>,
        notices: Mutex<Vec<String>>,
    }

    impl PromptHandler for Recorder {
        fn ask(&self, prompt: SshPrompt, responder: PromptResponder) {
            self.asked.lock().unwrap().push(prompt.clone());
            match prompt.id {
                2 => responder.reply(PromptReply::Answer("open sesame".into())),
                // Waits until kamui withdraws the prompt.
                3 => {}
                _ => responder.reply(PromptReply::Decline),
            }
        }
        fn withdraw(&self, id: i64) {
            self.withdrawn.lock().unwrap().push(id);
        }
        fn notice(&self, text: &str) {
            self.notices.lock().unwrap().push(text.into());
        }
    }

    #[test]
    fn connect_answers_prompts_through_standard_input() {
        let (_directory, kamui) = fake_kamui(
            r#"[ "$1 $2 $3" = "mirror --json-prompts reyna" ] || { echo "arguments: $*" >&2; exit 2; }
(sleep 30 &)
echo '{"prompt":{"id":1,"kind":"notice","text":"Confirm user presence for key"}}'
echo '{"prompt":{"id":2,"kind":"secret","text":"Enter passphrase for key '"'"'/k'"'"': "}}'
read -r reply
[ "$reply" = '{"answer":"open sesame","id":2}' ] || { echo "reply 2: $reply" >&2; exit 3; }
echo '{"prompt":{"id":3,"kind":"secret","text":"Password: "}}'
sleep 1
echo '{"cancel":3}'
echo '{"prompt":{"id":4,"kind":"confirm","text":"Allow use of key?"}}'
read -r reply
[ "$reply" = '{"declined":true,"id":4}' ] || { echo "reply 4: $reply" >&2; exit 4; }
echo 'WARNING: SSH agent forwarding is enabled for reyna' >&2
"#,
        );
        let recorder = Arc::new(Recorder::default());
        let warnings = KamuiClient::new(kamui, HashMap::new())
            .connect("reyna", Some(recorder.clone()), |_| {})
            .unwrap();

        assert_eq!(
            warnings,
            ["WARNING: SSH agent forwarding is enabled for reyna"]
        );
        let asked = recorder.asked.lock().unwrap();
        assert_eq!(asked.iter().map(|p| p.id).collect::<Vec<_>>(), [2, 3, 4]);
        assert_eq!(
            asked.iter().map(|p| p.kind).collect::<Vec<_>>(),
            [PromptKind::Secret, PromptKind::Secret, PromptKind::Confirm]
        );
        assert_eq!(recorder.withdrawn.lock().unwrap().first(), Some(&3));
        assert_eq!(
            *recorder.notices.lock().unwrap(),
            ["Confirm user presence for key"]
        );
    }

    #[test]
    fn older_kamui_without_json_prompts_is_reported() {
        let (_directory, kamui) = fake_kamui(
            "echo 'Incorrect Usage: flag provided but not defined: -json-prompts' >&2; exit 1\n",
        );
        let client = KamuiClient::new(kamui.clone(), HashMap::new());
        assert_eq!(
            client.connect("reyna", Some(Arc::new(Declines)), |_| {}),
            Err(KamuiError::PromptsUnsupported(kamui.display().to_string()))
        );
    }
}
