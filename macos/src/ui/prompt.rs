//! OpenSSH's questions, and the window's other messages.
//!
//! A prompt always names the host it comes from, because a password dialog
//! is a target for spoofing. Typed answers always use a secure field: prompt
//! text comes partly from the server, so it never decides whether input is
//! shown. Answers live only in memory until they are written to kamui's
//! standard input, and the field is cleared when the prompt ends.

use rune::{El, col, row, text};
use rune_kit::{Button, ButtonVariant, Dialog, TextInput};

use super::model::{Model, PendingPrompt};
use super::theme::{DESCRIPTION, Paint};
use crate::core::prompt::PromptKind;

pub fn title(prompt: &PendingPrompt) -> String {
    match prompt.prompt.kind {
        PromptKind::Confirm => format!("{} asks for permission", prompt.destination),
        _ => format!("SSH needs your answer to connect to {}", prompt.destination),
    }
}

/// Prompt text longer than this scrolls, so the answer field and buttons
/// stay in the window whatever a server sends.
fn is_long(text: &str) -> bool {
    text.lines().count() > 6 || text.chars().count() > 360
}

/// The prompt's text, in a scroll view when it is long.
fn prompt_text(text_el: El, text: &str, height: f32) -> El {
    if is_long(text) {
        col()
            .key("text-scroll")
            .w_full()
            .h(height)
            .scroll(rune::ScrollSpec {
                stick_bottom: false,
            })
            .scroll_edge_fade(10.0)
            .child(text_el)
    } else {
        text_el
    }
}

/// The answer field and the two buttons, shared by the dialog and the
/// tour's inline card. Every handler names its prompt by serial, so a press
/// that began on a prompt kamui then withdrew can't answer the next one.
fn controls(p: Paint, m: &Model, prompt: &PendingPrompt) -> El {
    let serial = prompt.serial;
    let (accept, decline) = match prompt.prompt.kind {
        PromptKind::Confirm => ("Allow", "Deny"),
        _ => ("Continue", "Cancel"),
    };
    let buttons = row()
        .key("buttons")
        .w_full()
        .gap(8.0)
        .justify_end()
        .child(
            Button::new(decline)
                .variant(ButtonVariant::Neutral)
                .on_press(move |m: &mut Model| m.decline_prompt(serial))
                .build(p.k)
                .key("decline"),
        )
        .child(
            Button::new(accept)
                .variant(ButtonVariant::Contrast)
                .on_press(move |m: &mut Model| m.answer_prompt(serial))
                .build(p.k)
                .key("accept")
                .default_button(),
        );
    let mut body = col().key(format!("controls-{serial}")).w_full().gap(12.0);
    if prompt.prompt.kind != PromptKind::Confirm {
        body = body.child(
            TextInput::new(&m.answer)
                .secure()
                .label(format!("Answer for {}", prompt.destination))
                .width(None)
                .on_edit(|m: &mut Model| &mut m.answer)
                .on_submit(move |m: &mut Model, _| m.answer_prompt(serial))
                .build(p.k)
                .key("answer"),
        );
    }
    body.child(buttons)
}

/// The prompt as a card under the tour's host picker.
pub fn inline_prompt(p: Paint, m: &Model, prompt: &PendingPrompt) -> El {
    let body = prompt.prompt.text.trim();
    p.raised("prompt-card")
        .w_full()
        .padding(16.0)
        .gap(12.0)
        .role("group")
        .label(title(prompt))
        .child(
            col()
                .key("copy")
                .gap(4.0)
                .child(
                    text(title(prompt))
                        .key("title")
                        .font(p.k.ty.ui_medium)
                        .color(p.text())
                        .wrap(),
                )
                .child(prompt_text(
                    text(body)
                        .key("text")
                        .font(DESCRIPTION)
                        .color(p.muted())
                        .wrap()
                        .selectable(true),
                    body,
                    96.0,
                )),
        )
        .child(controls(p, m, prompt))
}

