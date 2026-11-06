//! The window and model, driven headless through Rune's `Runtime` with a
//! backend that records what it was asked to do.

use std::rc::Rc;
use std::sync::{Arc, Mutex};

use rune::{Env, Runtime, TextEdit};

use super::menu::{self, Action, Item};
use super::model::{Model, Page, PendingPrompt, Snapshot};
use super::onboarding::{ADVANCE_BEAT, Step, Tour};
use super::scenes::{self, Recorder};
use super::{SIZE, view};
use crate::core::prompt::{PromptKind, PromptResponder, SshPrompt};
use crate::core::runner::CommandInput;
use crate::core::{Indicator, KamuiError, Session, SessionState};

fn runtime(m: Model) -> Runtime<Model> {
    let mut rt = Runtime::new(
        m,
        view,
        SIZE,
        Env {
            system_dark: false,
            scale: 2.0,
        },
    );
    rt.frame(0.0);
    rt
}

fn settle(rt: &mut Runtime<Model>) {
    for _ in 0..90 {
        rt.frame(1.0 / 60.0);
    }
}

fn press(rt: &mut Runtime<Model>, key: &str) {
    let (x, y) = rt
        .find(key)
        .unwrap_or_else(|| panic!("no element keyed {key}"));
    rt.pointer_down(x, y);
    rt.pointer_up(x, y);
    settle(rt);
}

/// The fixture with a recorder whose calls the test reads.
fn fixture() -> (Model, Rc<Recorder>) {
    let (mut m, recorder) = scenes::recorded();
    let base = scenes::fixture();
    m.executable = base.executable;
    m.bundled = base.bundled;
    m.hosts = base.hosts;
    m.sessions = base.sessions;
    m.launches_at_login = true;
    m.loaded = true;
    m.page = base.page;
    (m, recorder)
}

fn first_run() -> (Model, Rc<Recorder>) {
    let (mut m, recorder) = fixture();
    m.sessions.clear();
    m.page = Page::Connect;
    m.tour = Some(Tour::new());
    (m, recorder)
}

/// A responder whose replies land in `written`.
fn responder(id: i64) -> (PromptResponder, Arc<Mutex<String>>) {
    let input = CommandInput::new();
    let written = Arc::new(Mutex::new(String::new()));
    let sink = written.clone();
    input.attach(Box::new(move |data: &[u8]| {
        sink.lock()
            .unwrap()
            .push_str(&String::from_utf8_lossy(data));
        Ok(())
    }));
    (PromptResponder::new(id, input), written)
}

fn ask(m: &mut Model, destination: &str, kind: PromptKind) -> Arc<Mutex<String>> {
    let (responder, written) = responder(1);
    m.prompt_arrived(
        destination,
        SshPrompt {
            id: 1,
            kind,
            text: "Password: ".into(),
        },
        responder,
    );
    written
}

#[test]
fn every_scene_draws_in_both_appearances() {
    for (name, scene) in scenes::SCENES {
        for dark in [false, true] {
            let mut m = scenes::fixture();
            scene(&mut m);
            let mut rt = Runtime::new(
                m,
                view,
                SIZE,
                Env {
                    system_dark: dark,
                    scale: 2.0,
                },
            );
            rt.frame(0.0);
            settle(&mut rt);
            assert!(!rt.elements().is_empty(), "scene {name} drew nothing");
        }
    }
}

#[test]
fn the_tour_walks_forward_and_back_and_the_vortex_turns_each_step() {
    let (m, _) = first_run();
    let mut rt = runtime(m);
    press(&mut rt, "continue");
    assert_eq!(rt.state.tour.as_ref().unwrap().step, Step::HowItWorks);
    press(&mut rt, "continue");
    assert_eq!(rt.state.tour.as_ref().unwrap().step, Step::Setup);
    press(&mut rt, "back");
    assert_eq!(rt.state.tour.as_ref().unwrap().step, Step::HowItWorks);
    press(&mut rt, "continue");
    press(&mut rt, "continue");
    let tour = rt.state.tour.as_ref().unwrap();
    assert_eq!(tour.step, Step::Connect);
    assert_eq!(
        tour.turns, 4,
        "one third of a turn per step forward, none back"
    );
}

#[test]
fn setup_waits_for_the_cli_before_continuing() {
    let (mut m, _) = first_run();
    m.executable = None;
    m.tour = Some(Tour::at(Step::Setup));
    let mut rt = runtime(m);
    press(&mut rt, "continue");
    assert_eq!(
        rt.state.tour.as_ref().unwrap().step,
        Step::Setup,
        "Continue is disabled without kamui"
    );
    assert!(rt.find("choose").is_some(), "and offers to choose it");
}

