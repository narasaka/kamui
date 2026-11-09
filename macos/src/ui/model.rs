//! The app's state and every change to it. Views read it; the macOS host
//! and the window's handlers change it on the UI thread. Work that blocks
//! (running kamui) goes through [`Backend`], which reports back with
//! `AppHandle::update`, so the model itself never waits and tests drive it
//! with a fake backend.

use std::collections::{BTreeMap, BTreeSet};
use std::path::PathBuf;
use std::rc::Rc;
use std::time::Duration;

use rune::{TextEdit, Tx};
use url::Url;

use super::onboarding::Tour;
use super::toast::{Toast, Tone};
use crate::core::prompt::{PromptKind, PromptReply, PromptResponder, SshPrompt};
use crate::core::{KamuiError, Session, SessionState, client, destination};

/// Blocking and native work the model asks for. Each call returns at once;
/// results come back as model updates.
pub trait Backend {
    /// Reads status, hosts, the executable, and the login item again now.
    fn refresh(&self);
    fn connect(&self, destination: String);
    /// Stops one session. `id` comes back with the result in
    /// `Model::stop_finished`.
    fn stop(&self, destination: String, id: u64);
    /// Stops connecting, then declines the prompt. Stopping first ends the
    /// attempt, so OpenSSH never tries an empty answer and asks again.
    fn stop_then_decline(&self, destination: String, responder: PromptResponder, id: u64);
    fn stop_all(&self, id: u64);
    /// Registers or unregisters the login item and returns whether it is on.
    fn set_launch_at_login(&self, enabled: bool) -> Result<bool, String>;
    fn save_executable_override(&self, path: Option<String>);
    fn save_onboarded(&self, done: bool);
    /// Brings the window forward, for a prompt or a message.
    fn show_window(&self);
    /// Whether the window is open and in front, where a message can show
    /// without taking over the screen.
    fn window_in_front(&self) -> bool {
        false
    }
    /// Shows a toast under the menu bar. Returns false when it can't, so the
    /// window shows the message instead.
    fn toast(&self, _toast: Toast) -> bool {
        false
    }
    /// The window closed; the app stays in the menu bar.
    fn window_closed(&self) {}
    fn quit(&self);
}

/// What the main window shows beside its sidebar.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Page {
    Session(String),
    Connect,
    Settings,
}

/// An OpenSSH question waiting for the user.
#[derive(Debug, Clone)]
pub struct PendingPrompt {
    /// Unique for the app's life, so a press that began on one prompt can't
    /// answer another that took its place.
    pub serial: u64,
    pub destination: String,
    pub prompt: SshPrompt,
    pub responder: PromptResponder,
}

/// A session page a clicked toast opened before a refresh reported the
/// session.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Opening {
    pub destination: String,
    /// Refreshes the page waits through for its session. One that started
    /// before the host connected doesn't have it.
    pub refreshes: u8,
}

/// How many refreshes a clicked toast's page waits for its session.
const OPENING_REFRESHES: u8 = 3;

/// A message the window shows until it is dismissed.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Note {
    pub title: String,
    pub detail: String,
}

/// A sign-in link from a server other than Tailscale's, which the user
/// confirms before it opens.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SignIn {
    /// Unique for the app's life, like a prompt's serial.
    pub serial: u64,
    pub destination: String,
    pub url: Url,
}

/// Everything one status refresh found.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Snapshot {
    pub sessions: Option<Vec<Session>>,
    pub problem: Option<String>,
    pub executable: Option<PathBuf>,
    pub hosts: Vec<String>,
    pub launches_at_login: bool,
}

