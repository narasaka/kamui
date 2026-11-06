//! First-run setup: a short tour over the whole window that explains Kamui,
//! checks the CLI and hosts it found, connects to a real host (answering SSH
//! prompts inline), shows its ports arriving, and ends where Kamui lives, in
//! the menu bar.
//!
//! Each step checks what it can by itself. When the connection succeeds the
//! tour holds the check for a beat ([`ADVANCE_BEAT`]) and moves on, so the
//! person sees it land. Completion is saved, so the tour runs once;
//! Settings › Run setup again replays it.

use std::time::Duration;

use rune::{Align, El, TextEdit, Tx, col, el, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Switch, TextInput, glyphs};

use super::mark::vortex;
use super::model::{Backend, Model, Page};
use super::prompt::inline_prompt;
use super::theme::{self, DESCRIPTION, LEAD, Paint, TITLE, arrives, staggered};

/// How long the "Connected" check shows before the tour moves on.
pub const ADVANCE_BEAT: f64 = 0.7;
/// How often the ports step asks for fresh status.
const PORTS_REFRESH: f64 = 1.0;
const TOP_BAR: f32 = 48.0;
const COLUMN: f32 = 520.0;
/// Ports the tour's list shows before it scrolls, and one row's height.
const VISIBLE_PORTS: usize = 5;
const PORT_ROW: f32 = 44.0;

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub enum Step {
    Welcome,
    HowItWorks,
    Setup,
    Connect,
    Ports,
    Done,
}

impl Step {
    pub const ALL: [Step; 6] = [
        Step::Welcome,
        Step::HowItWorks,
        Step::Setup,
        Step::Connect,
        Step::Ports,
        Step::Done,
    ];

    fn index(self) -> usize {
        Self::ALL.iter().position(|s| *s == self).unwrap_or(0)
    }

    fn next(self) -> Step {
        Self::ALL
            .get(self.index() + 1)
            .copied()
            .unwrap_or(Step::Done)
    }

    fn previous(self) -> Step {
        Self::ALL[self.index().saturating_sub(1)]
    }

    fn key(self) -> &'static str {
        match self {
            Step::Welcome => "welcome",
            Step::HowItWorks => "how-it-works",
            Step::Setup => "setup",
            Step::Connect => "connect",
            Step::Ports => "ports",
            Step::Done => "done",
        }
    }
}

#[derive(Debug, Clone)]
pub struct Tour {
    pub step: Step,
    /// The host the person picked on the connect step.
    pub choice: Option<String>,
    /// The destination the tour connected, or is connecting, to.
    pub host: Option<String>,
    /// Thirds of a turn the vortex has made: one per step forward.
    pub turns: u32,
    pub advance_at: Option<f64>,
    last_refresh: f64,
}

impl Tour {
    pub fn new() -> Self {
        Self {
            step: Step::Welcome,
            choice: None,
            host: None,
            turns: 0,
            advance_at: None,
            last_refresh: 0.0,
        }
    }

    pub fn at(step: Step) -> Self {
        Self {
            step,
            turns: step.index() as u32,
            ..Self::new()
        }
    }

    fn go(&mut self, step: Step) {
        if step > self.step {
            self.turns += 1;
        }
        self.step = step;
        self.advance_at = None;
    }

    /// The tour's connection reached the controller.
    pub fn connected(&mut self, destination: &str, now: f64, tx: &mut Tx) {
        if self.step == Step::Connect && self.host.as_deref() == Some(destination) {
            self.advance_at = Some(now + ADVANCE_BEAT);
            tx.wake_after(Duration::from_secs_f64(ADVANCE_BEAT));
        }
    }

    pub fn poll(&mut self, now: f64, backend: &dyn Backend, tx: &mut Tx) -> bool {
        if let Some(at) = self.advance_at {
            if now >= at {
                self.go(Step::Ports);
                self.last_refresh = now;
                backend.refresh();
                tx.wake_after(Duration::from_secs_f64(PORTS_REFRESH));
                return true;
            }
            tx.wake_after(Duration::from_secs_f64(at - now));
        }
        if self.step == Step::Ports {
            if now - self.last_refresh >= PORTS_REFRESH {
                self.last_refresh = now;
                backend.refresh();
            }
            tx.wake_after(Duration::from_secs_f64(PORTS_REFRESH));
        }
        false
    }
}

