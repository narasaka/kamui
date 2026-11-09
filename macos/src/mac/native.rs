//! Small AppKit and Foundation calls: settings in `NSUserDefaults`, the
//! login item, the bundle's version, and the top of the screen that the
//! menu bar and the notch cover.
//!
//! Settings keep the keys the Swift app used, so an upgrade keeps the user's
//! chosen kamui.

use objc2_app_kit::NSScreen;
use objc2_foundation::{MainThreadMarker, NSBundle, NSString, NSUserDefaults};
use objc2_service_management::{SMAppService, SMAppServiceStatus};

const EXECUTABLE_OVERRIDE: &str = "kamuiExecutablePath";
const ONBOARDED: &str = "onboardingCompleted";

fn defaults() -> objc2::rc::Retained<NSUserDefaults> {
    NSUserDefaults::standardUserDefaults()
}

pub fn executable_override() -> Option<String> {
    defaults()
        .stringForKey(&NSString::from_str(EXECUTABLE_OVERRIDE))
        .map(|s| s.to_string())
        .filter(|s| !s.is_empty())
}

pub fn set_executable_override(path: Option<&str>) {
    let key = NSString::from_str(EXECUTABLE_OVERRIDE);
    match path {
        // SAFETY: an NSString is a valid property-list object.
        Some(path) => unsafe { defaults().setObject_forKey(Some(&NSString::from_str(path)), &key) },
        None => defaults().removeObjectForKey(&key),
    }
}

pub fn onboarded() -> bool {
    defaults().boolForKey(&NSString::from_str(ONBOARDED))
}

pub fn set_onboarded(done: bool) {
    defaults().setBool_forKey(done, &NSString::from_str(ONBOARDED));
}

pub fn launches_at_login() -> bool {
    // SAFETY: SMAppService's main-app service and status take no arguments.
    unsafe { SMAppService::mainAppService().status() == SMAppServiceStatus::Enabled }
}

pub fn set_launches_at_login(enabled: bool) -> Result<bool, String> {
    // SAFETY: registering the main app as a login item has no preconditions.
    let result = unsafe {
        let service = SMAppService::mainAppService();
        if enabled {
            service.registerAndReturnError()
        } else {
            service.unregisterAndReturnError()
        }
    };
    result.map_err(|error| error.localizedDescription().to_string())?;
    Ok(launches_at_login())
}

/// `CFBundleShortVersionString`, when the app runs from a bundle.
pub fn version() -> Option<String> {
    let bundle = NSBundle::mainBundle();
    let value =
        bundle.objectForInfoDictionaryKey(&NSString::from_str("CFBundleShortVersionString"))?;
    let value = value.downcast::<NSString>().ok()?;
    Some(value.to_string())
}

/// How far down the main display the menu bar and the notch reach, in
/// points. The menu bar is taller on a Mac with a notch; while it hides,
/// the notch still covers the top. Call it on the main thread.
pub fn top_inset() -> f64 {
    const USUAL: f64 = 24.0;
    let Some(mtm) = MainThreadMarker::new() else {
        return USUAL;
    };
    let screens = NSScreen::screens(mtm);
    let Some(screen) = screens.firstObject() else {
        return USUAL;
    };
    let (frame, visible) = (screen.frame(), screen.visibleFrame());
    let top = frame.origin.y + frame.size.height;
    let visible_top = visible.origin.y + visible.size.height;
    (top - visible_top)
        .max(screen.safeAreaInsets().top)
        .max(0.0)
}