pub struct Model {
    pub backend: Rc<dyn Backend>,
    pub sessions: Vec<Session>,
    /// Connections this app started that have not finished.
    pub pending: BTreeSet<String>,
    /// Connections the user stopped while they were starting, whose failure
    /// is expected rather than reported.
    pub stopping: BTreeSet<String>,
    /// Stops that haven't reported back, by id, with their destination
    /// (empty for Stop All). A destination with one in flight is busy, so a
    /// new connection can't start and be ended by it.
    pub stops: BTreeMap<u64, String>,
    pub stop_serial: u64,
    /// Connections that finished after the user stopped them, with the id
    /// of the stop that ends them now. They stay pending until it reports.
    pub cleanup: BTreeMap<String, u64>,
    /// Stops sent while their destination's connection was on its way or
    /// being cleaned up. Their results say nothing about the session the
    /// connection makes: its cleanup stop, or its failure, decides.
    pub quiet_stops: BTreeSet<u64>,
    /// OpenSSH notices, such as "Confirm user presence for key", shown while
    /// a connection waits.
    pub notices: BTreeMap<String, String>,
    /// The last failure for each destination this app tried, until it
    /// connects or is tried again.
    pub failures: BTreeMap<String, String>,
    pub prompts: Vec<PendingPrompt>,
    pub prompt_serial: u64,
    pub hosts: Vec<String>,
    pub problem: Option<String>,
    pub executable: Option<PathBuf>,
    pub bundled: Option<PathBuf>,
    pub executable_override: Option<String>,
    pub launches_at_login: bool,
    /// The first refresh has answered.
    pub loaded: bool,
    /// Whether this launch has decided if it needs first-run setup.
    pub first_run_decided: bool,
    pub version: String,

    pub page: Page,
    pub destination: TextEdit,
    /// The front prompt's typed answer. Cleared whenever that prompt ends.
    pub answer: TextEdit,
    pub notes: Vec<Note>,
    /// A session page shown for a clicked toast before any refresh has
    /// reported the session, which a host that just connected isn't until
    /// then. Leaving the page or closing the window ends it.
    pub opening: Option<Opening>,
    /// Sign-in links waiting for a yes, in the order they arrived.
    pub sign_ins: Vec<SignIn>,
    pub tour: Option<Tour>,
    /// Seconds on the window's clock at the last frame.
    pub clock: f64,
}

impl Model {
    pub fn new(backend: Rc<dyn Backend>) -> Self {
        Self {
            backend,
            sessions: Vec::new(),
            pending: BTreeSet::new(),
            stopping: BTreeSet::new(),
            stops: BTreeMap::new(),
            stop_serial: 0,
            cleanup: BTreeMap::new(),
            quiet_stops: BTreeSet::new(),
            notices: BTreeMap::new(),
            failures: BTreeMap::new(),
            prompts: Vec::new(),
            prompt_serial: 0,
            hosts: Vec::new(),
            problem: None,
            executable: None,
            bundled: None,
            executable_override: None,
            launches_at_login: false,
            loaded: false,
            first_run_decided: false,
            version: env!("CARGO_PKG_VERSION").into(),
            page: Page::Connect,
            destination: TextEdit::new(""),
            answer: TextEdit::new(""),
            notes: Vec::new(),
            opening: None,
            sign_ins: Vec::new(),
            tour: None,
            clock: 0.0,
        }
    }

    pub fn session(&self, destination: &str) -> Option<&Session> {
        self.sessions.iter().find(|s| s.destination == destination)
    }

    pub fn uses_bundled_executable(&self) -> bool {
        self.executable.is_some() && self.executable == self.bundled
    }

    /// Destinations the sidebar and menu list: sessions, then connections
    /// that have not reached the controller yet.
    pub fn destinations(&self) -> Vec<String> {
        let mut all: Vec<String> = self
            .sessions
            .iter()
            .map(|s| s.destination.clone())
            .collect();
        all.extend(
            self.pending
                .iter()
                .filter(|d| self.session(d).is_none())
                .cloned(),
        );
        all
    }

    /// Whether connecting to `destination` now would do nothing.
    pub fn is_busy(&self, destination: &str) -> bool {
        self.pending.contains(destination)
            || self.stopping_now(destination)
            || self
                .session(destination)
                .is_some_and(|s| s.state == SessionState::Connected)
    }

