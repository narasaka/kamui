//! Fixture states for headless snapshots: `kamui-app --snap out.png --scene
//! ports --theme dark`. `--list-scenes` lists them.

use std::cell::{Cell, RefCell};
use std::rc::Rc;

use rune::TextEdit;

use super::model::{Backend, Model, Page};
use super::onboarding::{Step, Tour};
use super::toast::Toast;
use crate::core::prompt::{PromptKind, PromptResponder, SshPrompt};
use crate::core::{Conflict, Session, SessionState};

/// Records what the model asked for and does none of it.
#[derive(Default)]
pub struct Recorder {
    pub calls: RefCell<Vec<String>>,
    /// Whether the window counts as in front.
    pub in_front: Cell<bool>,
    /// Whether toasts show, as they do in the app.
    pub toasts: Cell<bool>,
}

impl Recorder {
    pub fn calls(&self) -> Vec<String> {
        self.calls.borrow().clone()
    }

    fn record(&self, call: String) {
        self.calls.borrow_mut().push(call);
    }
}

impl Backend for Recorder {
    fn refresh(&self) {
        self.record("refresh".into());
    }
    fn connect(&self, destination: String) {
        self.record(format!("connect {destination}"));
    }
    fn stop(&self, destination: String, _id: u64) {
        self.record(format!("stop {destination}"));
    }
    fn stop_then_decline(&self, destination: String, responder: PromptResponder, _id: u64) {
        self.record(format!(
            "stop-then-decline {destination} {}",
            responder.id()
        ));
    }
    fn stop_all(&self, _id: u64) {
        self.record("stop-all".into());
    }
    fn set_launch_at_login(&self, enabled: bool) -> Result<bool, String> {
        self.record(format!("login {enabled}"));
        Ok(enabled)
    }
    fn save_executable_override(&self, path: Option<String>) {
        self.record(format!("override {}", path.unwrap_or_default()));
    }
    fn save_onboarded(&self, done: bool) {
        self.record(format!("onboarded {done}"));
    }
    fn show_window(&self) {
        self.record("show".into());
    }
    fn window_in_front(&self) -> bool {
        self.in_front.get()
    }
    fn toast(&self, toast: Toast) -> bool {
        if self.toasts.get() {
            self.record(format!(
                "toast {:?} {} ({})",
                toast.tone,
                toast.title,
                toast.destination.unwrap_or_default()
            ));
        }
        self.toasts.get()
    }
    fn window_closed(&self) {
        self.record("closed".into());
    }
    fn quit(&self) {
        self.record("quit".into());
    }
}

pub fn recorded() -> (Model, Rc<Recorder>) {
    let recorder = Rc::new(Recorder::default());
    (Model::new(recorder.clone()), recorder)
}

fn session(destination: &str, state: SessionState, ports: &[u16]) -> Session {
    let mut session = Session::new(destination, state);
    session.mirror.enabled = true;
    session.mirror.mirrored_ports = ports.to_vec();
    session.browser = Some("firefox".into());
    session
}

/// A Mac with kamui installed, four hosts in ~/.ssh/config and two sessions.
pub fn fixture() -> Model {
    let (mut m, _) = recorded();
    m.executable = Some("/opt/homebrew/bin/kamui".into());
    m.bundled = Some("/Applications/Kamui.app/Contents/Helpers/kamui".into());
    m.hosts = ["reyna", "lab", "work-box", "pi"]
        .map(String::from)
        .to_vec();
    m.launches_at_login = true;
    m.loaded = true;
    let mut reyna = session("reyna", SessionState::Connected, &[3000, 5173, 8080]);
    reyna.mirror.excluded_ports = vec![22];
    reyna.mirror.conflicts = vec![Conflict {
        port: 5432,
        error: Some("address already in use".into()),
    }];
    let mut lab = session("lab", SessionState::AuthenticationRequired, &[]);
    lab.last_error =
        Some("SSH authentication failed for lab: Permission denied (publickey).".into());
    m.sessions = vec![reyna, lab];
    m.page = Page::Session("reyna".into());
    m.version = "1.4.0".into();
    m
}

fn prompt(m: &mut Model, destination: &str, kind: PromptKind, text: &str) {
    m.prompt_arrived(
        destination,
        SshPrompt {
            id: 1,
            kind,
            text: text.into(),
        },
        PromptResponder::detached(1),
    );
}

fn tour(m: &mut Model, step: Step) {
    m.tour = Some(Tour::at(step));
}

/// A first run: no sessions yet.
fn fresh(m: &mut Model) {
    m.sessions.clear();
    m.page = Page::Connect;
}

