//! The window's remembered size and position.
//!
//! Everything is in physical pixels, as Tauri reports them. The saved rectangle is checked
//! against the monitors that exist now, so a window never opens off screen after a monitor was
//! unplugged or the resolution changed. The decision is a pure function (`place`) so it can be
//! tested without a window; `lib.rs` applies it with Tauri's own window API (no plugin).

use serde::{Deserialize, Serialize};
use serde_json::Value;

pub const WINDOW_FILE: &str = "window.json";
/// Kept in step with `minWidth` and `minHeight` in tauri.conf.json (logical pixels).
pub const MIN_LOGICAL: (u32, u32) = (480, 360);
/// A file claiming more than this is damaged, not a window.
const SANE_LIMIT: u32 = 100_000;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Rect {
    pub x: i32,
    pub y: i32,
    pub width: u32,
    pub height: u32,
}

/// What is stored: the window's normal (not maximized) rectangle, and whether to maximize it.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct WindowState {
    pub x: i32,
    pub y: i32,
    pub width: u32,
    pub height: u32,
    pub maximized: bool,
}

impl WindowState {
    pub fn rect(&self) -> Rect {
        Rect {
            x: self.x,
            y: self.y,
            width: self.width,
            height: self.height,
        }
    }
}

/// A monitor's usable area (without the taskbar) and its scale factor.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Area {
    pub work: Rect,
    pub scale: f64,
}

/// Reads a stored state; None when the file is damaged or the numbers are not a window.
pub fn parse(bytes: &[u8]) -> Option<WindowState> {
    let value: Value = serde_json::from_slice(bytes).ok()?;
    let state: WindowState = serde_json::from_value(value).ok()?;
    (state.width > 0 && state.height > 0 && state.width <= SANE_LIMIT && state.height <= SANE_LIMIT).then_some(state)
}

pub fn serialize(state: &WindowState) -> Vec<u8> {
    serde_json::to_vec(state).unwrap_or_default()
}

fn overlap(a: &Rect, b: &Rect) -> u64 {
    let left = i64::from(a.x).max(i64::from(b.x));
    let top = i64::from(a.y).max(i64::from(b.y));
    let right = (i64::from(a.x) + i64::from(a.width)).min(i64::from(b.x) + i64::from(b.width));
    let bottom = (i64::from(a.y) + i64::from(a.height)).min(i64::from(b.y) + i64::from(b.height));
    if right <= left || bottom <= top {
        0
    } else {
        ((right - left) as u64) * ((bottom - top) as u64)
    }
}