#[test]
fn connecting_in_the_tour_holds_the_check_for_a_beat_then_shows_ports() {
    let (mut m, recorder) = first_run();
    m.tour = Some(Tour::at(Step::Connect));
    let mut rt = runtime(m);
    press(&mut rt, "host-reyna");
    press(&mut rt, "connect");
    assert!(recorder.calls().contains(&"connect reyna".to_owned()));
    assert_eq!(
        rt.state.tour.as_ref().unwrap().host.as_deref(),
        Some("reyna")
    );
    assert!(
        rt.find("host-lab").is_none(),
        "the picker folds to the host it connects to"
    );

    rt.update(|m, tx| {
        m.sessions = vec![Session::new("reyna", SessionState::Connected)];
        m.clock = 10.0;
        m.connect_finished("reyna", Ok(Vec::new()), tx);
    });
    settle(&mut rt);
    assert!(rt.find("connected").is_some(), "the check shows");
    rt.update(|m, tx| {
        m.poll(10.0 + ADVANCE_BEAT / 2.0, tx);
    });
    assert_eq!(
        rt.state.tour.as_ref().unwrap().step,
        Step::Connect,
        "and holds"
    );
    rt.update(|m, tx| {
        m.poll(10.0 + ADVANCE_BEAT, tx);
    });
    settle(&mut rt);
    assert_eq!(rt.state.tour.as_ref().unwrap().step, Step::Ports);
    assert!(
        recorder.calls().iter().filter(|c| *c == "refresh").count() >= 2,
        "ports poll faster"
    );
}

#[test]
fn a_typed_destination_wins_over_the_picked_host() {
    let (mut m, recorder) = first_run();
    m.tour = Some(Tour::at(Step::Connect));
    m.tour.as_mut().unwrap().choice = Some("reyna".into());
    m.destination = TextEdit::new("nathan@192.168.1.50");
    let mut rt = runtime(m);
    press(&mut rt, "connect");
    assert!(
        recorder
            .calls()
            .contains(&"connect nathan@192.168.1.50".to_owned())
    );
}

#[test]
fn a_failure_in_the_tour_shows_on_the_page_not_in_a_dialog() {
    let (mut m, _) = first_run();
    m.tour = Some(Tour::at(Step::Connect));
    m.tour.as_mut().unwrap().choice = Some("lab".into());
    let mut rt = runtime(m);
    press(&mut rt, "connect");
    rt.update(|m, tx| {
        m.connect_finished(
            "lab",
            Err(KamuiError::CommandFailed(
                "Permission denied (publickey).".into(),
            )),
            tx,
        )
    });
    settle(&mut rt);
    assert!(rt.state.notes.is_empty());
    assert!(rt.find("failure").is_some());
    assert!(rt.find("connect").is_some(), "Try again stays");
}

#[test]
fn a_failure_outside_the_tour_is_a_message() {
    let (mut m, recorder) = fixture();
    m.connect("pi");
    let mut rt = runtime(m);
    rt.update(|m, tx| {
        m.connect_finished("pi", Err(KamuiError::CommandFailed("timed out".into())), tx)
    });
    settle(&mut rt);
    assert_eq!(rt.state.notes[0].title, "Could not connect to pi");
    assert!(
        recorder.calls().contains(&"show".to_owned()),
        "the window comes forward"
    );
    press(&mut rt, "ok");
    assert!(rt.state.notes.is_empty());
}

#[test]
fn the_tour_answers_its_hosts_prompt_inline_and_clears_the_answer() {
    let (mut m, _) = first_run();
    m.tour = Some(Tour::at(Step::Connect));
    m.tour.as_mut().unwrap().host = Some("reyna".into());
    m.pending.insert("reyna".into());
    let written = ask(&mut m, "reyna", PromptKind::Secret);
    m.answer = TextEdit::new("open sesame");
    let mut rt = runtime(m);
    assert!(super::prompt_is_inline(&rt.state));
    assert!(rt.find("prompt").is_some() && rt.find("prompt-1-dialog").is_none());
    press(&mut rt, "accept");
    assert_eq!(
        *written.lock().unwrap(),
        "{\"answer\":\"open sesame\",\"id\":1}\n"
    );
    assert!(rt.state.prompts.is_empty());
    assert_eq!(
        rt.state.answer.text, "",
        "the answer leaves memory with the prompt"
    );
}

#[test]
fn cancel_stops_connecting_before_declining() {
    let (mut m, recorder) = fixture();
    m.pending.insert("pi".into());
    ask(&mut m, "pi", PromptKind::Secret);
    let mut rt = runtime(m);
    assert!(
        rt.find("prompt-1-dialog").is_some(),
        "outside the tour it is a dialog"
    );
    press(&mut rt, "decline");
    assert!(
        recorder
            .calls()
            .contains(&"stop-then-decline pi 1".to_owned())
    );
    assert!(
        rt.state.stopping.contains("pi"),
        "so the failure that follows is expected"
    );
    rt.update(|m, tx| {
        m.connect_finished("pi", Err(KamuiError::CommandFailed("cancelled".into())), tx)
    });
    assert!(rt.state.notes.is_empty());
}

