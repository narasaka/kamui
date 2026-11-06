//! The main window, after setup: sessions in a sidebar beside a raised
//! panel that shows one session, the Connect page, or Settings.

use rune::{Align, El, FileDialogOptions, Tx, col, el, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Switch, TextInput};

use super::mark::vortex;
use super::model::{Model, Page};
use super::onboarding;
use super::theme::{self, DESCRIPTION, PANE_TITLE, Paint, section, setting_row, tone_tag};
use crate::core::{Session, SessionState};

const SIDEBAR_W: f32 = 220.0;
const HEADER_H: f32 = 52.0;

pub fn view(p: Paint, m: &Model) -> El {
    row()
        .key("window")
        .w_full()
        .h_full()
        .fill(p.k.c.bg_deep)
        .child(sidebar(p, m))
        .child(panel(p, m))
}

// Sidebar.

fn state_color(p: Paint, m: &Model, destination: &str) -> rune::Color {
    match m.session(destination) {
        Some(s) if s.needs_attention() => p.warning(),
        Some(s) if s.state == SessionState::Connected => p.success(),
        _ => p.faint(),
    }
}

fn sidebar_row(
    p: Paint,
    key: &str,
    label: &str,
    detail: Option<String>,
    lead: El,
    selected: bool,
    page: Page,
) -> El {
    let mut item = row()
        .key(key)
        .role("button")
        .label(label)
        .checked(selected)
        .focusable(true)
        .w_full()
        .h(30.0)
        .px(10.0)
        .gap(9.0)
        .center_y()
        .radius(p.k.radius.md)
        .hover(p.k.c.surface_hover)
        .on_press(move |m: &mut Model| m.page = page.clone())
        .child(el().key("lead").size(14.0, 14.0).center().child(lead))
        .child(
            text(label)
                .key("label")
                .font(p.k.ty.ui)
                .color(p.text())
                .grow(1.0)
                .shrink()
                .max_lines(1),
        );
    if let Some(detail) = detail {
        item = item.child(
            text(detail)
                .key("detail")
                .font(p.k.ty.small)
                .color(p.faint()),
        );
    }
    if selected {
        item = item.fill(p.k.c.surface_raised);
    }
    item
}

fn sidebar(p: Paint, m: &Model) -> El {
    let sessions: Vec<El> = m
        .destinations()
        .into_iter()
        .map(|destination| {
            let pending = m.pending.contains(&destination)
                || m.session(&destination)
                    .is_some_and(|s| s.state == SessionState::Connecting);
            let lead = if pending {
                p.pulse("pulse", 7.0)
            } else {
                p.dot("dot", state_color(p, m, &destination), 7.0)
            };
            let ports = m
                .session(&destination)
                .map_or(0, |s| s.mirror.mirrored_ports.len());
            let selected = m.page == Page::Session(destination.clone());
            sidebar_row(
                p,
                &format!("session-{destination}"),
                &destination,
                (ports > 0).then(|| ports.to_string()),
                lead,
                selected,
                Page::Session(destination.clone()),
            )
            .transition(theme::roll(4.0))
        })
        .collect();
    let empty = sessions.is_empty().then(|| {
        text("No sessions yet")
            .key("empty")
            .font(p.k.ty.small)
            .color(p.faint())
            .px(10.0)
            .py(6.0)
    });
    let glyph =
        |key: &str, icon: &'static rune::Icon| rune::icon(icon, 14.0).key(key).color(p.muted());
    col()
        .key("sidebar")
        .w(SIDEBAR_W)
        .h_full()
        .pt(44.0)
        .px(10.0)
        .pb(12.0)
        .gap(2.0)
        .window_drag()
        .child(
            text("Sessions")
                .key("sessions-label")
                .font(p.k.ty.small_medium)
                .color(p.faint())
                .px(10.0)
                .pb(4.0),
        )
        .children(sessions)
        .children(empty)
        .child(el().key("gap").h(10.0))
        .child(sidebar_row(
            p,
            "nav-connect",
            "Connect",
            None,
            glyph("plus", &rune_theme_opencode::icons::PLUS),
            m.page == Page::Connect,
            Page::Connect,
        ))
        .child(el().key("spacer").grow(1.0))
        .child(
            row()
                .key("brand")
                .px(10.0)
                .pb(6.0)
                .gap(8.0)
                .center_y()
                .child(vortex(p, "brand-mark", 14.0, p.muted(), 0))
                .child(
                    text("Kamui")
                        .key("name")
                        .font(p.k.ty.small_medium)
                        .color(p.muted()),
                ),
        )
        .child(sidebar_row(
            p,
            "nav-settings",
            "Settings",
            None,
            glyph("gear", &rune_theme_opencode::icons::SETTINGS_GEAR),
            m.page == Page::Settings,
            Page::Settings,
        ))
}

