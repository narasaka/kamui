//! The macOS host: runs the Rune window as an accessory (menu bar) app,
//! owns the status item, and does the model's blocking work on background
//! threads, reporting back with `AppHandle::update`.
//!
//! The app starts with no window. It opens one for first-run setup, for an
//! SSH prompt, and when the user asks; messages are toasts under the menu
//! bar unless the window is in front. While a window shows, the app has a
//! Dock icon so ⌘Tab reaches it; closing the window hides the icon again.
//! Quitting the app leaves sessions running, because they live in kamui's
//! controller.

mod native;
mod status_item;

use std::collections::HashMap;
use std::path::PathBuf;
use std::rc::Rc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use rune::{
    Align, App, AppHandle, Availability, Chrome, OverlayHandle, OverlayInput, OverlayOptions,
    OverlaySpec, ScreenEdge, Tx,
};
use rune_mac::notify;

use crate::core::prompt::{PromptHandler, PromptReply, PromptResponder, SshPrompt};
use crate::core::{KamuiClient, KamuiError, locator, ssh_config};
use crate::ui::model::{Backend, Model, POLL_INTERVAL, Snapshot};
use crate::ui::toast::{self, Toast, ToastState};

/// What background threads need to run kamui.
struct Context {
    environment: HashMap<String, String>,
    override_path: Option<String>,
    executable: Option<PathBuf>,
    bundled: Option<PathBuf>,
}

struct Shared {
    context: Mutex<Context>,
    /// The login shell's PATH and SSH agent have been read.
    ready: AtomicBool,
    refreshing: AtomicBool,
    /// A refresh was asked for while one ran; it runs again when done.
    again: AtomicBool,
}

impl Shared {
    fn client(&self) -> Option<KamuiClient> {
        let context = self.context.lock().unwrap();
        context
            .executable
            .clone()
            .map(|executable| KamuiClient::new(executable, context.environment.clone()))
    }
}

struct MacBackend {
    handle: AppHandle<Model>,
    shared: Arc<Shared>,
    toasts: OverlayHandle<ToastState>,
}

/// Runs `f` on the UI thread and brings the status item up to date.
fn update(handle: &AppHandle<Model>, f: impl FnOnce(&mut Model, &mut Tx) + Send + 'static) {
    handle.update(move |m, tx| {
        f(m, tx);
        status_item::sync(m);
    });
}

fn refresh(handle: &AppHandle<Model>, shared: &Arc<Shared>) {
    if !shared.ready.load(Ordering::SeqCst) {
        return;
    }
    if shared.refreshing.swap(true, Ordering::SeqCst) {
        // The running refresh may have read settings from before this call.
        shared.again.store(true, Ordering::SeqCst);
        return;
    }
    let (handle, shared) = (handle.clone(), shared.clone());
    std::thread::spawn(move || {
        let hosts = ssh_config::load_default();
        let executable = {
            let mut context = shared.context.lock().unwrap();
            let located = locator::locate(
                context.override_path.as_deref(),
                &context.environment,
                context.bundled.as_deref(),
                locator::is_executable,
            );
            context.executable = located.clone();
            located
        };
        // Without kamui there is nothing to ask, which is an answer: setup
        // starts there and offers to choose it.
        let (sessions, problem, answered) = match shared.client() {
            None => (
                Some(Vec::new()),
                Some(KamuiError::ExecutableNotFound.to_string()),
                true,
            ),
            Some(client) => match client.status() {
                Ok(document) => (Some(document.sessions), None, true),
                Err(error) => (None, Some(error.to_string()), false),
            },
        };
        shared.refreshing.store(false, Ordering::SeqCst);
        if shared.again.swap(false, Ordering::SeqCst) {
            refresh(&handle, &shared);
        }
        handle.update_if(move |m, _| {
            let snapshot = Snapshot {
                sessions,
                problem,
                executable,
                hosts,
                launches_at_login: native::launches_at_login(),
            };
            let first = !m.loaded;
            let changed = m.apply_if_changed(snapshot);
            let had_tour = m.tour.is_some();
            if m.decide_first_run(native::onboarded(), answered) {
                native::set_onboarded(true);
            }
            status_item::sync(m);
            changed || first || had_tour != m.tour.is_some()
        });
    });
}

/// Relays one connection's prompts to the window.
struct WindowPrompts {
    handle: AppHandle<Model>,
    destination: String,
}

