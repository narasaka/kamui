//! Runs external commands. Tests substitute a fake for [`ProcessRunner`].

use std::collections::HashMap;
use std::fs::{File, OpenOptions};
use std::io::{Read, Write};
use std::os::unix::fs::OpenOptionsExt;
use std::path::Path;
use std::process::{Command, Stdio};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use super::KamuiError;

#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct CommandResult {
    pub exit_status: i32,
    pub stdout: Vec<u8>,
    pub stderr: String,
}

impl CommandResult {
    pub fn new(exit_status: i32) -> Self {
        Self {
            exit_status,
            ..Self::default()
        }
    }

    pub fn stdout(mut self, text: &str) -> Self {
        self.stdout = text.as_bytes().to_vec();
        self
    }

    pub fn stderr(mut self, text: &str) -> Self {
        self.stderr = text.into();
        self
    }
}

/// Receives each line of a command's output while the command runs.
pub type LineHandler = Box<dyn FnMut(&str) + Send>;

/// Optional interaction with a running command.
#[derive(Default)]
pub struct CommandIo {
    pub on_stderr_line: Option<LineHandler>,
    pub on_stdout_line: Option<LineHandler>,
    /// Connects the command's standard input to a pipe; otherwise it reads
    /// from the null device.
    pub input: Option<CommandInput>,
}

type Writer = Box<dyn FnMut(&[u8]) -> std::io::Result<()> + Send>;

/// Writes to a running command's standard input and pauses its timeout while
/// the command waits for the user.
#[derive(Clone, Default)]
pub struct CommandInput {
    inner: Arc<Mutex<InputState>>,
}

#[derive(Default)]
struct InputState {
    writer: Option<Writer>,
    holds: usize,
}

impl CommandInput {
    pub fn new() -> Self {
        Self::default()
    }

    /// Connects the input to a destination. The runner calls this before the
    /// command starts; tests may attach a recorder.
    pub fn attach(&self, writer: Writer) {
        self.inner.lock().unwrap().writer = Some(writer);
    }

    /// Writes data, or fails when the command has finished.
    pub fn write(&self, data: &[u8]) -> Result<(), KamuiError> {
        let mut state = self.inner.lock().unwrap();
        let writer = state.writer.as_mut().ok_or(KamuiError::CommandFinished)?;
        writer(data).map_err(|_| KamuiError::CommandFinished)
    }

    /// Stops accepting writes and closes the destination.
    pub fn close(&self) {
        self.inner.lock().unwrap().writer = None;
    }

    /// Pauses the command's timeout until a matching `release_timeout`.
    pub fn hold_timeout(&self) {
        self.inner.lock().unwrap().holds += 1;
    }

    pub fn release_timeout(&self) {
        let mut state = self.inner.lock().unwrap();
        state.holds = state.holds.saturating_sub(1);
    }

    pub fn holds_timeout(&self) -> bool {
        self.inner.lock().unwrap().holds > 0
    }
}

pub trait CommandRunner: Send + Sync {
    fn run(
        &self,
        executable: &Path,
        arguments: &[String],
        environment: &HashMap<String, String>,
        timeout: Duration,
        io: CommandIo,
    ) -> Result<CommandResult, KamuiError>;
}

/// Runs a process with its output captured in temporary files.
///
/// A kamui invocation may start the background controller, which inherits the
/// invocation's standard streams for its lifetime. Pipes would therefore never
/// reach end-of-file, so this runner waits for the direct child to exit and
/// then reads files instead. Line handlers receive complete lines as the files
/// grow, so the caller can act on output, such as a sign-in link or an OpenSSH
/// prompt, before the command finishes. Standard input, when requested, is a
/// pipe; the timeout does not count time in which the input holds it.
///
/// It blocks the calling thread; callers run it off the UI thread.
#[derive(Debug, Clone, Copy, Default)]
pub struct ProcessRunner;

impl ProcessRunner {
    /// How long a timed-out process may take to exit after SIGTERM before it
    /// receives SIGKILL.
    const TERMINATION_GRACE: Duration = Duration::from_secs(2);
    /// How often the runner checks for exit and new output.
    const POLL_INTERVAL: Duration = Duration::from_millis(50);
}