impl Default for Tour {
    fn default() -> Self {
        Self::new()
    }
}

// Moves, as handlers.

pub fn next(m: &mut Model) {
    if let Some(tour) = &mut m.tour {
        let step = tour.step.next();
        tour.go(step);
    }
}

pub fn back(m: &mut Model) {
    if let Some(tour) = &mut m.tour {
        let step = tour.step.previous();
        tour.go(step);
    }
}

/// Ends the tour and remembers it ran. The window stays open on the main
/// view unless `close`.
pub fn finish(m: &mut Model, close: bool, tx: &mut Tx) {
    let host = m.tour.take().and_then(|t| t.host);
    m.backend.save_onboarded(true);
    m.page = match host {
        Some(host) if m.session(&host).is_some() => Page::Session(host),
        _ => m
            .destinations()
            .into_iter()
            .next()
            .map_or(Page::Connect, Page::Session),
    };
    if close {
        tx.close_window();
    }
}

/// Settings › Run setup again.
pub fn restart(m: &mut Model) {
    m.tour = Some(Tour::new());
    m.backend.save_onboarded(false);
    m.backend.refresh();
}

fn pick(m: &mut Model, host: &str) {
    if let Some(tour) = &mut m.tour {
        tour.choice = Some(host.to_owned());
    }
    m.destination = TextEdit::new("");
}

/// Connects to the picked host, or to what the field holds.
fn connect(m: &mut Model) {
    let typed = m.destination.text.trim().to_owned();
    let Some(choice) = (if typed.is_empty() {
        m.tour.as_ref().and_then(|t| t.choice.clone())
    } else {
        Some(typed)
    }) else {
        return;
    };
    let choice = choice.trim().to_owned();
    let connected = m
        .session(&choice)
        .is_some_and(|s| s.state == crate::core::SessionState::Connected);
    if let Some(tour) = &mut m.tour {
        tour.host = Some(choice.clone());
        if connected {
            // Already mirroring, from earlier or from the terminal: nothing
            // to connect, so go straight to its ports.
            tour.go(Step::Ports);
            m.failures.remove(&choice);
            m.backend.refresh();
            return;
        }
    }
    m.connect(&choice);
}

// The view.

pub fn view(p: Paint, m: &Model, tour: &Tour) -> El {
    let page = match tour.step {
        Step::Welcome => welcome(p, tour),
        Step::HowItWorks => how_it_works(p),
        Step::Setup => setup(p, m),
        Step::Connect => connect_page(p, m, tour),
        Step::Ports => ports(p, m, tour),
        Step::Done => done(p, tour),
    };
    col()
        .key("onboarding")
        .role("dialog")
        .label("Set up Kamui")
        .w_full()
        .h_full()
        .fill(p.k.c.bg_deep)
        .child(top_bar(p, tour))
        // kamui itself failing, such as a controller from another build,
        // shows here too, before setup's Connect could replace it.
        .children(
            m.problem
                .as_deref()
                .map(|problem| super::window::problem_banner(p, problem).mx(80.0)),
        )
        .child(
            // Each page fills the stage on its own, so the page leaving and
            // the page arriving cross in the same place.
            el().key("stage")
                .grow(1.0)
                .shrink()
                .min_h(0.0)
                .w_full()
                .child(
                    col()
                        .key(format!("page-{}", tour.step.key()))
                        .absolute(0.0, 0.0)
                        .w_full()
                        .h_full()
                        .center()
                        .pb(TOP_BAR)
                        .child(page)
                        .transition(theme::roll(10.0))
                        .enter_delay(0.1),
                ),
        )
}

