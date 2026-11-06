//! Kamui's vortex, drawn from [`crate::core::glyph`]. It turns a third of a
//! turn on [`TURN`](super::theme::TURN) whenever `turns` grows; three-fold
//! symmetry lands it on the same picture, so each step of setup feels like a
//! dial clicking forward.
//!
//! The mark is a bitmap rasterized at the window's pixel scale
//! ([`glyph::coverage`]) rather than a triangulated shape, which breaks its
//! thin arms apart at sidebar sizes.

use std::cell::RefCell;
use std::collections::HashMap;

use rune::{Anim, Color, El, Pixels, el};

use super::theme::Paint;
use crate::core::glyph;

thread_local! {
    /// Rasterized marks by pixel size and colour. A window shows a handful.
    static MARKS: RefCell<HashMap<(u32, String), Pixels>> = RefCell::new(HashMap::new());
}

fn pixels(px: u32, color: Color) -> Pixels {
    let hex = color.hex();
    MARKS.with(|marks| {
        marks
            .borrow_mut()
            .entry((px, hex.clone()))
            .or_insert_with(|| {
                let channel = |i: usize| u8::from_str_radix(&hex[i..i + 2], 16).unwrap_or(0);
                let (r, g, b, a) = (channel(1), channel(3), channel(5), f32::from(channel(7)));
                let rgba = glyph::coverage(px)
                    .into_iter()
                    .flat_map(|c| [r, g, b, (c * a).round() as u8])
                    .collect();
                let image = Pixels::new();
                image.set(px, px, rgba);
                image
            })
            .clone()
    })
}

pub fn vortex(p: Paint, key: &str, size: f32, color: Color, turns: u32) -> El {
    let scale = 1.0 / p.hair;
    let px = (size * scale).round().max(1.0) as u32;
    el().key(key)
        .size(size, size)
        .pixels(&pixels(px, color))
        .rotate(turns as f32 * std::f32::consts::TAU / 3.0)
        .rotate_motion(Anim::from(super::theme::TURN))
        .a11y_hidden()
}