// Panel.

fn panel(p: Paint, m: &Model) -> El {
    let (header, body) = match &m.page {
        Page::Session(destination) => session_page(p, m, destination),
        Page::Connect => connect_page(p, m),
        Page::Settings => settings_page(p, m),
    };
    let surface = col()
        .key("panel")
        .grow(1.0)
        .shrink()
        .min_w(0.0)
        .h_full()
        .mt(8.0)
        .mr(8.0)
        .mb(8.0)
        .radius(p.k.radius.lg)
        .fill(p.k.c.bg_base)
        .border(p.hair, p.k.c.border_weak)
        .clip_soft(0.0)
        .child(header.window_drag())
        .child(
            col()
                .key(format!("body-{}", page_key(&m.page)))
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .child(
                    col()
                        .key("content")
                        .w_full()
                        .px(16.0)
                        .pt(8.0)
                        .pb(24.0)
                        .gap(24.0)
                        .max_w(680.0)
                        .children(body),
                )
                .transition(theme::arrives()),
        );
    let banner = m.problem.as_ref().map(|problem| problem_banner(p, problem));
    col()
        .key("panel-column")
        .grow(1.0)
        .shrink()
        .min_w(0.0)
        .h_full()
        .child(surface)
        .children(banner)
}

fn page_key(page: &Page) -> String {
    match page {
        Page::Session(destination) => format!("session-{destination}"),
        Page::Connect => "connect".into(),
        Page::Settings => "settings".into(),
    }
}

/// kamui itself failed: not found, a status it can't read, a controller
/// from another build.
pub fn problem_banner(p: Paint, problem: &str) -> El {
    row()
        .key("problem")
        .mx(8.0)
        .mb(8.0)
        .px(14.0)
        .py(10.0)
        .gap(10.0)
        .center_y()
        .radius(p.k.radius.md)
        .fill(p.k.c.warning.bg)
        .border(p.hair, p.k.c.warning.border)
        .child(p.dot("dot", p.warning(), 8.0))
        .child(
            text(problem)
                .key("text")
                .font(DESCRIPTION)
                .color(p.text())
                .wrap()
                .grow(1.0)
                .shrink()
                .selectable(true),
        )
        .transition(theme::arrives())
}

fn header(p: Paint, title: &str, tag: Option<El>, actions: Vec<El>) -> El {
    row()
        .key("header")
        .w_full()
        .h(HEADER_H)
        .px(16.0)
        .gap(10.0)
        .center_y()
        .child(
            text(title)
                .key("title")
                .role("heading")
                .level(1)
                .font(PANE_TITLE)
                .color(p.strong())
                .max_lines(1)
                .shrink(),
        )
        .children(tag)
        .child(el().key("spacer").grow(1.0))
        .child(row().key("actions").gap(8.0).children(actions))
}

pub fn code(p: Paint, key: &str, snippet: &str) -> El {
    row()
        .key(key)
        .px(6.0)
        .py(2.0)
        .radius(p.k.radius.sm)
        .fill(p.k.c.surface_raised)
        .border(p.hair, p.k.c.border_weak)
        .child(
            text(snippet)
                .key("code")
                .font(p.k.ty.mono)
                .color(p.text())
                .selectable(true),
        )
}

