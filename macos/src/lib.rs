//! Kamui's macOS menu bar app. It drives the `kamui` CLI through its public
//! contract (`status --json`, `mirror --json-prompts`, `stop`) and never talks
//! to the controller socket (ADR 0012, ADR 0015, ADR 0016).

pub mod core;
#[cfg(target_os = "macos")]
mod mac;
pub mod ui;

/// Flags that make Rune render the window headless instead of opening it.
const HEADLESS: [&str; 7] = [
    "--snap",
    "--tree",
    "--list-scenes",
    "--script",
    "--video",
    "--out",
    "--trace",
];

pub fn run() {
    let headless = std::env::args()
        .skip(1)
        .any(|a| HEADLESS.contains(&a.as_str()));
    #[cfg(target_os = "macos")]
    if !headless {
        mac::run();
        return;
    }
    let _ = headless;
    rune::run(ui::spec());
}
