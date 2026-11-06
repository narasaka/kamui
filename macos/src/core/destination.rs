use super::KamuiError;

/// Validates an OpenSSH destination before it reaches the kamui CLI, which
/// performs the same checks. Returns the trimmed destination.
pub fn validate(raw: &str) -> Result<String, KamuiError> {
    let value = raw.trim();
    if value.is_empty() {
        return Err(KamuiError::InvalidDestination(
            "Enter an SSH destination, such as an alias from ~/.ssh/config or user@host.".into(),
        ));
    }
    if value.starts_with('-') {
        return Err(KamuiError::InvalidDestination(
            "SSH destinations cannot begin with \"-\".".into(),
        ));
    }
    if value.chars().any(|c| c.is_whitespace() || c.is_control()) {
        return Err(KamuiError::InvalidDestination(
            "SSH destinations cannot contain spaces or control characters.".into(),
        ));
    }
    Ok(value.to_owned())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn accepts_openssh_destination_forms() {
        for destination in [
            "foo",
            "nathan@192.168.1.50",
            "nathan@foo.example.com",
            "monitoring",
        ] {
            assert_eq!(validate(destination).unwrap(), destination);
        }
        assert_eq!(validate("  foo\n").unwrap(), "foo");
    }

    #[test]
    fn rejects_values_openssh_would_read_as_options() {
        for destination in [
            "",
            "   ",
            "-oProxyCommand=evil",
            "foo bar",
            "foo\tbar",
            "foo\u{7}",
        ] {
            assert!(validate(destination).is_err(), "{destination:?}");
        }
    }
}
