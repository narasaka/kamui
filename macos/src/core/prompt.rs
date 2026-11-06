//! The JSON lines that `kamui mirror --json-prompts` writes and reads
//! (ADR 0015), and the pieces that answer them.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

use serde::Deserialize;

use super::runner::CommandInput;

/// One OpenSSH question relayed by `kamui mirror --json-prompts`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SshPrompt {
    pub id: i64,
    pub kind: PromptKind,
    pub text: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PromptKind {
    /// Typed input, always hidden. Prompt text comes partly from the server,
    /// so it never decides whether input is shown.
    Secret,
    /// A yes-or-no question.
    Confirm,
    /// Information, such as "touch your security key", that needs no answer.
    Notice,
}

/// The user's response to a prompt.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PromptReply {
    Answer(String),
    Decline,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Message {
    Prompt(SshPrompt),
    Cancel(i64),
}

#[derive(Deserialize)]
struct Line {
    prompt: Option<LinePrompt>,
    cancel: Option<i64>,
}

#[derive(Deserialize)]
struct LinePrompt {
    id: i64,
    kind: String,
    text: String,
}

/// Decodes one line, or returns None for anything else. An unknown kind is
/// treated as hidden input.
pub fn message(line: &str) -> Option<Message> {
    let line: Line = serde_json::from_str(line).ok()?;
    if let Some(prompt) = line.prompt {
        let kind = match prompt.kind.as_str() {
            "confirm" => PromptKind::Confirm,
            "notice" => PromptKind::Notice,
            _ => PromptKind::Secret,
        };
        return Some(Message::Prompt(SshPrompt {
            id: prompt.id,
            kind,
            text: prompt.text,
        }));
    }
    line.cancel.filter(|id| *id > 0).map(Message::Cancel)
}

/// Encodes a reply as one line, with keys in sorted order. The answer is
/// never logged.
pub fn reply_line(reply: &PromptReply, id: i64) -> Vec<u8> {
    let value = match reply {
        PromptReply::Answer(answer) => serde_json::json!({ "answer": answer, "id": id }),
        PromptReply::Decline => serde_json::json!({ "declined": true, "id": id }),
    };
    let mut line = serde_json::to_vec(&value).unwrap_or_default();
    line.push(b'\n');
    line
}

/// Answers one prompt, once. While it is outstanding it holds the command's
/// timeout, so time spent waiting for the user does not count.
#[derive(Clone)]
pub struct PromptResponder {
    inner: Arc<ResponderInner>,
}

struct ResponderInner {
    id: i64,
    input: CommandInput,
    done: AtomicBool,
}

impl PromptResponder {
    pub(crate) fn new(id: i64, input: CommandInput) -> Self {
        input.hold_timeout();
        Self {
            inner: Arc::new(ResponderInner {
                id,
                input,
                done: AtomicBool::new(false),
            }),
        }
    }

    /// A responder that answers nowhere, for previews and tests of the UI.
    pub fn detached(id: i64) -> Self {
        Self::new(id, CommandInput::new())
    }

    pub fn id(&self) -> i64 {
        self.inner.id
    }

    /// Writes the reply to kamui. Later replies, and replies after kamui
    /// withdrew the prompt, do nothing.
    pub fn reply(&self, reply: PromptReply) {
        if !self.inner.done.swap(true, Ordering::SeqCst) {
            let _ = self.inner.input.write(&reply_line(&reply, self.inner.id));
            self.inner.input.release_timeout();
        }
    }

    /// Ends the prompt without answering, because kamui no longer needs it.
    pub(crate) fn withdraw(&self) {
        if !self.inner.done.swap(true, Ordering::SeqCst) {
            self.inner.input.release_timeout();
        }
    }

    pub fn is_done(&self) -> bool {
        self.inner.done.load(Ordering::SeqCst)
    }
}

impl std::fmt::Debug for PromptResponder {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("PromptResponder")
            .field("id", &self.inner.id)
            .finish_non_exhaustive()
    }
}

impl PartialEq for PromptResponder {
    fn eq(&self, other: &Self) -> bool {
        Arc::ptr_eq(&self.inner, &other.inner)
    }
}

/// Answers prompts for one connection. Calls arrive on a background thread.
pub trait PromptHandler: Send + Sync {
    /// Shows a question. The handler replies through `responder`, from any
    /// thread, whenever the user answers.
    fn ask(&self, prompt: SshPrompt, responder: PromptResponder);
    /// kamui no longer needs the answer to prompt `id`, for example because
    /// the attempt timed out or the session was stopped.
    fn withdraw(&self, id: i64);
    /// Information, such as "Confirm user presence for key", shown while the
    /// connection waits.
    fn notice(&self, text: &str);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn decodes_prompts_and_cancellations() {
        assert_eq!(
            message(
                r#"{"prompt":{"id":1,"kind":"secret","text":"Enter passphrase for key '/k': "}}"#
            ),
            Some(Message::Prompt(SshPrompt {
                id: 1,
                kind: PromptKind::Secret,
                text: "Enter passphrase for key '/k': ".into()
            }))
        );
        assert_eq!(
            message(r#"{"prompt":{"id":2,"kind":"confirm","text":"Allow?"}}"#),
            Some(Message::Prompt(SshPrompt {
                id: 2,
                kind: PromptKind::Confirm,
                text: "Allow?".into()
            }))
        );
        assert_eq!(
            message(r#"{"prompt":{"id":3,"kind":"future","text":"?"}}"#),
            Some(Message::Prompt(SshPrompt {
                id: 3,
                kind: PromptKind::Secret,
                text: "?".into()
            })),
            "unknown kinds stay hidden"
        );
        assert_eq!(message(r#"{"cancel":4}"#), Some(Message::Cancel(4)));
        assert_eq!(message("reyna mirroring enabled"), None);
        assert_eq!(message("{}"), None);
        assert_eq!(message(r#"{"cancel":0}"#), None);
    }

    #[test]
    fn encodes_replies_as_one_line() {
        assert_eq!(
            String::from_utf8(reply_line(
                &PromptReply::Answer("open \"sesame\"".into()),
                2
            ))
            .unwrap(),
            "{\"answer\":\"open \\\"sesame\\\"\",\"id\":2}\n"
        );
        assert_eq!(
            String::from_utf8(reply_line(&PromptReply::Decline, 3)).unwrap(),
            "{\"declined\":true,\"id\":3}\n"
        );
    }

    #[test]
    fn a_responder_answers_once_and_releases_the_timeout() {
        let input = CommandInput::new();
        let written = std::sync::Arc::new(std::sync::Mutex::new(Vec::new()));
        let sink = written.clone();
        input.attach(Box::new(move |data: &[u8]| {
            sink.lock().unwrap().extend_from_slice(data);
            Ok(())
        }));
        let responder = PromptResponder::new(7, input.clone());
        assert!(input.holds_timeout());
        responder.reply(PromptReply::Answer("first".into()));
        responder.reply(PromptReply::Answer("second".into()));
        assert!(!input.holds_timeout());
        assert_eq!(
            String::from_utf8(written.lock().unwrap().clone()).unwrap(),
            "{\"answer\":\"first\",\"id\":7}\n"
        );

        let withdrawn = PromptResponder::new(8, input.clone());
        withdrawn.withdraw();
        withdrawn.reply(PromptReply::Decline);
        assert!(!input.holds_timeout());
        assert!(
            !String::from_utf8(written.lock().unwrap().clone())
                .unwrap()
                .contains("\"id\":8")
        );
    }
}