impl CommandRunner for ProcessRunner {
    fn run(
        &self,
        executable: &Path,
        arguments: &[String],
        environment: &HashMap<String, String>,
        timeout: Duration,
        mut io: CommandIo,
    ) -> Result<CommandResult, KamuiError> {
        let launch = |error: std::io::Error| KamuiError::Launch(error.to_string());
        let directory = tempfile::Builder::new()
            .prefix("kamui-")
            .tempdir()
            .map_err(launch)?;
        let output_path = directory.path().join("stdout");
        let error_path = directory.path().join("stderr");
        let create = |path: &Path| {
            OpenOptions::new()
                .write(true)
                .create_new(true)
                .mode(0o600)
                .open(path)
        };
        let output = create(&output_path).map_err(launch)?;
        let error = create(&error_path).map_err(launch)?;

        let mut command = Command::new(executable);
        command
            .args(arguments)
            .env_clear()
            .envs(environment)
            .stdout(output)
            .stderr(error)
            .stdin(if io.input.is_some() {
                Stdio::piped()
            } else {
                Stdio::null()
            });
        let mut child = command.spawn().map_err(launch)?;
        if let (Some(input), Some(mut stdin)) = (&io.input, child.stdin.take()) {
            // Rust ignores SIGPIPE, so a write after kamui exits fails instead.
            input.attach(Box::new(move |data| {
                stdin.write_all(data)?;
                stdin.flush()
            }));
        }
        let mut readers: Vec<LineReader> = [
            io.on_stderr_line.take().map(|h| (error_path.as_path(), h)),
            io.on_stdout_line.take().map(|h| (output_path.as_path(), h)),
        ]
        .into_iter()
        .flatten()
        .filter_map(|(path, handler)| LineReader::open(path, handler).ok())
        .collect();

        let mut deadline = Instant::now() + timeout;
        let mut checked = Instant::now();
        let status = loop {
            let exited = child.try_wait().map_err(launch)?;
            readers.iter_mut().for_each(LineReader::drain);
            if let Some(status) = exited {
                break Some(status);
            }
            let now = Instant::now();
            if io.input.as_ref().is_some_and(CommandInput::holds_timeout) {
                // Time spent waiting for the user does not count.
                deadline += now - checked;
            }
            checked = now;
            if now >= deadline {
                break None;
            }
            std::thread::sleep(Self::POLL_INTERVAL.min(deadline - now));
        };
        if let Some(input) = &io.input {
            input.close();
        }
        let Some(status) = status else {
            terminate(&mut child);
            let mut words = vec![
                executable
                    .file_name()
                    .map_or_else(String::new, |n| n.to_string_lossy().into_owned()),
            ];
            words.extend(arguments.iter().cloned());
            return Err(KamuiError::TimedOut(words.join(" ")));
        };
        for reader in &mut readers {
            reader.drain();
            reader.finish();
        }
        Ok(CommandResult {
            exit_status: status.code().unwrap_or(-1),
            stdout: std::fs::read(&output_path).unwrap_or_default(),
            stderr: String::from_utf8_lossy(&std::fs::read(&error_path).unwrap_or_default())
                .into_owned(),
        })
    }
}