#[test]
fn deny_answers_a_confirmation_without_stopping() {
    let (mut m, recorder) = fixture();
    m.pending.insert("pi".into());
    let written = ask(&mut m, "pi", PromptKind::Confirm);
    let mut rt = runtime(m);
    press(&mut rt, "decline");
    assert_eq!(*written.lock().unwrap(), "{\"declined\":true,\"id\":1}\n");
    assert!(!recorder.calls().iter().any(|c| c.starts_with("stop")));
}

#[test]
fn allow_answers_yes() {
    let (mut m, _) = fixture();
    let written = ask(&mut m, "pi", PromptKind::Confirm);
    let mut rt = runtime(m);
    press(&mut rt, "accept");
    assert_eq!(*written.lock().unwrap(), "{\"answer\":\"yes\",\"id\":1}\n");
}

#[test]
fn a_withdrawn_prompt_closes_and_forgets_the_typed_answer() {
    let (mut m, _) = fixture();
    ask(&mut m, "pi", PromptKind::Secret);
    m.answer = TextEdit::new("half typed");
    m.prompt_withdrawn("pi", 1);
    assert!(m.prompts.is_empty());
    assert_eq!(m.answer.text, "");
}

#[test]
fn a_connection_stopped_before_it_reached_the_controller_is_stopped_after() {
    let (mut m, recorder) = fixture();
    m.connect("pi");
    m.stop("pi");
    let mut rt = runtime(m);
    rt.update(|m, tx| m.connect_finished("pi", Ok(Vec::new()), tx));
    assert_eq!(
        recorder.calls().iter().filter(|c| *c == "stop pi").count(),
        2
    );
    assert!(rt.state.notes.is_empty());
}

#[test]
fn warnings_after_connecting_are_shown() {
    let (mut m, _) = fixture();
    m.connect("pi");
    let mut rt = runtime(m);
    rt.update(|m, tx| {
        m.connect_finished(
            "pi",
            Ok(vec![
                "WARNING: SSH agent forwarding is enabled for pi".into(),
            ]),
            tx,
        )
    });
    assert_eq!(rt.state.notes[0].title, "Connected to pi");
}

#[test]
fn a_tailscale_link_opens_and_any_other_waits_for_a_yes() {
    let (m, recorder) = fixture();
    let mut rt = runtime(m);
    rt.update(|m, tx| {
        m.sign_in_requested(
            "lab",
            url::Url::parse("https://login.tailscale.com/a/x").unwrap(),
            tx,
        )
    });
    assert!(rt.state.sign_ins.is_empty());
    rt.update(|m, tx| {
        m.sign_in_requested(
            "lab",
            url::Url::parse("https://headscale.example.com/a/x").unwrap(),
            tx,
        )
    });
    settle(&mut rt);
    assert!(!rt.state.sign_ins.is_empty());
    assert!(recorder.calls().contains(&"show".to_owned()));
    press(&mut rt, "cancel");
    assert!(rt.state.sign_ins.is_empty());
}

#[test]
fn skipping_setup_remembers_it() {
    let (m, recorder) = first_run();
    let mut rt = runtime(m);
    press(&mut rt, "skip");
    assert!(rt.state.tour.is_none());
    assert!(recorder.calls().contains(&"onboarded true".to_owned()));
}

#[test]
fn done_ends_on_the_session_the_tour_connected() {
    let (mut m, recorder) = first_run();
    m.sessions = vec![Session::new("reyna", SessionState::Connected)];
    let mut tour = Tour::at(Step::Done);
    tour.host = Some("reyna".into());
    m.tour = Some(tour);
    let mut rt = runtime(m);
    press(&mut rt, "open");
    assert!(rt.state.tour.is_none());
    assert_eq!(rt.state.page, Page::Session("reyna".into()));
    assert!(recorder.calls().contains(&"onboarded true".to_owned()));
}

#[test]
fn run_setup_again_starts_from_welcome() {
    let (mut m, recorder) = fixture();
    m.page = Page::Settings;
    let mut rt = runtime(m);
    press(&mut rt, "restart");
    assert_eq!(rt.state.tour.as_ref().map(|t| t.step), Some(Step::Welcome));
    assert!(recorder.calls().contains(&"onboarded false".to_owned()));
}

#[test]
fn the_sidebar_switches_pages() {
    let (m, _) = fixture();
    let mut rt = runtime(m);
    press(&mut rt, "session-lab");
    assert_eq!(rt.state.page, Page::Session("lab".into()));
    assert!(
        rt.find("reconnect").is_some(),
        "a session that needs authentication offers Reconnect"
    );
    press(&mut rt, "nav-settings");
    assert_eq!(rt.state.page, Page::Settings);
    press(&mut rt, "nav-connect");
    assert_eq!(rt.state.page, Page::Connect);
}

