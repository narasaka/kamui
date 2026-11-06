use std::fmt;

/// Everything the app reports about running kamui. Messages are written for
/// people: they appear in the menu and the window as they are.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum KamuiError {
    ExecutableNotFound,
    InvalidDestination(String),
    CommandFailed(String),
    TimedOut(String),
    Launch(String),
    MalformedStatus(String),
    UnsupportedStatusFormat(i64),
    PromptsUnsupported(String),
    CommandFinished,
}

impl fmt::Display for KamuiError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::ExecutableNotFound => f.write_str(
                "Kamui could not find the kamui command-line tool. Reinstall Kamui or choose the tool's location in Settings.",
            ),
            Self::InvalidDestination(reason) | Self::CommandFailed(reason) => f.write_str(reason),
            Self::TimedOut(command) => write!(f, "`{command}` did not finish in time."),
            Self::Launch(detail) => write!(f, "Kamui could not run kamui: {detail}"),
            Self::MalformedStatus(detail) => {
                write!(f, "kamui returned status this app could not read: {detail}")
            }
            Self::UnsupportedStatusFormat(format) => write!(
                f,
                "kamui reports status format {format}, which this app does not understand. Update the Kamui app."
            ),
            Self::PromptsUnsupported(path) => write!(
                f,
                "The kamui at {path} is older than this app and can't pass SSH prompts to it. Update it, or choose Use bundled kamui in Settings. Switching restarts Kamui's background controller and ends the sessions it is running, so updating the terminal's kamui is usually better."
            ),
            Self::CommandFinished => f.write_str("kamui finished before the answer could be sent."),
        }
    }
}

impl std::error::Error for KamuiError {}
