//! The menu bar item: the vortex icon and a native menu built from
//! [`crate::ui::menu`]. It lives on the main thread for the app's life.

use std::cell::RefCell;

use block2::RcBlock;
use objc2::rc::Retained;
use objc2::runtime::{Bool, NSObjectProtocol};
use objc2::{MainThreadOnly, define_class, msg_send, sel};
use objc2_app_kit::{
    NSBezierPath, NSColor, NSCompositingOperation, NSControlStateValueOff, NSControlStateValueOn,
    NSEventModifierFlags, NSGraphicsContext, NSImage, NSMenu, NSMenuItem, NSStatusBar,
    NSStatusItem,
};
use objc2_foundation::{MainThreadMarker, NSObject, NSPoint, NSRect, NSSize, NSString};
use rune::AppHandle;

use crate::core::{Indicator, glyph};
use crate::ui::menu::{self, Action, Item};
use crate::ui::model::Model;

const POINT_SIZE: f64 = 18.0;

struct Status {
    item: Retained<NSStatusItem>,
    menu: Retained<NSMenu>,
    target: Retained<MenuTarget>,
    handle: AppHandle<Model>,
    indicator: Option<Indicator>,
    items: Vec<Item>,
    /// The action of each item that has one, by its tag.
    actions: Vec<Action>,
}

thread_local! {
    static STATUS: RefCell<Option<Status>> = const { RefCell::new(None) };
}

define_class!(
    #[unsafe(super = NSObject)]
    #[thread_kind = MainThreadOnly]
    #[name = "KamuiMenuTarget"]
    struct MenuTarget;

    unsafe impl NSObjectProtocol for MenuTarget {}

    impl MenuTarget {
        #[unsafe(method(pick:))]
        fn pick(&self, sender: &NSMenuItem) {
            let picked = STATUS.with(|status| {
                let status = status.borrow();
                let status = status.as_ref()?;
                let action = usize::try_from(sender.tag()).ok().and_then(|tag| status.actions.get(tag).cloned())?;
                Some((status.handle.clone(), action))
            });
            if let Some((handle, action)) = picked {
                handle.update(move |m, tx| {
                    menu::pick(m, action, tx);
                    sync(m);
                });
            }
        }
    }
);

impl MenuTarget {
    fn new(mtm: MainThreadMarker) -> Retained<Self> {
        let this = Self::alloc(mtm).set_ivars(());
        // SAFETY: NSObject's init takes no arguments.
        unsafe { msg_send![super(this), init] }
    }
}

pub fn install(handle: AppHandle<Model>) {
    let Some(mtm) = MainThreadMarker::new() else {
        return;
    };
    let target = MenuTarget::new(mtm);
    let menu = NSMenu::initWithTitle(NSMenu::alloc(mtm), &NSString::from_str("Kamui"));
    menu.setAutoenablesItems(false);
    let item = NSStatusBar::systemStatusBar().statusItemWithLength(-1.0);
    if let Some(button) = item.button(mtm) {
        button.setImage(Some(&icon(Indicator::Idle)));
    }
    item.setMenu(Some(&menu));
    STATUS.with(|status| {
        *status.borrow_mut() = Some(Status {
            item,
            menu,
            target,
            handle,
            indicator: Some(Indicator::Idle),
            items: Vec::new(),
            actions: Vec::new(),
        });
    });
}

/// Brings the icon and the menu up to date with the model. Cheap when
/// nothing changed, so it runs on every frame and update.
pub fn sync(m: &Model) {
    let Some(mtm) = MainThreadMarker::new() else {
        return;
    };
    STATUS.with(|status| {
        let mut status = status.borrow_mut();
        let Some(status) = status.as_mut() else {
            return;
        };
        let indicator = menu::indicator(m);
        if status.indicator != Some(indicator) {
            status.indicator = Some(indicator);
            if let Some(button) = status.item.button(mtm) {
                button.setImage(Some(&icon(indicator)));
            }
        }
        let items = menu::items(m);
        if items != status.items {
            let mut actions = Vec::new();
            status.menu.removeAllItems();
            fill(&status.menu, &items, &status.target, &mut actions, mtm);
            status.items = items;
            status.actions = actions;
        }
    });
}