#[test]
fn the_connect_page_connects_and_shows_the_session() {
    let (mut m, recorder) = fixture();
    m.page = Page::Connect;
    m.destination = TextEdit::new("  work-box ");
    let mut rt = runtime(m);
    press(&mut rt, "connect");
    assert!(recorder.calls().contains(&"connect work-box".to_owned()));
    assert_eq!(rt.state.page, Page::Session("work-box".into()));
    assert_eq!(rt.state.destination.text, "");
}

#[test]
fn an_option_like_destination_is_refused_before_kamui_runs() {
    let (mut m, recorder) = fixture();
    assert_eq!(m.connect("-oProxyCommand=evil"), None);
    assert!(!recorder.calls().iter().any(|c| c.starts_with("connect")));
    assert_eq!(m.notes.len(), 1);
}

#[test]
fn refreshes_report_only_changes_and_leave_a_vanished_session() {
    let (mut m, _) = fixture();
    let same = Snapshot {
        sessions: Some(m.sessions.clone()),
        problem: None,
        executable: m.executable.clone(),
        hosts: m.hosts.clone(),
        launches_at_login: true,
    };
    assert!(
        !m.apply_if_changed(same.clone()),
        "an idle poll redraws nothing"
    );
    let gone = Snapshot {
        sessions: Some(vec![m.sessions[1].clone()]),
        ..same.clone()
    };
    assert!(m.apply_if_changed(gone));
    assert_eq!(
        m.page,
        Page::Session("lab".into()),
        "the page leaves reyna once it is gone"
    );
    let failed = Snapshot {
        sessions: None,
        problem: Some("kamui returned status this app could not read".into()),
        ..same
    };
    assert!(m.apply_if_changed(failed));
    assert_eq!(
        m.sessions.len(),
        1,
        "a failed status keeps the last sessions"
    );
}

#[test]
fn the_window_opens_on_the_first_session_once_known() {
    let (mut m, _) = scenes::recorded();
    assert_eq!(m.page, Page::Connect);
    m.apply(Snapshot {
        sessions: Some(vec![Session::new("reyna", SessionState::Connected)]),
        ..Snapshot::default()
    });
    assert_eq!(m.page, Page::Session("reyna".into()));
}

// The menu.

fn titles(items: &[Item]) -> Vec<String> {
    items
        .iter()
        .filter_map(|item| match item {
            Item::Action { title, .. } | Item::Label(title) | Item::Submenu { title, .. } => {
                Some(title.clone())
            }
            Item::Separator => None,
        })
        .collect()
}

fn submenu<'a>(items: &'a [Item], title: &str) -> &'a [Item] {
    items
        .iter()
        .find_map(|item| match item {
            Item::Submenu {
                title: t, items, ..
            } if t == title => Some(items.as_slice()),
            _ => None,
        })
        .unwrap_or_else(|| panic!("no submenu {title}"))
}

#[test]
fn the_menu_lists_sessions_ports_and_actions() {
    let (m, _) = fixture();
    let items = menu::items(&m);
    assert_eq!(
        titles(&items),
        [
            "reyna — Connected",
            "lab — Authentication required",
            "Connect",
            "Stop All Sessions",
            "Open Kamui…",
            "Settings…",
            "Open at Login",
            "Quit Kamui"
        ]
    );
    let reyna = submenu(&items, "reyna — Connected");
    assert!(reyna.contains(&Item::Action {
        title: "localhost:3000".into(),
        action: Action::OpenPort(3000),
        enabled: true,
        checked: false,
        key: None
    }));
    assert!(titles(reyna).contains(&"Ports Held Elsewhere".to_owned()));
    let lab = submenu(&items, "lab — Authentication required");
    assert_eq!(titles(lab)[0], "Reconnect");
    let connect = submenu(&items, "Connect");
    let enabled: Vec<(String, bool)> = connect
        .iter()
        .filter_map(|i| match i {
            Item::Action { title, enabled, .. } => Some((title.clone(), *enabled)),
            _ => None,
        })
        .collect();
    assert_eq!(
        enabled[0],
        ("reyna".to_owned(), false),
        "a connected host can't be connected again"
    );
    assert_eq!(enabled[1], ("lab".to_owned(), true));
    assert_eq!(enabled.last().unwrap().0, "Other Destination…");
    assert!(items.contains(&Item::Action {
        title: "Open at Login".into(),
        action: Action::ToggleLogin,
        enabled: true,
        checked: true,
        key: None
    }));
}

#[test]
fn the_menu_says_when_nothing_runs_and_leads_with_a_waiting_prompt() {
    let (mut m, _) = fixture();
    m.sessions.clear();
    assert_eq!(titles(&menu::items(&m))[0], "No Active Sessions");
    assert_eq!(menu::indicator(&m), Indicator::Idle);
    m.pending.insert("pi".into());
    m.notices
        .insert("pi".into(), "Confirm user presence for key".into());
    assert_eq!(menu::indicator(&m), Indicator::Connecting);
    ask(&mut m, "pi", PromptKind::Secret);
    let items = menu::items(&m);
    assert_eq!(titles(&items)[0], "Answer pi's SSH Prompt…");
    assert!(titles(&items).contains(&"pi — Confirm user presence for key".to_owned()));
    assert_eq!(menu::indicator(&m), Indicator::Attention);
}

