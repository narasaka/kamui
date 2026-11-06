use serde::Deserialize;

use super::KamuiError;

/// The newest `format` value of `kamui status --json` this app understands.
pub const SUPPORTED_FORMAT: i64 = 1;

/// The document printed by `kamui status --json`.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
pub struct StatusDocument {
    pub format: i64,
    #[serde(default)]
    pub sessions: Vec<Session>,
}

impl StatusDocument {
    pub fn decode(data: &[u8]) -> Result<Self, KamuiError> {
        let document: Self = serde_json::from_slice(data)
            .map_err(|error| KamuiError::MalformedStatus(error.to_string()))?;
        if document.format > SUPPORTED_FORMAT {
            return Err(KamuiError::UnsupportedStatusFormat(document.format));
        }
        Ok(document)
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Session {
    pub destination: String,
    pub state: SessionState,
    #[serde(default)]
    pub last_error: Option<String>,
    #[serde(default)]
    pub browser: Option<String>,
    #[serde(default)]
    pub mirror: Mirror,
}

impl Session {
    pub fn new(destination: &str, state: SessionState) -> Self {
        Self {
            destination: destination.into(),
            state,
            last_error: None,
            browser: None,
            mirror: Mirror::default(),
        }
    }

    /// Whether the session needs the user: it is not working, or its port
    /// discovery failed.
    pub fn needs_attention(&self) -> bool {
        self.state.can_reconnect() || self.mirror.last_error.is_some()
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SessionState {
    Connecting,
    Connected,
    AuthenticationRequired,
    Unavailable,
    Unknown(String),
}

impl<'de> Deserialize<'de> for SessionState {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let value = String::deserialize(deserializer)?;
        Ok(match value.as_str() {
            "connecting" => Self::Connecting,
            "connected" => Self::Connected,
            "authentication-required" => Self::AuthenticationRequired,
            "unavailable" => Self::Unavailable,
            _ => Self::Unknown(value),
        })
    }
}

impl SessionState {
    pub fn label(&self) -> &str {
        match self {
            Self::Connecting => "Connecting",
            Self::Connected => "Connected",
            Self::AuthenticationRequired => "Authentication required",
            Self::Unavailable => "Unavailable",
            Self::Unknown(value) => value,
        }
    }

    /// Whether the session is not working and the app offers to reconnect
    /// it. Kamui may already be retrying an unavailable session in the
    /// background; a session that needs authentication waits for the user.
    pub fn can_reconnect(&self) -> bool {
        matches!(self, Self::AuthenticationRequired | Self::Unavailable)
    }
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Mirror {
    pub enabled: bool,
    pub mirrored_ports: Vec<u16>,
    pub excluded_ports: Vec<u16>,
    pub conflicts: Vec<Conflict>,
    pub last_error: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
pub struct Conflict {
    pub port: u16,
    #[serde(default)]
    pub error: Option<String>,
}

/// The aggregate state shown by the menu bar icon.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Indicator {
    Idle,
    Connecting,
    Connected,
    Attention,
}

impl Indicator {
    pub fn new(sessions: &[Session], pending: bool, failed: bool) -> Self {
        if failed || sessions.iter().any(Session::needs_attention) {
            Self::Attention
        } else if pending || sessions.iter().any(|s| s.state != SessionState::Connected) {
            Self::Connecting
        } else if sessions.is_empty() {
            Self::Idle
        } else {
            Self::Connected
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn decodes_cli_status_document() {
        let json = r#"{
          "format": 1,
          "sessions": [
            {
              "destination": "reyna",
              "state": "authentication-required",
              "lastError": "SSH authentication failed for reyna",
              "mirror": {
                "enabled": true,
                "mirroredPorts": [3000, 5173],
                "excludedPorts": [22],
                "conflicts": [{"port": 5432, "error": "address already in use"}],
                "lastError": "discovery stalled"
              }
            },
            {
              "destination": "nathan@192.168.1.50",
              "state": "connected",
              "browser": "firefox",
              "futureField": true,
              "mirror": {"enabled": false, "mirroredPorts": [], "excludedPorts": [], "conflicts": []}
            }
          ]
        }"#;
        let document = StatusDocument::decode(json.as_bytes()).unwrap();
        let mut reyna = Session::new("reyna", SessionState::AuthenticationRequired);
        reyna.last_error = Some("SSH authentication failed for reyna".into());
        reyna.mirror = Mirror {
            enabled: true,
            mirrored_ports: vec![3000, 5173],
            excluded_ports: vec![22],
            conflicts: vec![Conflict {
                port: 5432,
                error: Some("address already in use".into()),
            }],
            last_error: Some("discovery stalled".into()),
        };
        let mut nathan = Session::new("nathan@192.168.1.50", SessionState::Connected);
        nathan.browser = Some("firefox".into());
        assert_eq!(document.sessions, vec![reyna, nathan]);
    }

    #[test]
    fn decodes_golden_output_from_go_cli() {
        // internal/cliapp/status_test.go asserts that kamui prints this file.
        let fixture = concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../internal/cliapp/testdata/status.json"
        );
        let document = StatusDocument::decode(&std::fs::read(fixture).unwrap()).unwrap();
        assert_eq!(document.sessions.len(), 1);
        let session = &document.sessions[0];
        assert_eq!(session.destination, "reyna");
        assert_eq!(session.state, SessionState::AuthenticationRequired);
        assert_eq!(
            session.last_error.as_deref(),
            Some("SSH authentication failed for reyna")
        );
        assert_eq!(session.mirror.mirrored_ports, vec![3000, 5173]);
        assert_eq!(
            session.mirror.conflicts,
            vec![Conflict {
                port: 5432,
                error: Some("address already in use".into())
            }]
        );
        assert_eq!(
            session.mirror.last_error.as_deref(),
            Some("discovery stalled")
        );
    }

    #[test]
    fn rejects_newer_status_format() {
        assert_eq!(
            StatusDocument::decode(br#"{"format": 2, "sessions": []}"#),
            Err(KamuiError::UnsupportedStatusFormat(2))
        );
        assert!(matches!(
            StatusDocument::decode(b"not json"),
            Err(KamuiError::MalformedStatus(_))
        ));
    }

    #[test]
    fn unknown_states_remain_visible() {
        let document = StatusDocument::decode(
            br#"{"format":1,"sessions":[{"destination":"a","state":"paused","mirror":{"enabled":false,"mirroredPorts":[],"excludedPorts":[],"conflicts":[]}}]}"#,
        )
        .unwrap();
        assert_eq!(
            document.sessions[0].state,
            SessionState::Unknown("paused".into())
        );
        assert_eq!(document.sessions[0].state.label(), "paused");
    }

    #[test]
    fn indicator_prioritizes_problems() {
        let connected = Session::new("a", SessionState::Connected);
        let connecting = Session::new("b", SessionState::Connecting);
        let needs_auth = Session::new("c", SessionState::AuthenticationRequired);
        let mut mirror_failure = connected.clone();
        mirror_failure.mirror.last_error = Some("discovery failed".into());

        assert_eq!(Indicator::new(&[], false, false), Indicator::Idle);
        assert_eq!(
            Indicator::new(std::slice::from_ref(&connected), false, false),
            Indicator::Connected
        );
        assert_eq!(
            Indicator::new(std::slice::from_ref(&connected), true, false),
            Indicator::Connecting
        );
        assert_eq!(
            Indicator::new(&[connected.clone(), connecting.clone()], false, false),
            Indicator::Connecting
        );
        assert_eq!(
            Indicator::new(&[connecting, needs_auth], false, false),
            Indicator::Attention
        );
        assert_eq!(
            Indicator::new(&[mirror_failure], false, false),
            Indicator::Attention
        );
        assert_eq!(
            Indicator::new(&[connected], false, true),
            Indicator::Attention
        );
    }
}