/// Back, the step dots, and Skip. The window has no title bar, so this band
/// moves it.
fn top_bar(p: Paint, tour: &Tour) -> El {
    let dots = row()
        .key("steps")
        .gap(6.0)
        .center_y()
        .label(format!(
            "Step {} of {}",
            tour.step.index() + 1,
            Step::ALL.len()
        ))
        .children(Step::ALL.iter().map(|step| {
            let current = *step == tour.step;
            let done = *step < tour.step;
            el().key(format!("dot-{}", step.key()))
                .size(if current { 18.0 } else { 6.0 }, 6.0)
                .radius(3.0)
                .fill(if current {
                    p.text()
                } else if done {
                    p.muted()
                } else {
                    p.k.c.border_strong
                })
                .motion(theme::UI_SPRING)
                .a11y_hidden()
        }));
    let back = (tour.step > Step::Welcome && tour.step < Step::Done).then(|| {
        Button::new("Back")
            .variant(ButtonVariant::Ghost)
            .on_press(back)
            .build(p.k)
            .key("back")
    });
    let skip = (tour.step < Step::Done).then(|| {
        let quiet = p.muted();
        Button::new("Skip setup")
            .variant(ButtonVariant::Ghost)
            .label_with(move |font, _| text("Skip setup").key("label").font(font).color(quiet))
            .on_press_tx(|m: &mut Model, tx| finish(m, false, tx))
            .build(p.k)
            .key("skip")
    });
    row()
        .key("top-bar")
        .w_full()
        .h(TOP_BAR)
        .pl(84.0)
        .pr(16.0)
        .center_y()
        .window_drag()
        .child(row().key("leading").w(120.0).children(back))
        .child(row().key("centre").grow(1.0).justify_center().child(dots))
        .child(row().key("trailing").w(120.0).justify_end().children(skip))
}

fn heading(p: Paint, title: &str, lead: &str) -> El {
    col()
        .key("heading")
        .gap(8.0)
        .items_center()
        .child(
            text(title)
                .key("title")
                .role("heading")
                .level(1)
                .font(TITLE)
                .color(p.strong()),
        )
        .child(
            text(lead)
                .key("lead")
                .font(LEAD)
                .color(p.muted())
                .align(Align::Center)
                .wrap()
                .max_w(500.0),
        )
}

fn primary(label: &str) -> Button {
    Button::new(label)
        .variant(ButtonVariant::Contrast)
        .control_size(ControlSize::Large)
}

fn continue_button(p: Paint, label: &str, enabled: bool) -> El {
    primary(label)
        .disabled(!enabled)
        .on_press(next)
        .build(p.k)
        .key("continue")
        .default_button()
}

fn welcome(p: Paint, tour: &Tour) -> El {
    col()
        .gap(28.0)
        .items_center()
        .child(staggered(vortex(p, "mark", 88.0, p.strong(), tour.turns), 0.0))
        .child(staggered(
            heading(
                p,
                "Welcome to Kamui",
                "Use your remote machine's localhost from this Mac, over SSH. Setup takes about a minute.",
            ),
            1.0,
        ))
        .child(staggered(continue_button(p, "Get started", true), 2.0))
}

/// Mac ⟷ SSH ⟷ host, with the same port on both ends.
fn diagram(p: Paint) -> El {
    let machine = |key: &str, title: &str, detail: &str| {
        p.raised(key)
            .w(180.0)
            .px(14.0)
            .py(12.0)
            .gap(6.0)
            .child(
                text(title)
                    .key("title")
                    .font(p.k.ty.ui_medium)
                    .color(p.text()),
            )
            .child(
                row()
                    .key("port")
                    .gap(6.0)
                    .center_y()
                    .child(p.dot("live", p.success(), 6.0))
                    .child(
                        text(detail)
                            .key("detail")
                            .font(p.k.ty.mono)
                            .color(p.muted()),
                    ),
            )
    };
    let link = col()
        .key("link")
        .grow(1.0)
        .items_center()
        .gap(6.0)
        .child(
            text("one SSH connection")
                .key("caption")
                .font(DESCRIPTION)
                .color(p.faint()),
        )
        .child(
            el().key("wire")
                .w_full()
                .h(p.hair.max(1.0))
                .fill(p.k.c.border_strong),
        );
    row()
        .key("diagram")
        .w(COLUMN + 80.0)
        .gap(12.0)
        .center_y()
        .child(staggered(machine("mac", "This Mac", "localhost:3000"), 1.0))
        .child(staggered(link, 2.0))
        .child(staggered(
            machine("host", "Your server", "localhost:3000"),
            3.0,
        ))
}