#[test]
fn menu_picks_change_the_model() {
    let (m, recorder) = fixture();
    let mut rt = runtime(m);
    rt.update(|m, tx| menu::pick(m, Action::ShowSession("lab".into()), tx));
    assert_eq!(rt.state.page, Page::Session("lab".into()));
    rt.update(|m, tx| menu::pick(m, Action::Connect("lab".into()), tx));
    rt.update(|m, tx| menu::pick(m, Action::Stop("reyna".into()), tx));
    rt.update(|m, tx| menu::pick(m, Action::ToggleLogin, tx));
    rt.update(|m, tx| menu::pick(m, Action::Settings, tx));
    rt.update(|m, tx| menu::pick(m, Action::Quit, tx));
    assert_eq!(rt.state.page, Page::Settings);
    assert!(!rt.state.launches_at_login);
    let calls = recorder.calls();
    for call in ["connect lab", "stop reyna", "login false", "quit"] {
        assert!(calls.contains(&call.to_owned()), "{call} in {calls:?}");
    }
}

#[test]
fn pending_prompts_are_kept_in_order() {
    let (mut m, _) = fixture();
    ask(&mut m, "pi", PromptKind::Secret);
    let (second, _) = responder(2);
    m.prompt_arrived(
        "lab",
        SshPrompt {
            id: 2,
            kind: PromptKind::Confirm,
            text: "Allow?".into(),
        },
        second,
    );
    let front: Option<&PendingPrompt> = m.front_prompt();
    assert_eq!(front.map(|p| p.destination.as_str()), Some("pi"));
    let serial = m.front_prompt().unwrap().serial;
    m.answer_prompt(serial);
    assert_eq!(
        m.front_prompt().map(|p| p.destination.as_str()),
        Some("lab")
    );
}

#[test]
fn many_ports_scroll_inside_the_tour_and_keep_continue_in_the_window() {
    let mut m = scenes::fixture();
    let (_, scene) = scenes::SCENES
        .iter()
        .find(|(name, _)| *name == "ports-many")
        .unwrap();
    scene(&mut m);
    let mut rt = runtime(m);
    settle(&mut rt);
    for key in ["title", "continue"] {
        let (rect, _) = rt.presented(key).unwrap_or_else(|| panic!("no {key}"));
        assert!(
            rect[1] >= 0.0 && rect[1] + rect[3] <= SIZE.1,
            "{key} at {rect:?} is outside the window"
        );
    }
}

#[test]
fn a_host_that_is_already_connected_goes_straight_to_its_ports() {
    let (mut m, recorder) = first_run();
    m.sessions = vec![Session::new("reyna", SessionState::Connected)];
    m.tour = Some(Tour::at(Step::Connect));
    m.tour.as_mut().unwrap().choice = Some("reyna".into());
    let mut rt = runtime(m);
    press(&mut rt, "connect");
    assert_eq!(rt.state.tour.as_ref().unwrap().step, Step::Ports);
    assert!(
        !recorder.calls().iter().any(|c| c.starts_with("connect")),
        "kamui isn't asked to connect again"
    );
}

#[test]
fn a_press_that_began_on_a_withdrawn_prompt_does_not_answer_the_next() {
    let (mut m, _) = fixture();
    let first = ask(&mut m, "pi", PromptKind::Confirm);
    let mut rt = runtime(m);
    settle(&mut rt);
    let (x, y) = rt.find("accept").unwrap();
    rt.pointer_down(x, y);
    let (second, written) = responder(1);
    rt.update(move |m, _| {
        m.prompt_withdrawn("pi", 1);
        m.prompt_arrived(
            "lab",
            SshPrompt {
                id: 1,
                kind: PromptKind::Confirm,
                text: "Allow?".into(),
            },
            second,
        );
    });
    rt.frame(1.0 / 60.0);
    rt.pointer_up(x, y);
    settle(&mut rt);
    assert_eq!(*first.lock().unwrap(), "");
    assert_eq!(
        *written.lock().unwrap(),
        "",
        "lab's prompt was never shown under the press"
    );
    assert_eq!(
        rt.state.front_prompt().map(|p| p.destination.as_str()),
        Some("lab")
    );
    let stale = rt.state.front_prompt().unwrap().serial - 1;
    rt.state.answer_prompt(stale);
    assert_eq!(
        *written.lock().unwrap(),
        "",
        "an answer for an old serial is ignored"
    );
}