fn fill(
    menu: &NSMenu,
    items: &[Item],
    target: &MenuTarget,
    actions: &mut Vec<Action>,
    mtm: MainThreadMarker,
) {
    for item in items {
        let native = match item {
            Item::Separator => NSMenuItem::separatorItem(mtm),
            Item::Label(title) => {
                let native = plain(title, mtm);
                native.setEnabled(false);
                native
            }
            Item::Action {
                title,
                action,
                enabled,
                checked,
                key,
            } => {
                // SAFETY: the selector names MenuTarget's `pick:` method, and
                // the target outlives the menu (both live in STATUS).
                let native = unsafe {
                    NSMenuItem::initWithTitle_action_keyEquivalent(
                        NSMenuItem::alloc(mtm),
                        &NSString::from_str(title),
                        Some(sel!(pick:)),
                        &NSString::from_str(key.unwrap_or("")),
                    )
                };
                if key.is_some() {
                    native.setKeyEquivalentModifierMask(NSEventModifierFlags::Command);
                }
                // SAFETY: as above.
                unsafe { native.setTarget(Some(target)) };
                native.setTag(actions.len() as isize);
                actions.push(action.clone());
                native.setEnabled(*enabled);
                native.setState(if *checked {
                    NSControlStateValueOn
                } else {
                    NSControlStateValueOff
                });
                native
            }
            Item::Submenu {
                title,
                symbol,
                items,
            } => {
                let native = plain(title, mtm);
                let submenu = NSMenu::initWithTitle(NSMenu::alloc(mtm), &NSString::from_str(title));
                submenu.setAutoenablesItems(false);
                fill(&submenu, items, target, actions, mtm);
                native.setSubmenu(Some(&submenu));
                if let Some(symbol) = symbol
                    && let Some(image) = NSImage::imageWithSystemSymbolName_accessibilityDescription(
                        &NSString::from_str(symbol),
                        None,
                    )
                {
                    native.setImage(Some(&image));
                }
                native
            }
        };
        menu.addItem(&native);
    }
}

fn plain(title: &str, mtm: MainThreadMarker) -> Retained<NSMenuItem> {
    // SAFETY: an item without an action sends nothing.
    unsafe {
        NSMenuItem::initWithTitle_action_keyEquivalent(
            NSMenuItem::alloc(mtm),
            &NSString::from_str(title),
            None,
            &NSString::from_str(""),
        )
    }
}

/// The vortex as an 18 pt template image, so macOS tints it for light and
/// dark menu bars. Idle and connecting states are dimmed; a session that
/// needs attention gets a dot.
fn icon(indicator: Indicator) -> Retained<NSImage> {
    let opacity = match indicator {
        Indicator::Idle => 0.45,
        Indicator::Connecting => 0.7,
        Indicator::Connected | Indicator::Attention => 1.0,
    };
    let attention = indicator == Indicator::Attention;
    let arms = glyph::arms(70);
    let draw = RcBlock::new(move |rect: NSRect| -> Bool {
        let scale = rect.size.width / glyph::SIZE;
        let point = |(x, y): (f64, f64)| NSPoint::new(x * scale, y * scale);
        let circle = |x: f64, y: f64, r: f64| {
            NSRect::new(
                NSPoint::new((x - r) * scale, (y - r) * scale),
                NSSize::new(2.0 * r * scale, 2.0 * r * scale),
            )
        };
        NSColor::colorWithWhite_alpha(0.0, opacity).setFill();
        for arm in &arms {
            let path = NSBezierPath::bezierPath();
            path.moveToPoint(point(arm[0]));
            for p in &arm[1..] {
                path.lineToPoint(point(*p));
            }
            path.closePath();
            path.fill();
        }
        if attention && let Some(context) = NSGraphicsContext::currentContext() {
            context.setCompositingOperation(NSCompositingOperation::Clear);
            NSBezierPath::bezierPathWithOvalInRect(circle(84.0, 84.0, 20.0)).fill();
            context.setCompositingOperation(NSCompositingOperation::SourceOver);
            NSColor::blackColor().setFill();
            NSBezierPath::bezierPathWithOvalInRect(circle(84.0, 84.0, 13.0)).fill();
        }
        Bool::YES
    });
    let image = NSImage::imageWithSize_flipped_drawingHandler(
        NSSize::new(POINT_SIZE, POINT_SIZE),
        true,
        &draw,
    );
    image.setTemplate(true);
    image.setAccessibilityDescription(Some(&NSString::from_str("Kamui")));
    image
}