/// Where the window should open: the saved rectangle moved and shrunk to lie wholly inside the
/// work area of the monitor it overlaps most. With no overlap (its monitor is gone) it goes to the
/// first monitor, which the caller makes the primary one. None only when no monitor is known, and
/// the window then keeps the operating system's own placement.
pub fn place(saved: &Rect, monitors: &[Area], min_logical: (u32, u32)) -> Option<Rect> {
    let mut best: Option<(&Area, u64)> = None;
    for area in monitors {
        let covered = overlap(saved, &area.work);
        if best.is_none_or(|(_, most)| covered > most) {
            best = Some((area, covered));
        }
    }
    let (area, _) = best?;
    let work = area.work;
    let scale = if area.scale.is_finite() && area.scale > 0.0 {
        area.scale
    } else {
        1.0
    };
    let min_w = ((f64::from(min_logical.0) * scale).round() as u32).min(work.width);
    let min_h = ((f64::from(min_logical.1) * scale).round() as u32).min(work.height);
    let width = saved.width.clamp(min_w, work.width);
    let height = saved.height.clamp(min_h, work.height);
    let room_x = i64::from(work.width - width);
    let room_y = i64::from(work.height - height);
    let x = (i64::from(saved.x) - i64::from(work.x)).clamp(0, room_x) + i64::from(work.x);
    let y = (i64::from(saved.y) - i64::from(work.y)).clamp(0, room_y) + i64::from(work.y);
    Some(Rect {
        x: x as i32,
        y: y as i32,
        width,
        height,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    const fn rect(x: i32, y: i32, width: u32, height: u32) -> Rect {
        Rect { x, y, width, height }
    }
    const fn area(x: i32, y: i32, width: u32, height: u32, scale: f64) -> Area {
        Area {
            work: rect(x, y, width, height),
            scale,
        }
    }
    const MIN: (u32, u32) = (480, 360);
    const MAIN: Area = area(0, 0, 1920, 1040, 1.0);

    #[test]
    fn a_window_that_fits_stays_where_it_was() {
        let saved = rect(100, 80, 1320, 860);
        assert_eq!(place(&saved, &[MAIN], MIN), Some(saved));
    }

    #[test]
    fn a_window_partly_off_screen_is_pulled_back_in() {
        assert_eq!(
            place(&rect(1500, 700, 800, 600), &[MAIN], MIN),
            Some(rect(1120, 440, 800, 600))
        );
        assert_eq!(
            place(&rect(-300, -50, 800, 600), &[MAIN], MIN),
            Some(rect(0, 0, 800, 600))
        );
    }

    #[test]
    fn a_window_larger_than_the_screen_is_shrunk_to_the_work_area() {
        assert_eq!(
            place(&rect(-20, -20, 5000, 4000), &[MAIN], MIN),
            Some(rect(0, 0, 1920, 1040))
        );
    }

    #[test]
    fn a_window_too_small_is_grown_to_the_minimum_at_the_monitor_scale() {
        let sharp = area(0, 0, 3840, 2080, 2.0);
        assert_eq!(
            place(&rect(10, 10, 100, 100), &[sharp], MIN),
            Some(rect(10, 10, 960, 720))
        );
    }

    #[test]
    fn a_window_on_an_unplugged_monitor_opens_on_the_first_one() {
        let saved = rect(2100, 100, 1000, 700);
        assert_eq!(place(&saved, &[MAIN], MIN), Some(rect(920, 100, 1000, 700)));
        let far = rect(-9000, 12000, 1000, 700);
        assert_eq!(place(&far, &[MAIN], MIN), Some(rect(0, 340, 1000, 700)));
    }

    #[test]
    fn the_monitor_with_most_of_the_window_wins() {
        let left = area(-1920, 0, 1920, 1040, 1.0);
        // Two thirds on the left monitor: it stays there, pulled just inside its right edge.
        let saved = rect(-1000, 100, 1500, 700);
        assert_eq!(place(&saved, &[MAIN, left], MIN), Some(rect(-1500, 100, 1500, 700)));
        let mostly_main = rect(-200, 100, 1500, 700);
        assert_eq!(place(&mostly_main, &[left, MAIN], MIN), Some(rect(0, 100, 1500, 700)));
    }

    #[test]
    fn a_second_monitor_above_or_below_is_respected() {
        let above = area(0, -1080, 1920, 1040, 1.0);
        let saved = rect(200, -900, 1000, 600);
        assert_eq!(place(&saved, &[MAIN, above], MIN), Some(saved));
    }

    #[test]
    fn with_no_monitor_known_nothing_is_decided() {
        assert_eq!(place(&rect(0, 0, 800, 600), &[], MIN), None);
    }

    #[test]
    fn a_work_area_smaller_than_the_minimum_wins_over_it() {
        let tiny = area(0, 0, 400, 300, 1.0);
        assert_eq!(place(&rect(0, 0, 800, 600), &[tiny], MIN), Some(rect(0, 0, 400, 300)));
    }

    #[test]
    fn a_nonsense_scale_factor_falls_back_to_one() {
        let odd = area(0, 0, 1920, 1040, f64::NAN);
        assert_eq!(place(&rect(0, 0, 100, 100), &[odd], MIN), Some(rect(0, 0, 480, 360)));
    }

    #[test]
    fn the_state_round_trips_and_a_damaged_file_is_ignored() {
        let state = WindowState {
            x: -20,
            y: 40,
            width: 1320,
            height: 860,
            maximized: true,
        };
        assert_eq!(parse(&serialize(&state)), Some(state));
        assert_eq!(parse(b""), None);
        assert_eq!(parse(b"{}"), None);
        assert_eq!(parse(b"[1,2]"), None);
        assert_eq!(
            parse(br#"{"x":0,"y":0,"width":0,"height":600,"maximized":false}"#),
            None
        );
        assert_eq!(
            parse(br#"{"x":0,"y":0,"width":4000000000,"height":600,"maximized":false}"#),
            None
        );
        assert_eq!(
            parse(br#"{"x":"a","y":0,"width":800,"height":600,"maximized":false}"#),
            None
        );
    }
}