impl PromptHandler for WindowPrompts {
    fn ask(&self, prompt: SshPrompt, responder: PromptResponder) {
        let destination = self.destination.clone();
        update(&self.handle, move |m, _| {
            m.prompt_arrived(&destination, prompt, responder)
        });
    }
    fn withdraw(&self, id: i64) {
        let destination = self.destination.clone();
        update(&self.handle, move |m, _| {
            m.prompt_withdrawn(&destination, id)
        });
    }
    fn notice(&self, text: &str) {
        let (destination, text) = (self.destination.clone(), text.to_owned());
        update(&self.handle, move |m, _| m.notice(&destination, &text));
    }
}

impl Backend for MacBackend {
    fn refresh(&self) {
        refresh(&self.handle, &self.shared);
    }

    fn connect(&self, destination: String) {
        let handle = self.handle.clone();
        let Some(client) = self.shared.client() else {
            // The model counts this destination as pending until it hears.
            update(&handle, move |m, tx| {
                m.connect_finished(&destination, Err(KamuiError::ExecutableNotFound), tx)
            });
            return;
        };
        std::thread::spawn(move || {
            let prompts = Arc::new(WindowPrompts {
                handle: handle.clone(),
                destination: destination.clone(),
            });
            let sign_in = handle.clone();
            let target = destination.clone();
            let result = client.connect(&destination, Some(prompts), move |url| {
                let target = target.clone();
                update(&sign_in, move |m, tx| m.sign_in_requested(&target, url, tx));
            });
            update(&handle, move |m, tx| {
                m.connect_finished(&destination, result, tx)
            });
        });
    }

    fn stop(&self, destination: String, id: u64) {
        let handle = self.handle.clone();
        let Some(client) = self.shared.client() else {
            update(&handle, move |m, _| {
                m.stop_finished(id, Err(KamuiError::ExecutableNotFound))
            });
            return;
        };
        std::thread::spawn(move || {
            let result = client.stop(&destination);
            update(&handle, move |m, _| m.stop_finished(id, result));
        });
    }

    fn stop_then_decline(&self, destination: String, responder: PromptResponder, id: u64) {
        let client = self.shared.client();
        let handle = self.handle.clone();
        std::thread::spawn(move || {
            let result = client.map_or(Err(KamuiError::ExecutableNotFound), |c| {
                c.stop(&destination)
            });
            responder.reply(PromptReply::Decline);
            update(&handle, move |m, _| m.stop_finished(id, result));
        });
    }

    fn stop_all(&self, id: u64) {
        let handle = self.handle.clone();
        let Some(client) = self.shared.client() else {
            update(&handle, move |m, _| {
                m.stop_finished(id, Err(KamuiError::ExecutableNotFound))
            });
            return;
        };
        std::thread::spawn(move || {
            let result = client.stop_all();
            update(&handle, move |m, _| m.stop_finished(id, result));
        });
    }

    fn set_launch_at_login(&self, enabled: bool) -> Result<bool, String> {
        native::set_launches_at_login(enabled)
    }

    fn save_executable_override(&self, path: Option<String>) {
        native::set_executable_override(path.as_deref());
        // Commands use the new choice from now on, not after the next poll.
        let mut context = self.shared.context.lock().unwrap();
        context.executable = locator::locate(
            path.as_deref(),
            &context.environment,
            context.bundled.as_deref(),
            locator::is_executable,
        );
        context.override_path = path;
    }

    fn save_onboarded(&self, done: bool) {
        native::set_onboarded(done);
    }

    fn show_window(&self) {
        self.handle.set_dock_icon(true);
        self.handle.open();
    }

    fn window_in_front(&self) -> bool {
        self.handle.is_visible() && notify::app_focused()
    }

    fn toast(&self, toast: Toast) -> bool {
        if self.toasts.availability() == Availability::Unavailable {
            return false;
        }
        // Under the menu bar and the notch.
        let margin = native::top_inset() as f32 + 8.0 - toast::GUTTER;
        self.toasts.place(ScreenEdge::Top, margin.max(0.0));
        // Shown from the same update as the toast, so a hide that the last
        // toast's countdown asked for in the meantime can't land after it.
        let overlay = self.toasts.clone();
        self.toasts.update(move |s, _| {
            s.show(toast);
            overlay.fade_in(TOAST_FADE);
        });
        true
    }