fn how_it_works(p: Paint) -> El {
    let point = |key: &str, rank: f32, title: &str, body: &str| {
        staggered(
            col()
                .key(key)
                .w(188.0)
                .gap(6.0)
                .child(
                    text(title)
                        .key("title")
                        .font(p.k.ty.ui_medium)
                        .color(p.text()),
                )
                .child(
                    text(body)
                        .key("body")
                        .font(DESCRIPTION)
                        .color(p.muted())
                        .wrap(),
                ),
            rank,
        )
    };
    col()
        .gap(32.0)
        .items_center()
        .child(heading(p, "How Kamui works", "Services on your server's localhost show up on this Mac's localhost, on the same ports."))
        .child(diagram(p))
        .child(
            row()
                .key("points")
                .gap(20.0)
                .items_start()
                .child(point("ssh", 4.0, "Your SSH, as it is", "Kamui runs your OpenSSH with your ~/.ssh/config and keys, and checks host keys as usual."))
                .child(point("ports", 5.0, "Ports follow along", "When something starts listening on the server, it appears here. When it stops, it goes."))
                .child(point("browser", 6.0, "Only on this Mac", "Mirrored ports listen on this Mac's loopback alone, and a port already in use here is left alone.")),
        )
        .child(staggered(continue_button(p, "Continue", true), 7.0))
}

/// A checklist row's state light.
fn check(p: Paint, key: &str, ok: Option<bool>) -> El {
    match ok {
        None => p.pulse("pulse", 8.0).key(key),
        Some(true) => el()
            .key(key)
            .size(18.0, 18.0)
            .radius(9.0)
            .fill(p.k.c.success.bg)
            .center()
            .child(rune::icon(&glyphs::CHECK_SMALL, 12.0).color(p.success()))
            .transition(arrives())
            .a11y_hidden(),
        Some(false) => p.dot(key, p.warning(), 10.0).transition(arrives()),
    }
}

/// An optional item's light: a check when it is on, an empty ring when off.
fn optional(p: Paint, key: &str, on: bool) -> El {
    if on {
        return check(p, key, Some(true));
    }
    el().key(key)
        .size(16.0, 16.0)
        .radius(8.0)
        .border(1.0, p.k.c.border_strong)
        .transition(arrives())
        .a11y_hidden()
}

fn checklist_row(
    p: Paint,
    key: &str,
    light: El,
    title: &str,
    detail: &str,
    control: Option<El>,
) -> El {
    row()
        .key(key)
        .w_full()
        .min_h(60.0)
        .px(16.0)
        .py(10.0)
        .gap(14.0)
        .center_y()
        .child(
            el().key("check-slot")
                .size(18.0, 18.0)
                .center()
                .child(light),
        )
        .child(
            col()
                .key("copy")
                .grow(1.0)
                .shrink()
                .gap(2.0)
                .child(
                    text(title)
                        .key("title")
                        .font(p.k.ty.ui_medium)
                        .color(p.text()),
                )
                .child(
                    text(detail)
                        .key("detail")
                        .font(DESCRIPTION)
                        .color(p.muted())
                        .wrap(),
                ),
        )
        .children(control)
}

fn setup(p: Paint, m: &Model) -> El {
    let loaded = m.loaded.then_some(());
    let cli = match (&m.executable, loaded) {
        (_, None) => checklist_row(
            p,
            "cli",
            check(p, "check", None),
            "kamui command-line tool",
            "Looking…",
            None,
        ),
        (Some(_), _) if m.uses_bundled_executable() => checklist_row(
            p,
            "cli",
            check(p, "check", Some(true)),
            "kamui command-line tool",
            "Using the copy inside Kamui. Install kamui in your terminal later and the app follows it.",
            None,
        ),
        (Some(path), _) => checklist_row(
            p,
            "cli",
            check(p, "check", Some(true)),
            "kamui command-line tool",
            &format!("Using {}, the same one as your terminal.", path.display()),
            None,
        ),
        (None, _) => checklist_row(
            p,
            "cli",
            check(p, "check", Some(false)),
            "kamui command-line tool",
            "Not found. Choose where it is installed.",
            Some(super::window::choose_executable_button(p)),
        ),
    };
    let hosts = match (m.hosts.len(), loaded) {
        (_, None) => checklist_row(
            p,
            "hosts",
            check(p, "check", None),
            "SSH hosts",
            "Reading ~/.ssh/config…",
            None,
        ),
        (0, _) => checklist_row(
            p,
            "hosts",
            check(p, "check", Some(true)),
            "SSH hosts",
            "None in ~/.ssh/config yet. You can type any destination you'd pass to ssh.",
            None,
        ),
        (1, _) => checklist_row(
            p,
            "hosts",
            check(p, "check", Some(true)),
            "SSH hosts",
            &format!("{} from ~/.ssh/config", m.hosts[0]),
            None,
        ),
        (n, _) => checklist_row(
            p,
            "hosts",
            check(p, "check", Some(true)),
            "SSH hosts",
            &format!("{n} hosts from ~/.ssh/config"),
            None,
        ),
    };
    let login = checklist_row(
        p,
        "login",
        optional(p, "check", m.launches_at_login),
        "Open at login",
        "Keeps Kamui in the menu bar after you restart. Sessions keep running when you quit it.",
        Some(
            Switch::new(m.launches_at_login)
                .label("Open at login")
                .hide_label()
                .on_toggle(|m: &mut Model| {
                    let on = !m.launches_at_login;
                    m.set_launch_at_login(on);
                })
                .build(p.k)
                .key("login-switch"),
        ),
    );
    col()
        .gap(28.0)
        .items_center()
        .child(heading(
            p,
            "Check your setup",
            "Kamui runs the kamui command-line tool with your own OpenSSH.",
        ))
        .child(staggered(
            theme::card(p, "checklist", vec![cli, hosts, login]).w(COLUMN),
            1.0,
        ))
        .child(staggered(
            continue_button(p, "Continue", m.executable.is_some()),
            2.0,
        ))
}