    /// Whether a stop that could end `destination` hasn't reported back.
    pub fn stopping_now(&self, destination: &str) -> bool {
        self.stops
            .values()
            .any(|d| d == destination || d.is_empty())
    }

    /// Whether `destination`'s connection is still on its way to the
    /// controller, so a stop now has to be repeated when it arrives.
    fn in_flight(&self, destination: &str) -> bool {
        self.pending.contains(destination) && !self.cleanup.contains_key(destination)
    }

    fn next_stop(&mut self, destination: &str) -> u64 {
        self.stop_serial += 1;
        if !destination.is_empty() && self.pending.contains(destination) {
            self.quiet_stops.insert(self.stop_serial);
        }
        self.stops.insert(self.stop_serial, destination.to_owned());
        self.stop_serial
    }

    // Status.

    /// Applies a refresh and reports whether anything the window shows
    /// changed, so polling does not redraw an idle window.
    pub fn apply_if_changed(&mut self, snapshot: Snapshot) -> bool {
        let before = self.snapshot();
        let page = self.page.clone();
        self.apply(snapshot);
        before != self.snapshot() || page != self.page
    }

    fn snapshot(&self) -> Snapshot {
        Snapshot {
            sessions: Some(self.sessions.clone()),
            problem: self.problem.clone(),
            executable: self.executable.clone(),
            hosts: self.hosts.clone(),
            launches_at_login: self.launches_at_login,
        }
    }

    pub fn apply(&mut self, snapshot: Snapshot) {
        let answered = snapshot.sessions.is_some();
        if let Some(sessions) = snapshot.sessions {
            self.sessions = sessions;
        }
        self.problem = snapshot.problem;
        self.executable = snapshot.executable;
        self.hosts = snapshot.hosts;
        self.launches_at_login = snapshot.launches_at_login;
        if !self.loaded && self.page == Page::Connect {
            // The window opens on the first session once one is known.
            self.page = self.default_page();
        }
        self.loaded = true;
        if answered && let Some(mut opening) = self.opening.take() {
            let waiting = self.page == Page::Session(opening.destination.clone())
                && self.session(&opening.destination).is_none();
            if waiting && opening.refreshes > 0 {
                opening.refreshes -= 1;
                self.opening = Some(opening);
            }
        }
        // A session that disappeared leaves its page.
        if let Page::Session(destination) = &self.page
            && self.session(destination).is_none()
            && !self.pending.contains(destination)
            && self
                .opening
                .as_ref()
                .is_none_or(|o| o.destination != *destination)
        {
            self.page = self.default_page();
        }
    }

    /// Decides once per launch whether to start first-run setup, and only
    /// from a refresh whose status call answered: a status that failed, for
    /// example because a controller from another build is running, says
    /// nothing about whether sessions exist, and setup's Connect would replace
    /// that controller. Returns true when sessions already run, so the host
    /// remembers setup as done.
    pub fn decide_first_run(&mut self, onboarded: bool, status_answered: bool) -> bool {
        if self.first_run_decided || self.tour.is_some() || onboarded {
            self.first_run_decided = true;
            return false;
        }
        if !status_answered {
            return false;
        }
        self.first_run_decided = true;
        if self.sessions.is_empty() {
            self.tour = Some(Tour::new());
            self.backend.show_window();
            false
        } else {
            true
        }
    }

    fn default_page(&self) -> Page {
        self.destinations()
            .into_iter()
            .next()
            .map_or(Page::Connect, Page::Session)
    }

    // Connecting and stopping.