#[test]
fn first_run_setup_waits_for_a_status_that_answered() {
    let (mut m, _) = scenes::recorded();
    m.problem = Some("the running Kamui controller belongs to a different kamui build".into());
    assert!(!m.decide_first_run(false, false));
    assert!(
        m.tour.is_none(),
        "a failed status says nothing about running sessions"
    );
    assert!(!m.first_run_decided, "so the next refresh decides");

    m.sessions = vec![Session::new("reyna", SessionState::Connected)];
    assert!(
        m.decide_first_run(false, true),
        "sessions already run: remember setup as done"
    );
    assert!(m.tour.is_none());

    let (mut fresh, recorder) = scenes::recorded();
    assert!(!fresh.decide_first_run(false, true));
    assert!(fresh.tour.is_some());
    assert!(recorder.calls().contains(&"show".to_owned()));
    fresh.tour = None;
    assert!(
        !fresh.decide_first_run(false, true),
        "it decides once per launch"
    );
    assert!(fresh.tour.is_none());

    let (mut done, _) = scenes::recorded();
    assert!(!done.decide_first_run(true, true));
    assert!(done.tour.is_none());
}

#[test]
fn the_tour_shows_what_is_wrong_with_kamui() {
    let (mut m, _) = first_run();
    m.problem = Some("The kamui controller running now is from another build.".into());
    let rt = runtime(m);
    assert!(rt.find("problem").is_some());
}

fn connects(recorder: &Recorder, destination: &str) -> usize {
    recorder
        .calls()
        .iter()
        .filter(|c| **c == format!("connect {destination}"))
        .count()
}

/// Stop is pressed while `pi` connects; the connection then reaches the
/// controller, so a cleanup stop follows. Returns the ids of the user's stop
/// and the cleanup stop.
fn stopped_while_connecting() -> (Runtime<Model>, Rc<Recorder>, u64, u64) {
    let (mut m, recorder) = fixture();
    m.connect("pi");
    m.stop("pi");
    let user = m.stop_serial;
    let mut rt = runtime(m);
    rt.update(|m, tx| m.connect_finished("pi", Ok(Vec::new()), tx));
    let cleanup = rt.state.cleanup["pi"];
    assert_ne!(user, cleanup);
    (rt, recorder, user, cleanup)
}

#[test]
fn a_stopped_connection_stays_busy_until_every_stop_for_it_reports() {
    // The user's stop reports first, then the cleanup stop.
    let (mut rt, recorder, user, cleanup) = stopped_while_connecting();
    assert!(rt.state.is_busy("pi"));
    rt.state
        .stop_finished(user, Err(KamuiError::CommandFailed("no session pi".into())));
    assert!(
        rt.state.is_busy("pi"),
        "the cleanup stop is still on its way"
    );
    assert!(
        rt.state.notes.is_empty(),
        "the early stop had nothing to stop"
    );
    rt.state.connect("pi");
    assert_eq!(
        connects(&recorder, "pi"),
        1,
        "no new attempt for the cleanup stop to end"
    );
    rt.state.stop_finished(cleanup, Ok(()));
    assert!(!rt.state.is_busy("pi"));
    rt.state.connect("pi");
    assert_eq!(connects(&recorder, "pi"), 2);

    // The cleanup stop reports first, then the user's.
    let (mut rt, recorder, user, cleanup) = stopped_while_connecting();
    rt.state.stop_finished(cleanup, Ok(()));
    assert!(
        rt.state.is_busy("pi"),
        "the user's stop is still on its way"
    );
    rt.state.connect("pi");
    assert_eq!(connects(&recorder, "pi"), 1);
    rt.state.stop_finished(user, Ok(()));
    assert!(!rt.state.is_busy("pi"));
}

#[test]
fn a_failed_cleanup_stop_is_reported() {
    // The user's stop came too early to find the session, then the cleanup
    // stop failed too: the session the user stopped is still running.
    let (mut rt, _, user, cleanup) = stopped_while_connecting();
    rt.state.stop_finished(
        user,
        Err(KamuiError::CommandFailed("no Kamui session for pi".into())),
    );
    rt.state.stop_finished(
        cleanup,
        Err(KamuiError::CommandFailed("controller unavailable".into())),
    );
    assert_eq!(rt.state.notes.len(), 1);
}

#[test]
fn stopping_again_during_cleanup_leaves_the_next_connection_alone() {
    for again in ["stop", "stop-all"] {
        let (mut rt, recorder, user, cleanup) = stopped_while_connecting();
        match again {
            "stop" => rt.state.stop("pi"),
            _ => rt.state.stop_all(),
        }
        let repeat = rt.state.stop_serial;
        for id in [user, cleanup, repeat] {
            rt.state.stop_finished(id, Ok(()));
        }
        assert!(
            rt.state.stopping.is_empty(),
            "{again}: nothing is left marked stopping"
        );
        rt.state.connect("pi");
        let stops_before = recorder.calls().iter().filter(|c| *c == "stop pi").count();
        rt.update(|m, tx| m.connect_finished("pi", Ok(Vec::new()), tx));
        let stops_after = recorder.calls().iter().filter(|c| *c == "stop pi").count();
        assert_eq!(
            stops_before, stops_after,
            "{again}: the new session isn't stopped"
        );
        assert!(!rt.state.pending.contains("pi"));
    }
}