/// A named state for a snapshot.
pub type Scene = (&'static str, fn(&mut Model));

pub static SCENES: &[Scene] = &[
    ("welcome", |m| {
        fresh(m);
        tour(m, Step::Welcome);
    }),
    ("how", |m| {
        fresh(m);
        tour(m, Step::HowItWorks);
    }),
    ("setup", |m| {
        fresh(m);
        tour(m, Step::Setup);
    }),
    ("setup-loading", |m| {
        fresh(m);
        m.loaded = false;
        tour(m, Step::Setup);
    }),
    ("setup-missing", |m| {
        fresh(m);
        m.executable = None;
        m.hosts.clear();
        tour(m, Step::Setup);
    }),
    ("connect", |m| {
        fresh(m);
        tour(m, Step::Connect);
        if let Some(t) = &mut m.tour {
            t.choice = Some("reyna".into());
        }
    }),
    ("connect-prompt", |m| {
        fresh(m);
        tour(m, Step::Connect);
        if let Some(t) = &mut m.tour {
            t.choice = Some("reyna".into());
            t.host = Some("reyna".into());
        }
        m.pending.insert("reyna".into());
        prompt(
            m,
            "reyna",
            PromptKind::Secret,
            "Enter passphrase for key '/Users/you/.ssh/id_ed25519': ",
        );
        m.answer = TextEdit::new("hunter2");
    }),
    ("connect-failed", |m| {
        fresh(m);
        tour(m, Step::Connect);
        if let Some(t) = &mut m.tour {
            t.choice = Some("lab".into());
            t.host = Some("lab".into());
        }
        m.failures.insert(
            "lab".into(),
            "SSH authentication or connection failed for lab: Permission denied (publickey)."
                .into(),
        );
    }),
    ("connected", |m| {
        fresh(m);
        m.sessions = vec![session("reyna", SessionState::Connected, &[])];
        tour(m, Step::Connect);
        if let Some(t) = &mut m.tour {
            t.choice = Some("reyna".into());
            t.host = Some("reyna".into());
            t.advance_at = Some(f64::MAX);
        }
    }),
    ("ports-empty", |m| {
        fresh(m);
        m.sessions = vec![session("reyna", SessionState::Connected, &[])];
        tour(m, Step::Ports);
        if let Some(t) = &mut m.tour {
            t.host = Some("reyna".into());
        }
    }),
    ("ports", |m| {
        fresh(m);
        let mut reyna = session("reyna", SessionState::Connected, &[3000, 5173]);
        reyna.mirror.conflicts = vec![Conflict {
            port: 5432,
            error: None,
        }];
        m.sessions = vec![reyna];
        tour(m, Step::Ports);
        if let Some(t) = &mut m.tour {
            t.host = Some("reyna".into());
        }
    }),
    ("ports-many", |m| {
        fresh(m);
        let ports: Vec<u16> = (0..35).map(|i| 3000 + i * 97).collect();
        m.sessions = vec![session("reyna", SessionState::Connected, &ports)];
        tour(m, Step::Ports);
        if let Some(t) = &mut m.tour {
            t.host = Some("reyna".into());
        }
    }),
    ("connect-connected", |m| {
        fresh(m);
        m.sessions = vec![session("reyna", SessionState::Connected, &[3000])];
        tour(m, Step::Connect);
        if let Some(t) = &mut m.tour {
            t.choice = Some("reyna".into());
        }
    }),
    ("done", |m| tour(m, Step::Done)),
    ("main", |_| {}),
    ("attention", |m| m.page = Page::Session("lab".into())),
    ("connecting", |m| {
        m.pending.insert("pi".into());
        m.notices.insert(
            "pi".into(),
            "Confirm user presence for key ED25519-SK".into(),
        );
        m.page = Page::Session("pi".into());
    }),
    ("connect-page", |m| m.page = Page::Connect),
    ("settings", |m| m.page = Page::Settings),
    ("empty", |m| {
        fresh(m);
        m.problem = Some("The kamui controller running now is from another build (1.3.2). Connecting replaces it, which ends its other sessions.".into());
    }),
    ("prompt", |m| {
        m.pending.insert("pi".into());
        m.page = Page::Session("pi".into());
        prompt(m, "pi", PromptKind::Secret, "pi@192.168.1.50's password: ");
    }),
    ("host-key", |m| {
        m.pending.insert("work-box".into());
        m.page = Page::Session("work-box".into());
        prompt(
            m,
            "work-box",
            PromptKind::Secret,
            "The authenticity of host 'work-box (10.0.0.8)' can't be established.\nED25519 key fingerprint is SHA256:Qy3m6Zl0xYb2s1y8KJbCq3m9sVwqL0Zr1Yb2Nn4Tq8E.\nAre you sure you want to continue connecting (yes/no/[fingerprint])? ",
        );
    }),
    ("confirm", |m| {
        m.pending.insert("pi".into());
        m.page = Page::Session("pi".into());
        prompt(
            m,
            "pi",
            PromptKind::Confirm,
            "Allow use of key /Users/you/.ssh/id_ecdsa_sk?\nKey fingerprint SHA256:2bT5x0Y9.",
        );
    }),
    ("sign-in", |m| {
        let url = url::Url::parse("https://headscale.example.com/a/1f2e3d").unwrap();
        m.prompt_serial += 1;
        m.sign_ins.push(super::model::SignIn {
            serial: m.prompt_serial,
            destination: "lab".into(),
            url,
        });
    }),
    ("sign-in-long", |m| {
        // Long userinfo, a hostname that starts like Tailscale's, and a long
        // path: the real domain must stay readable.
        let label = "x".repeat(40);
        let url = format!(
            "https://{0}@login.tailscale.com.{1}.{1}.{3}.evil.example/a/1f2e3d?next={2}",
            "u".repeat(60),
            label,
            "y".repeat(80),
            // The longest label DNS allows, in the widest letter.
            "w".repeat(63)
        );
        m.prompt_serial += 1;
        m.sign_ins.push(super::model::SignIn {
            serial: m.prompt_serial,
            destination: "lab".into(),
            url: url::Url::parse(&url).unwrap(),
        });
    }),
    ("prompt-long", |m| {
        m.pending.insert("work-box".into());
        m.page = Page::Session("work-box".into());
        let text = (1..=30)
            .map(|i| format!("Line {i} of a long keyboard-interactive banner from the server."))
            .collect::<Vec<_>>()
            .join("\n");
        prompt(
            m,
            "work-box",
            PromptKind::Secret,
            &format!("{text}\nVerification code: "),
        );
    }),
    ("note", |m| {
        m.notes.push(super::model::Note {
            title: "Could not connect to pi".into(),
            detail: "SSH authentication or connection failed for pi: ssh: connect to host 192.168.1.50 port 22: Operation timed out".into(),
        });
    }),
];