    fn window_closed(&self) {
        self.handle.set_dock_icon(false);
    }

    fn quit(&self) {
        self.handle.quit();
    }
}

fn poll(m: &mut Model, now: f64, tx: &mut Tx) -> bool {
    let changed = m.poll(now, tx);
    status_item::sync(m);
    changed
}

/// Closing the window keeps the app in the menu bar, without a Dock icon.
fn on_close(m: &mut Model, _: &mut Tx) {
    m.window_closed();
}

/// Opening Kamui again from Finder, Spotlight or the Dock shows the window,
/// which an accessory app would not do by itself.
fn on_reopen(m: &mut Model, _: bool, _: &mut Tx) -> bool {
    m.backend.show_window();
    true
}

const TOAST_FADE: Duration = Duration::from_millis(180);

/// The toast's overlay, at the top centre of the main display. Only the card
/// takes clicks; one opens the window on the toast's session and messages.
fn toast_overlay(app: &mut App<Model>, handle: &AppHandle<Model>) -> OverlayHandle<ToastState> {
    let spec = OverlaySpec {
        align: Align::Center,
        ..OverlaySpec::new("Kamui", toast::SIZE, toast::view)
    };
    let toasts = app.overlay(
        spec.options(OverlayOptions {
            input: OverlayInput::Regions,
            // Made at launch, so a toast that can't show is known before
            // one is needed and the window shows the message instead.
            prewarm: true,
            ..OverlayOptions::default()
        })
        .poll(toast::poll),
        ToastState::default(),
    );
    let (overlay, handle) = (toasts.clone(), handle.clone());
    toasts.update(move |s, _| {
        s.on_dismiss = Some(Box::new(move || overlay.fade_out(TOAST_FADE)));
        s.on_open = Some(Box::new(move |toasts| {
            update(&handle, move |m, _| m.toast_clicked(toasts))
        }));
    });
    toasts
}

pub fn run() {
    rune_mac::launch();
    let bundled = std::env::current_exe()
        .ok()
        .and_then(|exe| exe.ancestors().nth(3).map(locator::bundled_executable))
        .filter(|path| locator::is_executable(path));
    let override_path = native::executable_override();
    let shared = Arc::new(Shared {
        context: Mutex::new(Context {
            environment: std::env::vars().collect(),
            override_path: override_path.clone(),
            executable: None,
            bundled: bundled.clone(),
        }),
        ready: AtomicBool::new(false),
        refreshing: AtomicBool::new(false),
        again: AtomicBool::new(false),
    });

    let mut spec = crate::ui::spec();
    spec.poll = Some(poll);
    let mut app = App::new(spec)
        .args(Vec::new())
        .accessory()
        .windowless()
        .keep_running()
        .standard_menus()
        .chrome(Chrome {
            full_size_content: true,
            traffic_lights: Some((18.0, 18.0)),
            drag_regions: true,
        })
        .on_close(on_close)
        .on_reopen(on_reopen);
    let handle = app.handle();
    let toasts = toast_overlay(&mut app, &handle);
    let backend_handle = handle.clone();
    let backend_shared = shared.clone();
    let app = app.init(move || {
        let backend = Rc::new(MacBackend {
            handle: backend_handle,
            shared: backend_shared,
            toasts,
        });
        let mut m = Model::new(backend);
        m.bundled = bundled;
        m.executable_override = override_path;
        m.launches_at_login = native::launches_at_login();
        m.version = native::version().unwrap_or(m.version);
        m
    });

    // `on_ready` waits for a native window, which a menu bar app may never
    // open, so the status item and the poller start with the first update
    // the event loop runs.
    let ready_handle = handle.clone();
    handle.update_if(move |m, _| {
        status_item::install(ready_handle.clone());
        status_item::sync(m);
        // Read the login shell's environment off the main thread, then poll.
        let (handle, shared) = (ready_handle, shared);
        std::thread::spawn(move || {
            let base = shared.context.lock().unwrap().environment.clone();
            let environment =
                locator::login_environment(&crate::core::runner::ProcessRunner, &base);
            shared.context.lock().unwrap().environment = environment;
            shared.ready.store(true, Ordering::SeqCst);
            loop {
                refresh(&handle, &shared);
                std::thread::sleep(POLL_INTERVAL);
            }
        });
        false
    });
    app.run();
}