#[test]
fn stop_all_holds_every_connection_until_it_reports() {
    let (mut m, recorder) = fixture();
    m.stop_all();
    let id = m.stop_serial;
    assert_eq!(m.connect("pi"), None);
    assert_eq!(connects(&recorder, "pi"), 0);
    m.stop_finished(id, Ok(()));
    assert_eq!(m.connect("pi"), Some("pi".into()));
}

#[test]
fn a_sign_in_click_that_began_on_a_link_that_left_does_not_open_the_next() {
    let (mut m, _) = fixture();
    m.connect("lab");
    m.connect("pi");
    let mut rt = runtime(m);
    let (lab, pi) = (
        url::Url::parse("https://headscale.example.com/a/lab").unwrap(),
        url::Url::parse("https://headscale.example.com/a/pi").unwrap(),
    );
    rt.update(move |m, tx| m.sign_in_requested("lab", lab, tx));
    rt.update(move |m, tx| m.sign_in_requested("pi", pi, tx));
    settle(&mut rt);
    let (x, y) = rt.find("open").unwrap();
    rt.pointer_down(x, y);
    rt.update(|m, tx| {
        m.connect_finished(
            "lab",
            Err(KamuiError::CommandFailed("timed out".into())),
            tx,
        )
    });
    rt.frame(1.0 / 60.0);
    rt.pointer_up(x, y);
    settle(&mut rt);
    assert_eq!(
        rt.state.sign_ins.len(),
        1,
        "pi's link still waits for its own yes"
    );
    assert!(rt.state.sign_ins[0].url.path().ends_with("/pi"));
}

#[test]
fn sign_in_links_queue_and_leave_with_their_connection() {
    let (mut m, _) = fixture();
    m.connect("lab");
    m.connect("pi");
    let mut rt = runtime(m);
    let lab = url::Url::parse("https://headscale.example.com/a/lab").unwrap();
    let pi = url::Url::parse("https://headscale.example.com/a/pi").unwrap();
    let (a, b) = (lab.clone(), pi.clone());
    rt.update(move |m, tx| m.sign_in_requested("lab", a, tx));
    rt.update(move |m, tx| m.sign_in_requested("pi", b, tx));
    assert_eq!(
        rt.state.sign_ins.len(),
        2,
        "the second doesn't replace the first"
    );
    rt.update(|m, tx| {
        m.connect_finished(
            "lab",
            Err(KamuiError::CommandFailed("timed out".into())),
            tx,
        )
    });
    assert_eq!(
        rt.state
            .sign_ins
            .iter()
            .map(|s| s.url.clone())
            .collect::<Vec<_>>(),
        vec![pi.clone()]
    );
    let lab_serial = rt.state.sign_ins[0].serial - 1;
    rt.update(move |m, tx| m.answer_sign_in(lab_serial, true, tx));
    assert_eq!(
        rt.state.sign_ins.len(),
        1,
        "an answer for a link that left does nothing"
    );
    let pi_serial = rt.state.sign_ins[0].serial;
    rt.update(move |m, tx| m.answer_sign_in(pi_serial, false, tx));
    assert!(rt.state.sign_ins.is_empty());
    let _ = lab;
}

#[test]
fn a_long_prompt_keeps_its_answer_field_and_buttons_in_the_window() {
    for scene_name in ["prompt-long", "sign-in-long"] {
        let mut m = scenes::fixture();
        let (_, scene) = scenes::SCENES
            .iter()
            .find(|(name, _)| *name == scene_name)
            .unwrap();
        scene(&mut m);
        let mut rt = runtime(m);
        settle(&mut rt);
        let keys: &[&str] = if scene_name == "prompt-long" {
            &["answer", "accept", "decline"]
        } else {
            &["sign-in-host", "open", "cancel"]
        };
        for key in keys {
            let (rect, _) = rt
                .presented(key)
                .unwrap_or_else(|| panic!("no {key} in {scene_name}"));
            assert!(
                rect[1] >= 0.0 && rect[1] + rect[3] <= SIZE.1 && rect[0] + rect[2] <= SIZE.0,
                "{key} at {rect:?} in {scene_name}"
            );
        }
    }
}

fn no_session() -> Result<(), KamuiError> {
    Err(KamuiError::CommandFailed("no Kamui session for pi".into()))
}

#[test]
fn stop_all_succeeding_does_not_hide_a_failed_cleanup() {
    let (mut m, _) = fixture();
    m.connect("pi");
    m.stop_all();
    let all = m.stop_serial;
    m.stop_finished(all, Ok(()));
    let mut rt = runtime(m);
    rt.update(|m, tx| m.connect_finished("pi", Ok(Vec::new()), tx));
    let cleanup = rt.state.cleanup["pi"];
    rt.state.stop_finished(
        cleanup,
        Err(KamuiError::CommandFailed("controller unavailable".into())),
    );
    assert_eq!(rt.state.notes.len(), 1, "pi is still running");
}

