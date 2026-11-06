//! Everything the app does that needs no window: running kamui, reading its
//! status, relaying SSH prompts, finding the CLI, and reading ~/.ssh/config.
//! None of it touches AppKit, so it runs and tests anywhere.

pub mod client;
pub mod destination;
mod error;
pub mod glyph;
pub mod locator;
pub mod prompt;
pub mod runner;
pub mod ssh_config;
mod status;

pub use client::KamuiClient;
pub use error::KamuiError;
pub use status::{
    Conflict, Indicator, Mirror, SUPPORTED_FORMAT, Session, SessionState, StatusDocument,
};