pub fn port_row(p: Paint, port: u16) -> El {
    let label = format!("localhost:{port}");
    row()
        .key(format!("port-{port}"))
        .w_full()
        .h(44.0)
        .px(16.0)
        .gap(10.0)
        .center_y()
        .child(p.dot("live", p.success(), 6.0))
        .child(
            text(&label)
                .key("address")
                .font(p.k.ty.mono)
                .color(p.text())
                .grow(1.0)
                .selectable(true),
        )
        .child(
            Button::new("Open")
                .variant(ButtonVariant::Neutral)
                .control_size(ControlSize::Small)
                .help(format!("Open http://{label} in your browser"))
                .on_press_tx(move |_: &mut Model, tx: &mut Tx| Model::open_port(port, tx))
                .build(p.k)
                .key("open"),
        )
}

fn state_tag(p: Paint, m: &Model, destination: &str, session: Option<&Session>) -> El {
    match session {
        _ if m.pending.contains(destination) => tone_tag(p, "state", p.faint(), "Connecting"),
        Some(s) if s.needs_attention() => tone_tag(p, "state", p.warning(), s.state.label()),
        Some(s) if s.state == SessionState::Connected => {
            tone_tag(p, "state", p.success(), "Connected")
        }
        Some(s) => tone_tag(p, "state", p.faint(), s.state.label()),
        None => tone_tag(p, "state", p.faint(), "Connecting"),
    }
}

fn session_page(p: Paint, m: &Model, destination: &str) -> (El, Vec<El>) {
    let session = m.session(destination);
    let pending = m.pending.contains(destination);
    let owned = destination.to_owned();
    let mut actions = Vec::new();
    if session.is_some_and(|s| s.state.can_reconnect()) && !pending {
        let target = owned.clone();
        actions.push(
            Button::new("Reconnect")
                .variant(ButtonVariant::Contrast)
                .on_press(move |m: &mut Model| {
                    m.connect(&target);
                })
                .build(p.k)
                .key("reconnect"),
        );
    }
    let target = owned.clone();
    actions.push(
        Button::new(if pending && session.is_none() {
            "Stop connecting"
        } else {
            "Stop"
        })
        .variant(ButtonVariant::Neutral)
        .on_press(move |m: &mut Model| m.stop(&target))
        .build(p.k)
        .key("stop"),
    );
    let head = header(
        p,
        destination,
        Some(state_tag(p, m, destination, session)),
        actions,
    );

    let mut body = Vec::new();
    if let Some(notice) = m.notices.get(destination) {
        body.push(
            row()
                .key("notice")
                .w_full()
                .px(16.0)
                .gap(8.0)
                .center_y()
                .child(p.pulse("pulse", 8.0))
                .child(
                    text(notice)
                        .key("text")
                        .font(p.k.ty.ui)
                        .color(p.muted())
                        .wrap(),
                )
                .transition(theme::arrives()),
        );
    }
    let Some(session) = session else {
        body.push(
            text(format!(
                "Connecting to {destination}. Its ports appear here once it is connected."
            ))
            .key("connecting")
            .font(p.k.ty.ui)
            .color(p.muted())
            .px(16.0)
            .wrap(),
        );
        return (head, body);
    };

    let ports = &session.mirror.mirrored_ports;
    let port_rows: Vec<El> = if ports.is_empty() {
        let line = if session.mirror.enabled {
            "No mirrored ports yet. Start a service on the host and it appears here."
        } else {
            "Mirroring is off for this session."
        };
        vec![
            row()
                .key("no-ports")
                .w_full()
                .h(48.0)
                .px(16.0)
                .center_y()
                .child(text(line).key("text").font(p.k.ty.ui).color(p.muted())),
        ]
    } else {
        ports
            .iter()
            .map(|port| port_row(p, *port).transition(theme::roll(6.0)))
            .collect()
    };
    body.push(section(p, "ports", "Mirrored ports", port_rows));

    if !session.mirror.conflicts.is_empty() {
        let rows = session
            .mirror
            .conflicts
            .iter()
            .map(|c| {
                setting_row(
                    p,
                    &format!("conflict-{}", c.port),
                    &format!("localhost:{}", c.port),
                    Some(
                        c.error
                            .as_deref()
                            .unwrap_or("Another program on this Mac uses this port."),
                    ),
                    None,
                )
            })
            .collect();
        body.push(section(p, "conflicts", "Held elsewhere on this Mac", rows));
    }

    let mut problems = Vec::new();
    if let Some(error) = &session.mirror.last_error {
        problems.push(error_row(p, "mirror-error", "Port discovery", error));
    }
    if let Some(error) = &session.last_error {
        problems.push(error_row(p, "last-error", "Last error", error));
    }
    if let Some(error) = m.failures.get(destination).filter(|_| !pending) {
        problems.push(error_row(p, "failure", "Last connection attempt", error));
    }
    if !problems.is_empty() {
        body.push(section(p, "problems", "Problems", problems));
    }

    let mut about = Vec::new();
    if let Some(browser) = &session.browser {
        about.push(setting_row(
            p,
            "browser",
            "Browser profile",
            Some(&format!(
                "kamui browser {destination} opens this host in an isolated {browser} profile"
            )),
            None,
        ));
    }
    if !session.mirror.excluded_ports.is_empty() {
        let list = session
            .mirror
            .excluded_ports
            .iter()
            .map(u16::to_string)
            .collect::<Vec<_>>()
            .join(", ");
        about.push(setting_row(
            p,
            "excluded",
            "Not mirrored",
            Some(&list),
            None,
        ));
    }
    if !about.is_empty() {
        body.push(section(p, "about", "Session", about));
    }
    (head, body)
}