    /// Starts connecting. Returns the validated destination, or shows why it
    /// is not one.
    pub fn connect(&mut self, raw: &str) -> Option<String> {
        if self.executable.is_none() {
            self.note("Kamui", &KamuiError::ExecutableNotFound.to_string());
            return None;
        }
        let destination = match destination::validate(raw) {
            Ok(destination) => destination,
            Err(error) => {
                self.failures
                    .insert(raw.trim().to_owned(), error.to_string());
                if self.tour.is_none() {
                    self.note("Kamui", &error.to_string());
                }
                return None;
            }
        };
        if self.stopping_now(&destination) {
            // A stop still on its way would end the new session.
            return None;
        }
        if !self.pending.insert(destination.clone()) {
            return Some(destination);
        }
        // Nothing from an earlier attempt applies to this one.
        self.stopping.remove(&destination);
        self.notices.remove(&destination);
        self.failures.remove(&destination);
        self.backend.connect(destination.clone());
        Some(destination)
    }

    /// Connects to what the Connect page's field holds.
    pub fn connect_typed(&mut self) {
        let typed = self.destination.text.to_owned();
        if let Some(destination) = self.connect(&typed) {
            self.destination = TextEdit::new("");
            if self.tour.is_none() {
                self.page = Page::Session(destination);
            }
        }
    }

    pub fn connect_finished(
        &mut self,
        destination: &str,
        result: Result<Vec<String>, KamuiError>,
        tx: &mut Tx,
    ) {
        self.notices.remove(destination);
        self.withdraw_all(destination);
        self.sign_ins.retain(|s| s.destination != destination);
        let stopped = self.stopping.remove(destination);
        if stopped {
            // Stop was requested before this connection finished, so it may
            // have had nothing to stop then. Stop again, whatever the
            // connection reported: a failed reconnect keeps the session it
            // was reconnecting, and a stop of a session that is gone counts
            // as success. The destination stays pending until this stop ends.
            let id = self.next_stop(destination);
            // This stop's result is the verdict on the session.
            self.quiet_stops.remove(&id);
            self.cleanup.insert(destination.to_owned(), id);
            self.backend.stop(destination.to_owned(), id);
            return;
        }
        self.pending.remove(destination);
        match result {
            Ok(warnings) => {
                self.failures.remove(destination);
                let title = format!("Connected to {destination}");
                if !warnings.is_empty() {
                    self.tell(
                        &title,
                        &warnings.join("\n"),
                        Some(destination),
                        Tone::Warning,
                    );
                } else if !self.in_front() {
                    self.backend.toast(Toast {
                        title,
                        detail: "Its ports appear in the menu as services start.".into(),
                        destination: Some(destination.to_owned()),
                        tone: Tone::Connected,
                    });
                }
                if let Some(tour) = &mut self.tour {
                    tour.connected(destination, self.clock, tx);
                }
            }
            Err(error) => {
                self.failures
                    .insert(destination.to_owned(), error.to_string());
                let in_tour = self
                    .tour
                    .as_ref()
                    .is_some_and(|t| t.host.as_deref() == Some(destination));
                if !in_tour {
                    self.tell(
                        &format!("Could not connect to {destination}"),
                        &error.to_string(),
                        Some(destination),
                        Tone::Failed,
                    );
                }
            }
        }
        self.backend.refresh();
    }

    pub fn stop(&mut self, destination: &str) {
        // A page waiting for this session to appear has nothing to wait for.
        if self
            .opening
            .as_ref()
            .is_some_and(|o| o.destination == destination)
        {
            self.opening = None;
        }
        if self.in_flight(destination) {
            self.stopping.insert(destination.to_owned());
        }
        let id = self.next_stop(destination);
        self.backend.stop(destination.to_owned(), id);
    }

    /// A stop reported. kamui already counts a session that was gone as
    /// stopped (`KamuiClient::stop`), so an error here means a session may
    /// still be running, except from a stop sent while the connection was
    /// on its way, whose cleanup stop reports instead.
    pub fn stop_finished(&mut self, id: u64, result: Result<(), KamuiError>) {
        let Some(destination) = self.stops.remove(&id) else {
            return;
        };
        let quiet = self.quiet_stops.remove(&id);
        if self.cleanup.get(&destination) == Some(&id) {
            self.cleanup.remove(&destination);
            self.pending.remove(&destination);
            self.stopping.remove(&destination);
        }
        if let Err(error) = result
            && !quiet
        {
            self.note("Kamui", &error.to_string());
        }
        self.backend.refresh();
    }