#[test]
fn connecting_again_to_a_pending_host_keeps_its_cancellation() {
    let (mut m, recorder) = fixture();
    m.connect("pi");
    m.stop("pi");
    let user = m.stop_serial;
    m.stop_finished(user, no_session());
    assert_eq!(m.connect("pi"), Some("pi".into()), "already connecting");
    assert!(m.stopping.contains("pi"), "the earlier Stop still applies");
    let mut rt = runtime(m);
    rt.update(|m, tx| m.connect_finished("pi", Ok(Vec::new()), tx));
    assert_eq!(
        recorder.calls().iter().filter(|c| *c == "stop pi").count(),
        2,
        "the session is stopped once it arrives"
    );
}

#[test]
fn stops_sent_while_connecting_leave_the_verdict_to_the_cleanup_stop() {
    // The user's stop fails or succeeds; either way the cleanup stop's
    // result decides, in either order.
    for (user_result, cleanup_first) in
        [(no_session(), false), (no_session(), true), (Ok(()), false)]
    {
        let (mut rt, _, user, cleanup) = stopped_while_connecting();
        if cleanup_first {
            rt.state.stop_finished(cleanup, Ok(()));
            rt.state.stop_finished(user, user_result.clone());
        } else {
            rt.state.stop_finished(user, user_result.clone());
            rt.state.stop_finished(cleanup, Ok(()));
        }
        assert!(
            rt.state.notes.is_empty(),
            "{user_result:?}, cleanup first: {cleanup_first}"
        );
    }

    // A stop that ended the old session doesn't vouch for the new one.
    let (mut rt, _, user, cleanup) = stopped_while_connecting();
    rt.state.stop_finished(user, Ok(()));
    rt.state.stop_finished(
        cleanup,
        Err(KamuiError::Launch("No such file or directory".into())),
    );
    assert_eq!(rt.state.notes.len(), 1, "the new session may still run");

    // Stop pressed again during cleanup.
    let (mut rt, _, user, cleanup) = stopped_while_connecting();
    rt.state.stop("pi");
    let again = rt.state.stop_serial;
    rt.state.stop_finished(user, no_session());
    rt.state.stop_finished(cleanup, Ok(()));
    rt.state.stop_finished(again, no_session());
    assert!(rt.state.notes.is_empty());
}

#[test]
fn a_stopped_connection_that_failed_is_stopped_again_in_either_order() {
    for stop_first in [true, false] {
        let (mut m, recorder) = fixture();
        m.connect("pi");
        m.stop("pi");
        let user = m.stop_serial;
        if stop_first {
            m.stop_finished(user, no_session());
        }
        let mut rt = runtime(m);
        rt.update(|m, tx| {
            m.connect_finished("pi", Err(KamuiError::CommandFailed("cancelled".into())), tx)
        });
        if !stop_first {
            rt.state.stop_finished(user, no_session());
        }
        assert_eq!(
            recorder.calls().iter().filter(|c| *c == "stop pi").count(),
            2,
            "a cleanup stop follows"
        );
        assert!(rt.state.is_busy("pi"), "until it reports");
        let cleanup = rt.state.cleanup["pi"];
        // kamui finds nothing to stop, which the client reports as success.
        rt.state.stop_finished(cleanup, Ok(()));
        assert!(rt.state.notes.is_empty(), "stop first: {stop_first}");
        assert!(!rt.state.is_busy("pi"));
    }
}

#[test]
fn a_failed_reconnect_whose_stop_failed_is_not_left_running_silently() {
    // A session that needs authentication is reconnected, the user cancels,
    // the stop can't run, and the reconnect fails, which keeps the session.
    let (mut m, _) = fixture();
    m.connect("lab");
    m.stop("lab");
    let user = m.stop_serial;
    m.stop_finished(
        user,
        Err(KamuiError::Launch("No such file or directory".into())),
    );
    let mut rt = runtime(m);
    rt.update(|m, tx| {
        m.connect_finished(
            "lab",
            Err(KamuiError::CommandFailed("authentication failed".into())),
            tx,
        )
    });
    let cleanup = rt.state.cleanup["lab"];
    rt.state.stop_finished(
        cleanup,
        Err(KamuiError::Launch("No such file or directory".into())),
    );
    assert_eq!(rt.state.notes.len(), 1, "the stop that decides is reported");
}

#[test]
fn a_plain_stop_that_fails_is_reported_at_once() {
    let (mut m, _) = fixture();
    m.stop("reyna");
    let id = m.stop_serial;
    m.stop_finished(
        id,
        Err(KamuiError::CommandFailed(
            "the Kamui controller is unavailable".into(),
        )),
    );
    assert_eq!(m.notes.len(), 1);
}