fn host_row(p: Paint, host: &str, selected: bool, connected: bool) -> El {
    let owned = host.to_owned();
    let mut item = row()
        .key(format!("host-{host}"))
        .role("radio")
        .label(host)
        .checked(selected)
        .focusable(true)
        .w_full()
        .h(36.0)
        .px(12.0)
        .gap(10.0)
        .center_y()
        .radius(p.k.radius.md)
        .hover(p.k.c.surface_hover)
        .on_press(move |m: &mut Model| pick(m, &owned))
        .child(
            el().key("radio")
                .size(14.0, 14.0)
                .radius(7.0)
                .border(
                    if selected { 4.5 } else { 1.0 },
                    if selected {
                        p.strong()
                    } else {
                        p.k.c.border_strong
                    },
                )
                .motion(theme::UI_SPRING),
        )
        .child(
            text(host)
                .key("name")
                .font(p.k.ty.ui)
                .color(p.text())
                .grow(1.0),
        );
    if selected {
        item = item.fill(p.k.c.surface_raised);
    }
    if connected {
        item = item.child(theme::tone_tag(p, "connected", p.success(), "Connected"));
    }
    item
}

fn connect_page(p: Paint, m: &Model, tour: &Tour) -> El {
    let typed = !m.destination.text.trim().is_empty();
    let choice = if typed { None } else { tour.choice.as_deref() };
    let host = tour.host.as_deref();
    let pending = host.is_some_and(|h| m.pending.contains(h));
    let asking = host.is_some_and(|h| m.front_prompt().is_some_and(|f| f.destination == h));
    // While a connection is under way the picker folds to the host it is
    // connecting to, which leaves room for a prompt.
    let active = host.filter(|_| pending || asking || tour.advance_at.is_some());
    let shown: Vec<&String> = match active {
        Some(active) => m.hosts.iter().filter(|h| h.as_str() == active).collect(),
        None => m.hosts.iter().collect(),
    };
    let mut rows: Vec<El> = shown
        .into_iter()
        .map(|host| {
            let connected = m
                .session(host)
                .is_some_and(|s| s.state == crate::core::SessionState::Connected);
            host_row(
                p,
                host,
                active.map_or(choice == Some(host.as_str()), |a| a == host),
                connected,
            )
            .transition(theme::roll(4.0))
        })
        .collect();
    if let Some(active) = active.filter(|a| !m.hosts.iter().any(|h| h == a)) {
        rows.push(host_row(p, active, true, false));
    }
    let list = (!rows.is_empty()).then(|| {
        col()
            .key("hosts")
            .w_full()
            .max_h(220.0)
            .padding(6.0)
            .gap(2.0)
            .scroll(rune::ScrollSpec {
                stick_bottom: false,
            })
            .focus_group()
            .children(rows)
    });
    let field = active.is_none().then(|| {
        row()
            .key("other")
            .w_full()
            .px(12.0)
            .py(10.0)
            .gap(10.0)
            .center_y()
            .transition(theme::roll(4.0))
            .child(
                TextInput::new(&m.destination)
                    .placeholder(if m.hosts.is_empty() {
                        "user@host"
                    } else {
                        "Or type a destination, like user@host"
                    })
                    .label("SSH destination")
                    .width(None)
                    .on_edit(|m: &mut Model| &mut m.destination)
                    .on_change(|m: &mut Model, _| {
                        if let Some(tour) = &mut m.tour {
                            tour.choice = None;
                        }
                    })
                    .on_submit(|m: &mut Model, _| connect(m))
                    .build(p.k)
                    .key("destination"),
            )
    });
    let card_rows: Vec<El> = list.into_iter().chain(field).collect();

    let status = connection_status(p, m, tour);
    let ready = typed || choice.is_some();
    let label = if tour.advance_at.is_some() {
        "Connected"
    } else if pending {
        "Connecting…"
    } else if host.is_some_and(|h| m.failures.contains_key(h)) {
        "Try again"
    } else if choice.is_some_and(|c| {
        m.session(c)
            .is_some_and(|s| s.state == crate::core::SessionState::Connected)
    }) {
        "Continue"
    } else {
        "Connect"
    };
    let button = (!asking).then(|| {
        staggered(
            primary(label)
                .disabled(!ready || pending || tour.advance_at.is_some())
                .on_press(connect)
                .build(p.k)
                .key("connect")
                .default_button(),
            2.0,
        )
    });
    col()
        .gap(24.0)
        .items_center()
        .child(heading(
            p,
            "Connect to a host",
            "Pick a host from ~/.ssh/config, or type what you'd pass to ssh.",
        ))
        .child(staggered(
            theme::card(p, "picker", card_rows).w(COLUMN),
            1.0,
        ))
        .child(
            col()
                .key("status-slot")
                .w(COLUMN)
                .items_center()
                .children(status),
        )
        .children(button)
}

