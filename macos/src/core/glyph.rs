//! Geometry of Kamui's three-armed vortex mark in a 100 × 100 box with the
//! origin at the top left. The menu bar icon and the window's mark draw it;
//! the app icon in Resources/AppIcon.icns uses the same spiral.

pub const SIZE: f64 = 100.0;
const CENTER: (f64, f64) = (50.0, 50.0);

/// The three arms, each a closed polygon: outer edge from rim to core, then
/// the inner edge back out.
pub fn arms(steps: usize) -> Vec<Vec<(f64, f64)>> {
    [0.0, 120.0, 240.0]
        .iter()
        .map(|&rotation| arm(rotation, steps))
        .collect()
}

/// One logarithmic spiral band from the rim to the core. It tapers to a point
/// at the rim and thins toward the core.
fn arm(rotation: f64, steps: usize) -> Vec<(f64, f64)> {
    let (turns, outer_radius, inner_radius, outer_width, inner_width): (f64, f64, f64, f64, f64) =
        (1.15, 44.0, 6.0, 13.0, 2.5);
    let mut outer = Vec::with_capacity(steps + 1);
    let mut inner = Vec::with_capacity(steps + 1);
    for step in 0..=steps {
        let f = step as f64 / steps as f64;
        let radius = outer_radius * (inner_radius / outer_radius).powf(f);
        let width = (outer_width * (1.0 - f) + inner_width * f) * smoothstep(f / 0.14);
        let angle = (rotation + f * turns * 360.0 - 90.0).to_radians();
        outer.push(point(radius + width / 2.0, angle));
        inner.push(point((radius - width / 2.0).max(0.2), angle));
    }
    inner.reverse();
    outer.extend(inner);
    outer
}

fn point(radius: f64, angle: f64) -> (f64, f64) {
    (
        CENTER.0 + radius * angle.cos(),
        CENTER.1 + radius * angle.sin(),
    )
}

fn smoothstep(x: f64) -> f64 {
    let t = x.clamp(0.0, 1.0);
    t * t * (3.0 - 2.0 * t)
}

/// The mark's coverage in a `px` × `px` square, row by row, from 0 (empty)
/// to 1 (covered).
///
/// Small marks need this: a triangulated fill of a spiral whose arms taper to
/// a fraction of a pixel breaks into slivers. Each pixel row is sampled on
/// [`SUBROWS`] scanlines, and each scanline's spans add their exact
/// horizontal overlap, so thin arms fade instead of vanishing.
pub fn coverage(px: u32) -> Vec<f32> {
    const SUBROWS: u32 = 5;
    let size = px as usize;
    let mut cover = vec![0.0f32; size * size];
    if px == 0 {
        return cover;
    }
    let to_px = f64::from(px) / SIZE;
    let arms: Vec<Vec<(f64, f64)>> = arms(160)
        .into_iter()
        .map(|arm| {
            arm.into_iter()
                .map(|(x, y)| (x * to_px, y * to_px))
                .collect()
        })
        .collect();
    let mut crossings = Vec::new();
    for y in 0..size {
        let row = &mut cover[y * size..(y + 1) * size];
        for sub in 0..SUBROWS {
            let sy = y as f64 + (f64::from(sub) + 0.5) / f64::from(SUBROWS);
            for arm in &arms {
                crossings.clear();
                for (i, &(x0, y0)) in arm.iter().enumerate() {
                    let (x1, y1) = arm[(i + 1) % arm.len()];
                    if (y0 <= sy) != (y1 <= sy) {
                        crossings.push(x0 + (sy - y0) / (y1 - y0) * (x1 - x0));
                    }
                }
                crossings.sort_by(f64::total_cmp);
                for [start, end] in crossings.as_chunks::<2>().0 {
                    let (a, b) = (start.max(0.0), end.min(size as f64));
                    if b <= a {
                        continue;
                    }
                    for (x, cell) in row
                        .iter_mut()
                        .enumerate()
                        .take(b.ceil() as usize)
                        .skip(a.floor() as usize)
                    {
                        let overlap = b.min(x as f64 + 1.0) - a.max(x as f64);
                        *cell += (overlap / f64::from(SUBROWS)) as f32;
                    }
                }
            }
        }
    }
    cover.iter_mut().for_each(|c| *c = c.clamp(0.0, 1.0));
    cover
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn three_closed_arms_stay_inside_the_box() {
        let arms = arms(70);
        assert_eq!(arms.len(), 3);
        for arm in &arms {
            assert_eq!(arm.len(), 142, "outer and inner edges with 71 samples each");
            for &(x, y) in arm {
                assert!(
                    (0.0..=SIZE).contains(&x) && (0.0..=SIZE).contains(&y),
                    "({x}, {y})"
                );
            }
        }
    }

    #[test]
    fn arms_are_rotationally_symmetric() {
        let arms = arms(70);
        let angle = 120f64.to_radians();
        for (first, second) in arms[0].iter().zip(&arms[1]) {
            let (x, y) = (first.0 - 50.0, first.1 - 50.0);
            let rotated = (
                50.0 + x * angle.cos() - y * angle.sin(),
                50.0 + x * angle.sin() + y * angle.cos(),
            );
            assert!((rotated.0 - second.0).abs() < 0.001 && (rotated.1 - second.1).abs() < 0.001);
        }
    }

    #[test]
    fn arm_tapers_to_a_point_at_the_rim() {
        let arm = &arms(70)[0];
        let (outer, inner) = (arm[0], arm[arm.len() - 1]);
        assert!((outer.0 - inner.0).abs() < 0.001 && (outer.1 - inner.1).abs() < 0.001);
    }

    #[test]
    fn coverage_fills_the_arms_and_leaves_the_corners_empty() {
        let px = 28;
        let cover = coverage(px);
        assert_eq!(cover.len(), (px * px) as usize);
        assert!(cover.iter().all(|c| (0.0..=1.0).contains(c)));
        assert_eq!(cover[0], 0.0, "the corner is outside the spiral");
        let total: f32 = cover.iter().sum();
        let area = total / (px * px) as f32;
        assert!(
            (0.2..0.6).contains(&area),
            "about a third of the square is ink, got {area}"
        );
        assert!(
            cover.iter().filter(|c| **c > 0.0 && **c < 1.0).count() > 20,
            "edges are antialiased"
        );
    }

    #[test]
    fn coverage_is_the_same_ink_at_any_size() {
        let small: f32 = coverage(20).iter().sum::<f32>() / 400.0;
        let large: f32 = coverage(200).iter().sum::<f32>() / 40_000.0;
        assert!((small - large).abs() < 0.02, "{small} vs {large}");
    }
}