    pub fn stop_all(&mut self) {
        let in_flight: Vec<String> = self
            .pending
            .iter()
            .filter(|d| self.in_flight(d))
            .cloned()
            .collect();
        self.stopping.extend(in_flight);
        self.opening = None;
        let id = self.next_stop("");
        self.backend.stop_all(id);
    }

    // Prompts.

    pub fn prompt_arrived(
        &mut self,
        destination: &str,
        prompt: SshPrompt,
        responder: PromptResponder,
    ) {
        if self.prompts.is_empty() {
            self.answer = TextEdit::new("");
        }
        self.prompt_serial += 1;
        self.prompts.push(PendingPrompt {
            serial: self.prompt_serial,
            destination: destination.to_owned(),
            prompt,
            responder,
        });
        self.backend.show_window();
    }

    pub fn prompt_withdrawn(&mut self, destination: &str, id: i64) {
        let front = self
            .prompts
            .first()
            .map(|p| (p.destination.clone(), p.prompt.id));
        self.prompts
            .retain(|p| !(p.destination == destination && p.prompt.id == id));
        if front == Some((destination.to_owned(), id)) {
            self.answer = TextEdit::new("");
        }
    }

    fn withdraw_all(&mut self, destination: &str) {
        let ids: Vec<i64> = self
            .prompts
            .iter()
            .filter(|p| p.destination == destination)
            .map(|p| p.prompt.id)
            .collect();
        for id in ids {
            self.prompt_withdrawn(destination, id);
        }
    }

    /// The prompt the window asks now.
    pub fn front_prompt(&self) -> Option<&PendingPrompt> {
        self.prompts.first()
    }

    /// Sends the typed answer, or "yes" to a confirmation, to prompt
    /// `serial` if it is still the one the window asks.
    pub fn answer_prompt(&mut self, serial: u64) {
        if self.front_prompt().map(|p| p.serial) != Some(serial) {
            return;
        }
        let pending = self.prompts.remove(0);
        let answer = match pending.prompt.kind {
            PromptKind::Confirm => "yes".to_owned(),
            _ => self.answer.text.to_owned(),
        };
        self.answer = TextEdit::new("");
        pending.responder.reply(PromptReply::Answer(answer));
    }

    /// Deny for a confirmation; Cancel for a question, which stops
    /// connecting to that host.
    pub fn decline_prompt(&mut self, serial: u64) {
        if self.front_prompt().map(|p| p.serial) != Some(serial) {
            return;
        }
        let pending = self.prompts.remove(0);
        self.answer = TextEdit::new("");
        if pending.prompt.kind == PromptKind::Confirm {
            pending.responder.reply(PromptReply::Decline);
            return;
        }
        if self.in_flight(&pending.destination) {
            self.stopping.insert(pending.destination.clone());
        }
        self.withdraw_all(&pending.destination);
        let id = self.next_stop(&pending.destination);
        self.backend
            .stop_then_decline(pending.destination, pending.responder, id);
    }

    pub fn notice(&mut self, destination: &str, text: &str) {
        self.notices.insert(destination.to_owned(), text.to_owned());
    }

    // Sign-in links.

    /// A Tailscale SSH check waits until the user signs in. Tailscale's own
    /// login opens at once; any other server's link waits for a yes.
    pub fn sign_in_requested(&mut self, destination: &str, url: Url, tx: &mut Tx) {
        if client::is_tailscale_login(&url) {
            let _ = tx.open_url(url.as_str());
        } else {
            self.prompt_serial += 1;
            self.sign_ins.push(SignIn {
                serial: self.prompt_serial,
                destination: destination.to_owned(),
                url,
            });
            self.backend.show_window();
        }
    }

    /// Answers the front sign-in link, if it is still request `serial`.
    pub fn answer_sign_in(&mut self, serial: u64, open: bool, tx: &mut Tx) {
        if self.sign_ins.first().is_some_and(|s| s.serial == serial) {
            let sign_in = self.sign_ins.remove(0);
            if open {
                let _ = tx.open_url(sign_in.url.as_str());
            }
        }
    }

