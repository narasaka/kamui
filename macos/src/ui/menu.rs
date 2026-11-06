//! The menu bar item's menu, as data. The macOS host draws it as a native
//! `NSMenu` and sends picks back through [`pick`], so the menu and what it
//! does are tested without AppKit. Native menus keep the macOS title-case
//! convention; the window uses sentence case.

use rune::Tx;

use super::model::{Model, Page, truncated};
use crate::core::{Indicator, SessionState};

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Action {
    ShowWindow,
    ShowSession(String),
    OpenPort(u16),
    Connect(String),
    OtherDestination,
    Stop(String),
    StopAll,
    Settings,
    ToggleLogin,
    Quit,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Item {
    Action {
        title: String,
        action: Action,
        enabled: bool,
        checked: bool,
        key: Option<&'static str>,
    },
    /// Text that does nothing when picked, such as a notice.
    Label(String),
    Separator,
    Submenu {
        title: String,
        symbol: Option<&'static str>,
        items: Vec<Item>,
    },
}

fn action(title: impl Into<String>, action: Action) -> Item {
    Item::Action {
        title: title.into(),
        action,
        enabled: true,
        checked: false,
        key: None,
    }
}

fn with(item: Item, enabled: bool, key: Option<&'static str>) -> Item {
    match item {
        Item::Action {
            title,
            action,
            checked,
            ..
        } => Item::Action {
            title,
            action,
            enabled,
            checked,
            key,
        },
        other => other,
    }
}

/// What the menu bar icon shows. A prompt waiting for an answer needs the
/// user too.
pub fn indicator(m: &Model) -> Indicator {
    Indicator::new(
        &m.sessions,
        !m.pending.is_empty(),
        m.problem.is_some() || !m.prompts.is_empty(),
    )
}

pub fn items(m: &Model) -> Vec<Item> {
    let mut items = Vec::new();
    if let Some(problem) = &m.problem {
        items.push(action(truncated(problem, 70), Action::ShowWindow));
        items.push(Item::Separator);
    }
    if let Some(prompt) = m.front_prompt() {
        items.push(action(
            format!("Answer {}'s SSH Prompt…", prompt.destination),
            Action::ShowWindow,
        ));
        items.push(Item::Separator);
    }

    if m.sessions.is_empty() && m.pending.is_empty() {
        items.push(Item::Label("No Active Sessions".into()));
    }
    for session in &m.sessions {
        let destination = &session.destination;
        let mut sub = Vec::new();
        if let Some(notice) = m.notices.get(destination) {
            sub.push(Item::Label(truncated(notice, 70)));
            sub.push(Item::Separator);
        }
        if session.state.can_reconnect() {
            sub.push(with(
                action("Reconnect", Action::Connect(destination.clone())),
                !m.pending.contains(destination),
                None,
            ));
            sub.push(Item::Separator);
        }
        if session.mirror.mirrored_ports.is_empty() {
            sub.push(Item::Label(if session.mirror.enabled {
                "No Mirrored Ports".into()
            } else {
                "Mirroring Is Off".into()
            }));
        } else {
            sub.push(Item::Label("Open in Browser".into()));
            for port in &session.mirror.mirrored_ports {
                sub.push(action(format!("localhost:{port}"), Action::OpenPort(*port)));
            }
        }
        if !session.mirror.conflicts.is_empty() {
            sub.push(Item::Separator);
            sub.push(Item::Label("Ports Held Elsewhere".into()));
            for conflict in &session.mirror.conflicts {
                sub.push(Item::Label(conflict.port.to_string()));
            }
        }
        if session.mirror.last_error.is_some() || session.last_error.is_some() {
            sub.push(Item::Separator);
            sub.push(action(
                "Show Errors…",
                Action::ShowSession(destination.clone()),
            ));
        }
        sub.push(Item::Separator);
        sub.push(action(
            "Show in Kamui…",
            Action::ShowSession(destination.clone()),
        ));
        sub.push(action(
            format!("Stop {destination}"),
            Action::Stop(destination.clone()),
        ));
        let state = if m.pending.contains(destination) {
            "Connecting"
        } else {
            session.state.label()
        };
        items.push(Item::Submenu {
            title: format!("{destination} — {state}"),
            symbol: Some(symbol(&session.state, session.needs_attention())),
            items: sub,
        });
    }
    for destination in m.pending.iter().filter(|d| m.session(d).is_none()) {
        let line = m
            .notices
            .get(destination)
            .map_or("Connecting…".to_owned(), |n| truncated(n, 50));
        items.push(Item::Submenu {
            title: format!("{destination} — {line}"),
            symbol: Some("circle.dotted"),
            items: vec![
                action("Show in Kamui…", Action::ShowSession(destination.clone())),
                action(
                    format!("Stop {destination}"),
                    Action::Stop(destination.clone()),
                ),
            ],
        });
    }

    items.push(Item::Separator);
    let can_connect = m.executable.is_some();
    let mut connect: Vec<Item> = m
        .hosts
        .iter()
        .map(|host| {
            with(
                action(host.clone(), Action::Connect(host.clone())),
                can_connect && !m.is_busy(host),
                None,
            )
        })
        .collect();
    if !connect.is_empty() {
        connect.push(Item::Separator);
    }
    connect.push(action("Other Destination…", Action::OtherDestination));
    items.push(Item::Submenu {
        title: "Connect".into(),
        symbol: None,
        items: connect,
    });
    items.push(with(
        action("Stop All Sessions", Action::StopAll),
        !m.sessions.is_empty() || !m.pending.is_empty(),
        None,
    ));

    items.push(Item::Separator);
    items.push(action("Open Kamui…", Action::ShowWindow));
    items.push(with(action("Settings…", Action::Settings), true, Some(",")));
    items.push(Item::Action {
        title: "Open at Login".into(),
        action: Action::ToggleLogin,
        enabled: true,
        checked: m.launches_at_login,
        key: None,
    });
    items.push(with(action("Quit Kamui", Action::Quit), true, Some("q")));
    items
}

/// SF Symbols for a session's state.
fn symbol(state: &SessionState, attention: bool) -> &'static str {
    match state {
        _ if attention => "exclamationmark.circle",
        SessionState::Connected => "circle.fill",
        _ => "circle.dotted",
    }
}

/// Runs a picked menu item.
pub fn pick(m: &mut Model, action: Action, tx: &mut Tx) {
    match action {
        Action::ShowWindow => {
            if m.tour.is_none() && m.problem.is_some() && m.prompts.is_empty() {
                // The banner under the panel explains it.
                m.page = m
                    .destinations()
                    .into_iter()
                    .next()
                    .map_or(Page::Connect, Page::Session);
            }
            m.backend.show_window();
        }
        Action::ShowSession(destination) => {
            if m.tour.is_none() {
                m.page = Page::Session(destination);
            }
            m.backend.show_window();
        }
        Action::OpenPort(port) => Model::open_port(port, tx),
        Action::Connect(destination) => {
            m.connect(&destination);
        }
        Action::OtherDestination => {
            if m.tour.is_none() {
                m.page = Page::Connect;
            }
            m.backend.show_window();
        }
        Action::Stop(destination) => m.stop(&destination),
        Action::StopAll => m.stop_all(),
        Action::Settings => {
            if m.tour.is_none() {
                m.page = Page::Settings;
            }
            m.backend.show_window();
        }
        Action::ToggleLogin => {
            let on = !m.launches_at_login;
            m.set_launch_at_login(on);
        }
        Action::Quit => m.backend.quit(),
    }
}