fn error_row(p: Paint, key: &str, title: &str, detail: &str) -> El {
    col()
        .key(key)
        .w_full()
        .px(16.0)
        .py(12.0)
        .gap(4.0)
        .child(
            text(title)
                .key("title")
                .font(p.k.ty.ui_medium)
                .color(p.text()),
        )
        .child(
            text(detail)
                .key("detail")
                .font(p.k.ty.mono)
                .color(p.muted())
                .wrap()
                .selectable(true),
        )
}

fn connect_page(p: Paint, m: &Model) -> (El, Vec<El>) {
    let head = header(p, "Connect", None, Vec::new());
    let field = row()
        .key("new")
        .w_full()
        .px(16.0)
        .py(12.0)
        .gap(10.0)
        .center_y()
        .child(
            TextInput::new(&m.destination)
                .placeholder("user@host, or an alias from ~/.ssh/config")
                .label("SSH destination")
                .width(None)
                .on_edit(|m: &mut Model| &mut m.destination)
                .on_submit(|m: &mut Model, _| m.connect_typed())
                .build(p.k)
                .key("destination")
                .grow(1.0),
        )
        .child(
            Button::new("Connect")
                .variant(ButtonVariant::Contrast)
                .disabled(m.destination.text.trim().is_empty() || m.executable.is_none())
                .on_press(Model::connect_typed)
                .build(p.k)
                .key("connect")
                .default_button(),
        );
    let mut body = vec![section(p, "destination", "Any destination", vec![field])];
    if m.hosts.is_empty() {
        body.push(
            text("Hosts you add to ~/.ssh/config appear here.")
                .key("no-hosts")
                .font(DESCRIPTION)
                .color(p.faint())
                .px(16.0),
        );
    } else {
        let rows = m
            .hosts
            .iter()
            .map(|host| {
                let session = m.session(host);
                let control = if m.pending.contains(host) {
                    p.pulse("pulse", 8.0)
                } else if session.is_some_and(|s| s.state == SessionState::Connected) {
                    let target = host.clone();
                    Button::new("Show")
                        .variant(ButtonVariant::Ghost)
                        .control_size(ControlSize::Small)
                        .on_press(move |m: &mut Model| m.page = Page::Session(target.clone()))
                        .build(p.k)
                } else {
                    let target = host.clone();
                    Button::new(if session.is_some() {
                        "Reconnect"
                    } else {
                        "Connect"
                    })
                    .variant(ButtonVariant::Neutral)
                    .control_size(ControlSize::Small)
                    .disabled(m.executable.is_none())
                    .on_press(move |m: &mut Model| {
                        if let Some(destination) = m.connect(&target) {
                            m.page = Page::Session(destination);
                        }
                    })
                    .build(p.k)
                };
                let detail = session.map(|s| {
                    if s.state == SessionState::Connected {
                        "Connected".to_owned()
                    } else {
                        s.state.label().to_owned()
                    }
                });
                setting_row(
                    p,
                    &format!("host-{host}"),
                    host,
                    detail.as_deref(),
                    Some(control.key("action")),
                )
            })
            .collect();
        body.push(section(p, "hosts", "From ~/.ssh/config", rows));
    }
    (head, body)
}

