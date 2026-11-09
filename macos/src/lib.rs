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
    if !headless {
        // Off macOS, a run shows the window alone.
        rune::run(ui::spec());
        return;
    }
    // `--window toast` renders the toast; the window is the default.
    rune::run_windows(|_| {
        [
            rune::WindowSpec::new("main", ui::spec()).boxed(),
            rune::WindowSpec::new("toast", ui::toast::spec()).boxed(),
        ]
    });
}