/// Connecting, a prompt, the failure, or the success check, under the
/// picker.
fn connection_status(p: Paint, m: &Model, tour: &Tour) -> Option<El> {
    let host = tour.host.as_deref()?;
    if let Some(prompt) = m.prompts.iter().find(|q| q.destination == host)
        && m.front_prompt()
            .is_some_and(|f| f.prompt.id == prompt.prompt.id && f.destination == host)
    {
        return Some(
            inline_prompt(p, m, prompt)
                .key("prompt")
                .transition(arrives()),
        );
    }
    if tour.advance_at.is_some() {
        return Some(
            row()
                .key("connected")
                .gap(8.0)
                .center_y()
                .child(check(p, "check", Some(true)))
                .child(
                    text(format!("Connected to {host}"))
                        .key("label")
                        .font(p.k.ty.ui_medium)
                        .color(p.text()),
                )
                .transition(arrives()),
        );
    }
    if m.pending.contains(host) {
        let line = m
            .notices
            .get(host)
            .cloned()
            .unwrap_or_else(|| format!("Connecting to {host}…"));
        return Some(
            row()
                .key("connecting")
                .gap(8.0)
                .center_y()
                .child(p.pulse("pulse", 8.0))
                .child(text(line).key("label").font(p.k.ty.ui).color(p.muted()))
                .transition(arrives()),
        );
    }
    let failure = m.failures.get(host)?;
    Some(
        col()
            .key("failure")
            .w_full()
            .gap(4.0)
            .items_center()
            .child(
                text(format!("Couldn't connect to {host}"))
                    .key("title")
                    .font(p.k.ty.ui_medium)
                    .color(p.danger()),
            )
            .child(
                text(super::model::truncated(failure, 160))
                    .key("detail")
                    .font(DESCRIPTION)
                    .color(p.muted())
                    .align(Align::Center)
                    .wrap()
                    .selectable(true),
            )
            .transition(arrives()),
    )
}

