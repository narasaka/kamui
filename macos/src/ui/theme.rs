//! Kamui's look: rune-kit components with OpenCode's tokens
//! (`rune_theme_opencode`), following the system appearance. Views take
//! colours, type, radii and elevation from here and never write a colour
//! literal.

use rune::{Color, El, Spring, SpringPair, TextStyle, Transition, col, el, row, text};
use rune_kit::Kit;

/// Everything a view needs to paint itself.
#[derive(Clone, Copy)]
pub struct Paint {
    pub k: &'static Kit,
    /// One device pixel, for hairlines.
    pub hair: f32,
}

impl Paint {
    pub fn new(dark: bool, scale: f32) -> Self {
        Self {
            k: rune_theme_opencode::kit(dark),
            hair: 1.0 / scale.max(1.0),
        }
    }

    pub fn text(self) -> Color {
        self.k.c.text_base
    }
    pub fn strong(self) -> Color {
        self.k.c.text_strong
    }
    pub fn muted(self) -> Color {
        self.k.c.text_muted
    }
    pub fn faint(self) -> Color {
        self.k.c.text_faint
    }
    pub fn success(self) -> Color {
        self.k.c.success.fg
    }
    pub fn warning(self) -> Color {
        self.k.c.warning.fg
    }
    pub fn danger(self) -> Color {
        self.k.c.danger.fg
    }

    /// A raised surface: a card of rows.
    pub fn raised(self, key: &str) -> El {
        self.k.elevate(
            col()
                .key(key)
                .fill(self.k.c.bg_base)
                .radius(self.k.radius.lg),
            &self.k.elevation.raised,
        )
    }

    /// A muted label above a card. Screen readers move between sections by it.
    pub fn section_label(self, title: &str) -> El {
        text(title)
            .key("section-label")
            .role("heading")
            .level(2)
            .font(self.k.ty.ui_medium.lh(16.0))
            .color(self.muted())
    }

    /// A hairline between rows of a card, inset from both edges.
    pub fn separator(self, key: &str) -> El {
        row()
            .key(key)
            .w_full()
            .px(16.0)
            .child(
                el().key("line")
                    .grow(1.0)
                    .h(self.hair)
                    .fill(self.k.c.border_weak),
            )
            .a11y_hidden()
    }

    /// A state light that breathes while something is under way. Reduced
    /// motion holds it still.
    pub fn pulse(self, key: &str, size: f32) -> El {
        self.dot(key, self.muted(), size).cycle(rune::Cycle {
            period: 1.4,
            phase: 0.0,
            stops: &[(0.0, 1.0), (0.5, 0.3), (1.0, 1.0)],
            still: 1.0,
            fps: 30.0,
        })
    }

    /// A small round state light.
    pub fn dot(self, key: &str, color: Color, size: f32) -> El {
        el().key(key)
            .size(size, size)
            .radius(size / 2.0)
            .fill(color)
            .a11y_hidden()
    }
}

/// Large headings: onboarding steps and the session page.
pub const TITLE: TextStyle = TextStyle::new(26.0, 600.0).lh(32.0).tracking(-0.35);
pub const PANE_TITLE: TextStyle = TextStyle::new(17.0, 600.0).lh(22.0).tracking(-0.2);
/// The line under a heading.
pub const LEAD: TextStyle = TextStyle::new(15.0, 410.0).lh(22.0);
/// Row descriptions and secondary copy.
pub const DESCRIPTION: TextStyle = TextStyle::new(12.0, 440.0).lh(16.0);

/// The product's one UI spring: 0.30 s, no bounce.
pub const UI_SPRING: Spring = Spring::new(0.30, 0.0);
const ENTER: Spring = Spring::new(0.34, 0.0);
const EXIT: Spring = Spring::new(0.26, 0.0);
/// The vortex's third of a turn: a little bounce, so it settles like a dial.
pub const TURN: Spring = Spring::new(0.7, 0.12);

/// Rises in as it fades in, lifts away as it fades out.
pub fn roll(distance: f32) -> Transition {
    let fade = |y| Transition::offset(0.0, y).combined(Transition::opacity());
    Transition::asymmetric(fade(distance), fade(-distance)).springs(SpringPair::new(ENTER, EXIT))
}

/// `child` rolls in `rank` beats after the first element of its group.
pub fn staggered(child: El, rank: f32) -> El {
    child.transition(roll(8.0)).enter_delay(rank * 0.05)
}

/// Replaces a whole state in place.
pub fn arrives() -> Transition {
    Transition::scale(0.97)
        .combined(Transition::opacity())
        .springs(SpringPair::new(UI_SPRING, EXIT))
}

/// A row of a settings card: title and optional description on the left, a
/// control on the right.
pub fn setting_row(
    p: Paint,
    key: &str,
    title: &str,
    description: Option<&str>,
    control: Option<El>,
) -> El {
    let mut copy = col().key("copy").grow(1.0).shrink().gap(2.0).child(
        text(title)
            .key("title")
            .font(p.k.ty.ui_medium)
            .color(p.text()),
    );
    if let Some(description) = description {
        copy = copy.child(
            text(description)
                .key("description")
                .font(DESCRIPTION)
                .color(p.muted())
                .wrap(),
        );
    }
    row()
        .key(key)
        .w_full()
        .min_h(if description.is_some() { 60.0 } else { 48.0 })
        .px(16.0)
        .py(10.0)
        .gap(16.0)
        .center_y()
        .child(copy)
        .children(control)
}

/// A card of rows with hairlines between them.
pub fn card(p: Paint, key: &str, rows: Vec<El>) -> El {
    let mut children = Vec::with_capacity(rows.len() * 2);
    for (i, row) in rows.into_iter().enumerate() {
        if i > 0 {
            children.push(p.separator(&format!("separator-{i}")));
        }
        children.push(row);
    }
    p.raised(key).children(children)
}

/// A titled card.
pub fn section(p: Paint, key: &str, title: &str, rows: Vec<El>) -> El {
    col()
        .key(key)
        .w_full()
        .gap(8.0)
        .child(p.section_label(title).mx(16.0))
        .child(card(p, "card", rows))
}

/// Status text that is never clickable: a tinted dot and a word.
pub fn tone_tag(p: Paint, key: &str, color: Color, label: &str) -> El {
    row()
        .key(key)
        .h(22.0)
        .px(8.0)
        .gap(6.0)
        .center_y()
        .radius(11.0)
        .fill(p.k.c.surface_raised)
        .border(p.hair, p.k.c.border_weak)
        .child(p.dot("dot", color, 6.0))
        .child(
            text(label)
                .key("label")
                .font(p.k.ty.small_medium)
                .color(p.text()),
        )
}
