//! The toast: a small card at the top centre of the screen, under the menu
//! bar, that says how a connection went while the window is closed. It
//! fades away on its own; clicking it opens that session in the window.
//!
//! The macOS host draws it in a Rune overlay, a panel that never takes focus
//! and lets clicks outside the card through. Headless runs show it as the
//! `toast` window: `kamui-app --snap out.png --window toast --scene failed`.

use std::collections::VecDeque;
use std::time::Duration;

use rune::{AppSpec, Cx, El, Tx, col, el, row, text};

use super::theme::Paint;

/// The overlay's size in points: the card, with room around it for its
/// shadow. Clicks on the clear part pass through.
pub const SIZE: (f32, f32) = (380.0, 150.0);
const CARD_W: f32 = 340.0;
/// Points between the card and the overlay's edge, for the shadow.
pub const GUTTER: f32 = (SIZE.0 - CARD_W) / 2.0;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Tone {
    Connected,
    Warning,
    Failed,
}

impl Tone {
    /// How long the toast stays. A warning or an error takes longer to read.
    fn duration(self) -> Duration {
        match self {
            Tone::Connected => Duration::from_secs(4),
            Tone::Warning | Tone::Failed => Duration::from_secs(8),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Toast {
    pub title: String,
    pub detail: String,
    /// The session clicking the toast opens.
    pub destination: Option<String>,
    pub tone: Tone,
}

impl Toast {
    /// The detail as the card shows it. A warning's dot already says it is
    /// one, so the card drops kamui's `WARNING:`; the window keeps it.
    pub fn shown_detail(&self) -> String {
        if self.tone != Tone::Warning {
            return self.detail.clone();
        }
        let lines: Vec<&str> = self
            .detail
            .lines()
            .map(|l| l.trim_start_matches("WARNING:").trim_start())
            .collect();
        lines.join("\n")
    }
}

/// What the overlay shows, and how it reaches the host.
#[derive(Default)]
pub struct ToastState {
    pub toast: Option<Toast>,
    pub showing: bool,
    /// Toasts waiting for a warning or an error on screen to go, so that a
    /// later toast never hides one before it has been seen.
    queue: VecDeque<Toast>,
    /// Counts the toasts shown. The card's key carries it, so a press that
    /// began on a toast that has since been replaced opens nothing.
    serial: u64,
    /// When it goes, on the overlay's clock. Set by the first frame after
    /// it shows, which is the first time that clock is known.
    deadline: Option<f64>,
    /// Hides the overlay.
    pub on_dismiss: Option<Box<dyn Fn()>>,
    /// Opens the window for the clicked toast, then those still waiting.
    pub on_open: Option<Box<dyn Fn(Vec<Toast>)>>,
}

impl ToastState {
    /// Shows `toast`. It replaces a toast that says a host connected, and
    /// otherwise waits its turn. A repeat of the last one is dropped.
    pub fn show(&mut self, toast: Toast) {
        let current = self.toast.as_ref().filter(|_| self.showing);
        if self.queue.back().or(current) == Some(&toast) {
            return;
        }
        if self.queue.is_empty() && current.is_none_or(|t| t.tone == Tone::Connected) {
            self.put(toast);
        } else {
            self.queue.push_back(toast);
        }
    }

    fn put(&mut self, toast: Toast) {
        self.toast = Some(toast);
        self.showing = true;
        self.serial += 1;
        self.deadline = None;
    }

    /// The card's key, which changes with each toast.
    pub fn key(&self) -> String {
        format!("toast-{}", self.serial)
    }

    /// The toast's time is up: the next one waiting shows, or the overlay
    /// hides.
    pub fn dismiss(&mut self) {
        if !self.showing {
            return;
        }
        match self.queue.pop_front() {
            Some(next) => self.put(next),
            None => self.hide(),
        }
    }

    fn hide(&mut self) {
        self.showing = false;
        self.deadline = None;
        if let Some(dismiss) = &self.on_dismiss {
            dismiss();
        }
    }

    /// The toast was clicked: the window takes it and any still waiting.
    pub fn open(&mut self) {
        if !self.showing {
            return;
        }
        let toasts: Vec<Toast> = self
            .toast
            .iter()
            .cloned()
            .chain(self.queue.drain(..))
            .collect();
        self.hide();
        if let Some(open) = &self.on_open {
            open(toasts);
        }
    }
}

/// Counts the toast down, and dismisses it when its time is up.
pub fn poll(s: &mut ToastState, now: f64, tx: &mut Tx) -> bool {
    let Some(toast) = s.toast.as_ref().filter(|_| s.showing) else {
        return false;
    };
    let deadline = *s
        .deadline
        .get_or_insert(now + toast.tone.duration().as_secs_f64());
    if now >= deadline {
        s.dismiss();
        // A toast that was waiting starts its countdown now.
        if let Some(next) = s.toast.as_ref().filter(|_| s.showing) {
            let duration = next.tone.duration();
            s.deadline = Some(now + duration.as_secs_f64());
            tx.wake_after(duration);
        }
        return true;
    }
    tx.wake_after(Duration::from_secs_f64(deadline - now));
    false
}

pub fn view(s: &ToastState, cx: &mut Cx<ToastState>) -> El {
    let p = Paint::new(cx.system_dark(), cx.env().scale);
    let root = col()
        .key("toast-root")
        .w_full()
        .h_full()
        .padding(GUTTER)
        .items_center();
    match &s.toast {
        Some(toast) => root.child(card(p, toast, s.key())),
        None => root,
    }
}

fn card(p: Paint, toast: &Toast, key: String) -> El {
    let dot = match toast.tone {
        Tone::Connected => p.success(),
        Tone::Warning => p.warning(),
        Tone::Failed => p.danger(),
    };
    let detail = toast.shown_detail();
    let body = col()
        .key("content")
        .grow(1.0)
        .shrink()
        .gap(2.0)
        .child(
            text(&toast.title)
                .key("title")
                .font(p.k.ty.ui_medium)
                .color(p.strong())
                .w_full()
                .max_lines(1),
        )
        .child(
            text(&detail)
                .key("detail")
                .font(p.k.ty.small)
                .color(p.muted())
                .w_full()
                .wrap()
                .max_lines(3),
        );
    let card = row()
        .key(key)
        .role("button")
        .label(&toast.title)
        .description(&detail)
        .w(CARD_W)
        .padding(12.0)
        .gap(10.0)
        .items_start()
        .radius(p.k.radius.lg)
        .fill(p.k.c.bg_base)
        .hover(p.k.c.surface_hover)
        .hit()
        .on_press(ToastState::open)
        .child(
            el().key("tone")
                .size(8.0, 18.0)
                .center()
                .child(el().key("dot").size(8.0, 8.0).radius(4.0).fill(dot)),
        )
        .child(body);
    p.k.elevate(card, &p.k.elevation.floating)
}

type Scene = (&'static str, fn(&mut ToastState));

/// Fixture toasts for headless snapshots.
pub const SCENES: &[Scene] = &[
    ("connected", |s| {
        s.show(Toast {
            title: "Connected to reyna".into(),
            detail: "Its ports appear in the menu as services start.".into(),
            destination: Some("reyna".into()),
            tone: Tone::Connected,
        })
    }),
    ("warning", |s| {
        s.show(Toast {
            title: "Connected to reyna".into(),
            detail: "WARNING: SSH agent forwarding is enabled for reyna; the remote host can \
                     access the forwarded agent."
                .into(),
            destination: Some("reyna".into()),
            tone: Tone::Warning,
        })
    }),
    ("failed", |s| {
        s.show(Toast {
            title: "Could not connect to pi".into(),
            detail: "ssh: connect to host pi port 22: Operation timed out".into(),
            destination: Some("pi".into()),
            tone: Tone::Failed,
        })
    }),
];

fn fixture() -> ToastState {
    let mut s = ToastState::default();
    (SCENES[0].1)(&mut s);
    s
}

/// The toast as a window, for headless runs.
pub fn spec() -> AppSpec<ToastState> {
    AppSpec {
        scenes: SCENES,
        fonts: rune_theme_opencode::FONTS,
        families: rune_theme_opencode::FAMILIES,
        poll: Some(poll),
        ..AppSpec::new("Kamui toast", SIZE, fixture, view)
    }
}
