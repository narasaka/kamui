//! The window: drawn with Rune and rune-kit in OpenCode's design language.
//! It shows first-run setup until that is done, then the sessions window.
//! Prompts and messages float over either.

use rune::{Anim, AppSpec, Cx, El, Key, KeyEvent, Tx};

pub mod mark;
pub mod menu;
pub mod model;
pub mod onboarding;
pub mod prompt;
pub mod scenes;
pub mod theme;
pub mod toast;
pub mod window;

use model::{Model, Page};
use onboarding::Step;
use theme::Paint;

pub const TITLE: &str = "Kamui";
pub const SIZE: (f32, f32) = (860.0, 620.0);
pub const MIN_SIZE: (f32, f32) = (700.0, 540.0);

pub fn view(m: &Model, cx: &mut Cx<Model>) -> El {
    let p = Paint::new(cx.system_dark(), cx.env().scale);
    let base = match &m.tour {
        Some(tour) => onboarding::view(p, m, tour),
        None => window::view(p, m),
    };
    let mut layer = Vec::new();
    prompt::layer(p, m, prompt_is_inline(m), &mut layer);
    rune_kit::root(
        base.hover_motion_within(Anim::INSTANT, Anim::INSTANT),
        layer,
    )
}

/// The tour's connect step shows its own host's prompt in the page instead
/// of a dialog.
pub fn prompt_is_inline(m: &Model) -> bool {
    let Some(tour) = &m.tour else { return false };
    tour.step == Step::Connect
        && m.front_prompt()
            .is_some_and(|p| tour.host.as_deref() == Some(p.destination.as_str()))
}

fn poll(m: &mut Model, now: f64, tx: &mut Tx) -> bool {
    m.poll(now, tx)
}

/// ⌘W closes the window, ⌘N and ⌘, open Connect and Settings, ⌘Q quits.
fn on_key(m: &mut Model, ev: &KeyEvent, tx: &mut Tx) -> bool {
    match ev.key {
        Key::Command('w') => tx.close_window(),
        Key::Command('q') => m.backend.quit(),
        Key::Command('n') if m.tour.is_none() => m.page = Page::Connect,
        Key::Command(',') if m.tour.is_none() => m.page = Page::Settings,
        _ => return false,
    }
    true
}

/// The window's spec. `init` builds a fixture model; the macOS host
/// replaces it with the live one, and headless runs (`--snap`, `--tree`)
/// keep it, with [`scenes::SCENES`] to choose a state.
pub fn spec() -> AppSpec<Model> {
    AppSpec {
        min_size: Some(MIN_SIZE),
        scenes: scenes::SCENES,
        fonts: rune_theme_opencode::FONTS,
        families: rune_theme_opencode::FAMILIES,
        poll: Some(poll),
        on_key: Some(on_key),
        ..AppSpec::new(TITLE, SIZE, scenes::fixture, view)
    }
}

#[cfg(test)]
mod tests;