    // Messages.

    /// Reports an error: in the window when it is in front, otherwise in a
    /// toast.
    pub fn note(&mut self, title: &str, detail: &str) {
        self.tell(title, detail, None, Tone::Failed);
    }

    /// Shows a message without the window taking over the screen: the
    /// window shows it when it is in front or setup runs, and a toast under
    /// the menu bar does otherwise. Without a toast, the window comes
    /// forward.
    fn tell(&mut self, title: &str, detail: &str, destination: Option<&str>, tone: Tone) {
        let toast = Toast {
            title: title.to_owned(),
            detail: detail.to_owned(),
            destination: destination.map(str::to_owned),
            tone,
        };
        if !self.in_front() && self.backend.toast(toast) {
            return;
        }
        self.push_note(title, detail);
        self.backend.show_window();
    }

    fn push_note(&mut self, title: &str, detail: &str) {
        let note = Note {
            title: title.to_owned(),
            detail: detail.to_owned(),
        };
        if !self.notes.contains(&note) {
            self.notes.push(note);
        }
    }

    /// The window closed; the app stays in the menu bar.
    pub fn window_closed(&mut self) {
        self.opening = None;
        self.backend.window_closed();
    }

    fn in_front(&self) -> bool {
        self.tour.is_some() || self.backend.window_in_front()
    }

    /// A toast was clicked, with the toasts that were waiting behind it.
    /// The window opens on the clicked toast's session and shows each
    /// warning or error in full, which a toast may have cut short. A host
    /// that failed to connect has no session page, and one that just
    /// connected may not have one until the next refresh.
    pub fn toast_clicked(&mut self, toasts: Vec<Toast>) {
        self.opening = None;
        if let Some(destination) = toasts.first().and_then(|t| t.destination.clone())
            && self.tour.is_none()
        {
            if self.destinations().contains(&destination) {
                self.page = Page::Session(destination);
            } else if toasts[0].tone != Tone::Failed {
                // Connected, but no refresh has reported the session yet.
                self.page = Page::Session(destination.clone());
                self.opening = Some(Opening {
                    destination,
                    refreshes: OPENING_REFRESHES,
                });
            }
        }
        for toast in toasts.iter().filter(|t| t.tone != Tone::Connected) {
            self.push_note(&toast.title, &toast.detail);
        }
        self.backend.show_window();
    }

    pub fn dismiss_note(&mut self) {
        if !self.notes.is_empty() {
            self.notes.remove(0);
        }
    }

    // Settings.

    pub fn set_launch_at_login(&mut self, enabled: bool) {
        match self.backend.set_launch_at_login(enabled) {
            Ok(on) => self.launches_at_login = on,
            Err(error) => self.note("Kamui", &error),
        }
    }

    pub fn set_executable_override(&mut self, path: Option<String>) {
        self.executable_override = path.clone();
        self.backend.save_executable_override(path);
        self.backend.refresh();
    }

    pub fn open_port(port: u16, tx: &mut Tx) {
        let _ = tx.open_url(&format!("http://localhost:{port}"));
    }

    /// Called at the start of every frame.
    pub fn poll(&mut self, now: f64, tx: &mut Tx) -> bool {
        self.clock = now;
        match &mut self.tour {
            Some(tour) => tour.poll(now, &*self.backend, tx),
            None => false,
        }
    }
}

/// How often the host polls status. The tour's port step polls faster.
pub const POLL_INTERVAL: Duration = Duration::from_secs(3);

/// The first line of a message, cut to fit a menu item.
pub fn truncated(message: &str, limit: usize) -> String {
    let line = message.lines().next().unwrap_or(message);
    if line.chars().count() > limit {
        let cut: String = line.chars().take(limit - 1).collect();
        format!("{cut}…")
    } else {
        line.to_owned()
    }
}