/// Sends SIGTERM, then SIGKILL if the process has not exited after the grace
/// period.
fn terminate(child: &mut std::process::Child) {
    let pid = child.id() as libc::pid_t;
    let wait = |child: &mut std::process::Child| {
        let until = Instant::now() + ProcessRunner::TERMINATION_GRACE;
        while Instant::now() < until {
            if matches!(child.try_wait(), Ok(Some(_))) {
                return true;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        false
    };
    // SAFETY: kill only sends a signal to the child this runner started.
    unsafe { libc::kill(pid, libc::SIGTERM) };
    if !wait(child) {
        let _ = child.kill();
        wait(child);
    }
}

/// Reads lines appended to a file and passes each complete one to a handler.
struct LineReader {
    file: File,
    handler: LineHandler,
    pending: Vec<u8>,
}

impl LineReader {
    fn open(path: &Path, handler: LineHandler) -> std::io::Result<Self> {
        Ok(Self {
            file: File::open(path)?,
            handler,
            pending: Vec::new(),
        })
    }

    fn drain(&mut self) {
        let _ = self.file.read_to_end(&mut self.pending);
        while let Some(newline) = self.pending.iter().position(|&b| b == b'\n') {
            let line: Vec<u8> = self.pending.drain(..=newline).collect();
            (self.handler)(&String::from_utf8_lossy(&line[..line.len() - 1]));
        }
    }

    /// Passes a final line that has no newline.
    fn finish(&mut self) {
        if !self.pending.is_empty() {
            let line = std::mem::take(&mut self.pending);
            (self.handler)(&String::from_utf8_lossy(&line));
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sh(script: &str, timeout: Duration, io: CommandIo) -> Result<CommandResult, KamuiError> {
        ProcessRunner.run(
            Path::new("/bin/sh"),
            &["-c".into(), script.into()],
            &HashMap::new(),
            timeout,
            io,
        )
    }

    #[test]
    fn captures_output_after_direct_child_exits_even_when_a_descendant_keeps_streams_open() {
        // The background child mimics the kamui controller, which inherits
        // the invoking command's standard streams and outlives it.
        let started = Instant::now();
        let result = sh(
            "printf out; printf err >&2; (sleep 30 &) ; exit 4",
            Duration::from_secs(10),
            CommandIo::default(),
        )
        .unwrap();
        assert!(started.elapsed() < Duration::from_secs(10));
        assert_eq!(result, CommandResult::new(4).stdout("out").stderr("err"));
    }

    #[test]
    fn streams_standard_error_lines_before_child_exits() {
        let lines = Arc::new(Mutex::new(Vec::new()));
        let seen = lines.clone();
        let started = Instant::now();
        let result = sh(
            "printf 'visit: https://login.tailscale.com/a/x\\n' >&2; sleep 2; printf 'last' >&2",
            Duration::from_secs(10),
            CommandIo {
                on_stderr_line: Some(Box::new(move |line| {
                    seen.lock()
                        .unwrap()
                        .push((line.to_owned(), started.elapsed()))
                })),
                ..CommandIo::default()
            },
        )
        .unwrap();
        let lines = lines.lock().unwrap();
        assert_eq!(
            lines.iter().map(|l| l.0.as_str()).collect::<Vec<_>>(),
            ["visit: https://login.tailscale.com/a/x", "last"]
        );
        assert!(
            lines[0].1 < Duration::from_secs(1),
            "the first line arrived only after the child exited"
        );
        assert_eq!(
            result.stderr,
            "visit: https://login.tailscale.com/a/x\nlast"
        );
    }

    #[test]
    fn timeout_kills_child_that_ignores_termination() {
        let started = Instant::now();
        let result = sh(
            "trap '' TERM; while :; do sleep 1; done",
            Duration::from_millis(100),
            CommandIo::default(),
        );
        assert_eq!(
            result,
            Err(KamuiError::TimedOut(
                "sh -c trap '' TERM; while :; do sleep 1; done".into()
            ))
        );
        assert!(started.elapsed() < Duration::from_secs(5));
    }

    #[test]
    fn times_out() {
        let result = ProcessRunner.run(
            Path::new("/bin/sleep"),
            &["5".into()],
            &HashMap::new(),
            Duration::from_millis(100),
            CommandIo::default(),
        );
        assert_eq!(result, Err(KamuiError::TimedOut("sleep 5".into())));
    }

    #[test]
    fn timeout_pauses_while_input_holds_it() {
        let input = CommandInput::new();
        let answering = input.clone();
        let started = Instant::now();
        let result = sh(
            "echo ask; read -r answer; echo \"got $answer\" >&2",
            Duration::from_millis(500),
            CommandIo {
                on_stdout_line: Some(Box::new(move |line| {
                    if line != "ask" {
                        return;
                    }
                    answering.hold_timeout();
                    let answering = answering.clone();
                    std::thread::spawn(move || {
                        // The user takes longer than the timeout to answer.
                        std::thread::sleep(Duration::from_secs(1));
                        let _ = answering.write(b"42\n");
                        answering.release_timeout();
                    });
                })),
                input: Some(input.clone()),
                ..CommandIo::default()
            },
        )
        .unwrap();
        assert_eq!(result.stderr, "got 42\n");
        assert!(started.elapsed() > Duration::from_millis(900));
        assert_eq!(
            input.write(b"late\n"),
            Err(KamuiError::CommandFinished),
            "input closes when the command exits"
        );
    }

    #[test]
    fn uses_only_the_given_environment() {
        let mut environment = HashMap::new();
        environment.insert("KAMUI_TEST".to_owned(), "yes".to_owned());
        let result = ProcessRunner
            .run(
                Path::new("/usr/bin/env"),
                &[],
                &environment,
                Duration::from_secs(5),
                CommandIo::default(),
            )
            .unwrap();
        assert_eq!(
            String::from_utf8(result.stdout).unwrap(),
            "KAMUI_TEST=yes\n"
        );
    }
}