pub fn choose_executable_button(p: Paint) -> El {
    Button::new("Choose…")
        .variant(ButtonVariant::Neutral)
        .control_size(ControlSize::Small)
        .on_press_tx(|_: &mut Model, tx: &mut Tx| {
            let options = FileDialogOptions {
                title: "Choose the kamui executable".into(),
                directory: Some("/opt/homebrew/bin".into()),
                ..Default::default()
            };
            tx.open_file(options, |m: &mut Model, result, _| match result {
                Ok(Some(path)) => m.set_executable_override(Some(path.display().to_string())),
                Ok(None) => {}
                Err(error) => m.note("Kamui", &error.to_string()),
            });
        })
        .build(p.k)
        .key("choose")
}

fn settings_page(p: Paint, m: &Model) -> (El, Vec<El>) {
    let head = header(p, "Settings", None, Vec::new());
    let login = setting_row(
        p,
        "login",
        "Open at login",
        Some("Keep Kamui in the menu bar after you restart"),
        Some(
            Switch::new(m.launches_at_login)
                .label("Open at login")
                .hide_label()
                .on_toggle(|m: &mut Model| {
                    let on = !m.launches_at_login;
                    m.set_launch_at_login(on);
                })
                .build(p.k)
                .key("switch"),
        ),
    );
    let using = match &m.executable {
        Some(_) if m.uses_bundled_executable() => "The copy inside Kamui".to_owned(),
        Some(path) => path.display().to_string(),
        None => "Not found".to_owned(),
    };
    let how = if m.executable_override.is_some() {
        "Chosen in Settings"
    } else {
        "Found automatically: your terminal's kamui first, then the copy inside Kamui"
    };
    let mut cli_actions = vec![choose_executable_button(p)];
    if m.bundled.is_some() {
        let bundled = m.bundled.clone();
        cli_actions.push(
            Button::new("Use bundled kamui")
                .variant(ButtonVariant::Neutral)
                .control_size(ControlSize::Small)
                .disabled(m.uses_bundled_executable() && m.executable_override.is_some())
                .on_press(move |m: &mut Model| {
                    m.set_executable_override(bundled.as_ref().map(|b| b.display().to_string()))
                })
                .build(p.k)
                .key("bundled"),
        );
    }
    cli_actions.push(
        Button::new("Find automatically")
            .variant(ButtonVariant::Ghost)
            .control_size(ControlSize::Small)
            .disabled(m.executable_override.is_none())
            .on_press(|m: &mut Model| m.set_executable_override(None))
            .build(p.k)
            .key("automatic"),
    );
    let cli = col()
        .key("cli")
        .w_full()
        .px(16.0)
        .py(12.0)
        .gap(10.0)
        .child(
            col()
                .key("copy")
                .gap(2.0)
                .child(
                    text(&using)
                        .key("path")
                        .font(p.k.ty.mono)
                        .color(p.text())
                        .wrap()
                        .selectable(true),
                )
                .child(
                    text(how)
                        .key("how")
                        .font(DESCRIPTION)
                        .color(p.muted())
                        .wrap(),
                ),
        )
        .child(row().key("buttons").gap(8.0).children(cli_actions));
    let setup = setting_row(
        p,
        "setup",
        "Setup",
        Some("Walk through connecting your first host again"),
        Some(
            Button::new("Run setup again")
                .variant(ButtonVariant::Neutral)
                .control_size(ControlSize::Small)
                .on_press(onboarding::restart)
                .build(p.k)
                .key("restart"),
        ),
    );
    let footer = col()
        .key("footer")
        .w_full()
        .pt(8.0)
        .gap(4.0)
        .items_center()
        .child(vortex(p, "mark", 20.0, p.faint(), 0))
        .child(
            text(if m.version == "0.0.0" {
                "Kamui (development build)".to_owned()
            } else {
                format!("Kamui {}", m.version)
            })
            .key("version")
            .font(p.k.ty.small)
            .color(p.faint())
            .align(Align::Center),
        );
    let body = vec![
        section(p, "general", "General", vec![login, setup]),
        section(p, "cli", "kamui command-line tool", vec![cli]),
        footer,
    ];
    (head, body)
}