/// Everything that floats over the window: the front prompt (unless the
/// tour shows it inline), a sign-in link to confirm, and messages.
pub fn layer(p: Paint, m: &Model, inline: bool, layer: &mut Vec<El>) {
    if let Some(prompt) = m.front_prompt().filter(|_| !inline) {
        let serial = prompt.serial;
        let body = prompt.prompt.text.trim();
        Dialog::new(format!("prompt-{serial}"), title(prompt))
            .open(true)
            .fit()
            .width(460.0)
            .click_outside(false)
            .child(
                col()
                    .key("prompt-body")
                    .w_full()
                    .px(16.0)
                    .pb(16.0)
                    .gap(14.0)
                    .child(prompt_text(
                        text(body)
                            .key("text")
                            .font(p.k.ty.ui.lh(20.0))
                            .color(p.muted())
                            .wrap()
                            .selectable(true),
                        body,
                        200.0,
                    ))
                    .child(controls(p, m, prompt)),
            )
            .on_close(move |m: &mut Model| m.decline_prompt(serial))
            .build(p.k, layer);
        return;
    }
    if let Some(sign_in) = m.sign_ins.first() {
        let serial = sign_in.serial;
        // The server chooses the link, so its host shows on its own, whole:
        // breaks after each dot let a long one wrap rather than push its
        // real domain out of view.
        let host = sign_in
            .url
            .host_str()
            .unwrap_or_default()
            .replace('.', ".\u{200B}");
        let buttons = row()
            .key("buttons")
            .w_full()
            .gap(8.0)
            .justify_end()
            .child(
                Button::new("Cancel")
                    .variant(ButtonVariant::Neutral)
                    .on_press_tx(move |m: &mut Model, tx| m.answer_sign_in(serial, false, tx))
                    .build(p.k)
                    .key("cancel"),
            )
            .child(
                Button::new("Open link")
                    .variant(ButtonVariant::Contrast)
                    .on_press_tx(move |m: &mut Model, tx| m.answer_sign_in(serial, true, tx))
                    .build(p.k)
                    .key("open"),
            );
        Dialog::new(
            format!("sign-in-{serial}"),
            format!("Sign in to connect to {}?", sign_in.destination),
        )
        .open(true)
        .description("Open it only if you expect this host to ask for a sign-in.")
        .fit()
        .width(440.0)
        .child(
            col()
                .key(format!("sign-in-body-{serial}"))
                .w_full()
                .px(16.0)
                .pb(16.0)
                .gap(10.0)
                .child(
                    col()
                        .key("host")
                        .w_full()
                        .gap(2.0)
                        .child(
                            text("Opens a page on")
                                .key("label")
                                .font(DESCRIPTION)
                                .color(p.muted()),
                        )
                        .child(
                            text(host)
                                .key("sign-in-host")
                                .w_full()
                                .font(p.k.ty.ui_medium)
                                .color(p.strong())
                                .wrap()
                                .wrap_anywhere(),
                        ),
                )
                .child(
                    col().key("link-box").w_full().clip_soft(0.0).child(
                        text(sign_in.url.as_str())
                            .key("link")
                            .w_full()
                            .font(p.k.ty.mono)
                            .color(p.muted())
                            .wrap()
                            .max_lines(4)
                            .selectable(true),
                    ),
                )
                .child(buttons),
        )
        .on_close(move |m: &mut Model| m.sign_ins.retain(|s| s.serial != serial))
        .build(p.k, layer);
        return;
    }
    if let Some(note) = m.notes.first() {
        Dialog::new("note", &note.title)
            .open(true)
            .fit()
            .width(460.0)
            .child(
                col()
                    .key("note-body")
                    .w_full()
                    .px(16.0)
                    .pb(16.0)
                    .gap(14.0)
                    .child(
                        text(&note.detail)
                            .key("detail")
                            .font(p.k.ty.ui)
                            .color(p.muted())
                            .wrap()
                            .selectable(true),
                    )
                    .child(
                        row().key("buttons").w_full().justify_end().child(
                            Button::new("OK")
                                .variant(ButtonVariant::Contrast)
                                .on_press(Model::dismiss_note)
                                .build(p.k)
                                .key("ok")
                                .default_button(),
                        ),
                    ),
            )
            .on_close(Model::dismiss_note)
            .build(p.k, layer);
    }
}