fn ports(p: Paint, m: &Model, tour: &Tour) -> El {
    let host = tour.host.clone().unwrap_or_default();
    let session = m.session(&host);
    let ports = session
        .map(|s| s.mirror.mirrored_ports.clone())
        .unwrap_or_default();
    let body = if ports.is_empty() {
        let line = match session {
            Some(s) if !s.mirror.enabled => format!("Mirroring is off for {host}."),
            _ => format!("Watching {host} for services…"),
        };
        col()
            .key("waiting")
            .w_full()
            .px(16.0)
            .py(18.0)
            .gap(10.0)
            .items_center()
            .child(
                row()
                    .key("watching")
                    .gap(8.0)
                    .center_y()
                    .child(p.pulse("pulse", 8.0))
                    .child(text(line).key("label").font(p.k.ty.ui).color(p.muted())),
            )
            .child(
                row()
                    .key("hint")
                    .gap(6.0)
                    .center_y()
                    .child(
                        text("Try starting one there:")
                            .key("lead")
                            .font(DESCRIPTION)
                            .color(p.faint()),
                    )
                    .child(super::window::code(
                        p,
                        "example",
                        "python3 -m http.server 8000",
                    )),
            )
    } else {
        // A busy server can have dozens: the list scrolls, so the heading
        // and Continue stay in the window. Half a row shows past the edge
        // to say there is more.
        let rows = if ports.len() > VISIBLE_PORTS {
            VISIBLE_PORTS as f32 + 0.5
        } else {
            ports.len() as f32
        };
        col()
            .key("ports")
            .w_full()
            .h(rows * PORT_ROW + (rows.ceil() - 1.0).max(0.0) * p.hair)
            .scroll(rune::ScrollSpec {
                stick_bottom: false,
            })
            .scroll_edge_fade(12.0)
            .children(ports.iter().enumerate().map(|(i, port)| {
                let port = *port;
                let mut item = super::window::port_row(p, port);
                if i > 0 {
                    item = col()
                        .key(format!("port-{port}-wrap"))
                        .w_full()
                        .child(p.separator("separator"))
                        .child(item);
                }
                item.transition(theme::roll(6.0))
            }))
    };
    let held = session.map(|s| s.mirror.conflicts.len()).unwrap_or(0);
    let mut summary = Vec::new();
    if ports.len() > VISIBLE_PORTS {
        summary.push(format!("{} ports mirrored.", ports.len()));
    }
    if held > 0 {
        summary.push(format!(
            "{held} {} in use on this Mac, so Kamui left {} alone.",
            if held == 1 { "port is" } else { "ports are" },
            if held == 1 { "it" } else { "them" }
        ));
    }
    let held_note = (!summary.is_empty()).then(|| {
        text(summary.join(" "))
            .key("held")
            .font(DESCRIPTION)
            .color(p.muted())
    });
    col()
        .gap(24.0)
        .items_center()
        .child(heading(
            p,
            "Ports follow along",
            &format!("Anything listening on {host}'s localhost appears here, on the same port. Open one in your browser."),
        ))
        .child(staggered(p.raised("ports-card").w(COLUMN).child(body), 1.0))
        .children(held_note)
        .child(staggered(continue_button(p, "Continue", true), 2.0))
}

fn done(p: Paint, tour: &Tour) -> El {
    let k = p.k;
    let pill = k.elevate(
        row()
            .key("menubar-pill")
            .h(30.0)
            .px(12.0)
            .gap(10.0)
            .center_y()
            .radius(15.0)
            .fill(k.c.surface_raised)
            .border(p.hair, k.c.border_weak)
            .child(vortex(p, "menubar-mark", 14.0, p.strong(), tour.turns))
            .child(el().key("sep").size(p.hair, 12.0).fill(k.c.border_base))
            .child(
                text("Lives quietly in your menu bar")
                    .key("copy")
                    .font(k.ty.small)
                    .color(p.muted()),
            ),
        &k.elevation.raised,
    );
    col()
        .gap(28.0)
        .items_center()
        .child(staggered(vortex(p, "mark", 88.0, p.strong(), tour.turns), 0.0))
        .child(staggered(
            heading(p, "Kamui is ready", "Sessions keep running when you close this window. Open it again from the menu bar."),
            1.0,
        ))
        .child(staggered(pill, 2.0))
        .child(staggered(
            row()
                .key("actions")
                .gap(10.0)
                .child(
                    Button::new("Open Kamui")
                        .variant(ButtonVariant::Neutral)
                        .control_size(ControlSize::Large)
                        .on_press_tx(|m: &mut Model, tx| finish(m, false, tx))
                        .build(p.k)
                        .key("open"),
                )
                .child(primary("Done").on_press_tx(|m: &mut Model, tx| finish(m, true, tx)).build(p.k).key("done").default_button()),
            3.0,
        ))
}
