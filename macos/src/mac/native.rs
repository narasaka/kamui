//! Small AppKit and Foundation calls: settings in `NSUserDefaults`, the
//! login item, and the bundle's version.
//!
//! Settings keep the keys the Swift app used, so an upgrade keeps the user's
//! chosen kamui.

use objc2_foundation::{NSBundle, NSString, NSUserDefaults};
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
