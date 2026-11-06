//! Lists concrete `Host` aliases from OpenSSH client configuration so the app
//! can offer them as destinations. Patterns and negations are skipped because
//! they do not name one host.

use std::collections::{HashMap, HashSet};
use std::ffi::{CStr, CString};

use super::destination;

pub fn load(home: &str) -> Vec<String> {
    let mut visited = HashSet::new();
    let mut hosts = Vec::new();
    collect(
        &format!("{home}/.ssh/config"),
        &format!("{home}/.ssh"),
        &mut visited,
        &mut hosts,
        0,
    );
    hosts
}

/// The hosts in the current user's configuration.
pub fn load_default() -> Vec<String> {
    std::env::var("HOME")
        .map(|home| load(&home))
        .unwrap_or_default()
}

/// Parses one configuration text. `include` resolves Include arguments.
pub fn parse(text: &str, include: &dyn Fn(&str) -> Vec<String>) -> Vec<String> {
    let mut hosts: Vec<String> = Vec::new();
    let add = |alias: String, hosts: &mut Vec<String>| {
        if !hosts.contains(&alias) {
            hosts.push(alias);
        }
    };
    for line in text.lines() {
        let words = tokens(line);
        let Some(keyword) = words.first().map(|w| w.to_lowercase()) else {
            continue;
        };
        match keyword.as_str() {
            "host" => words[1..]
                .iter()
                .filter(|a| is_concrete(a))
                .for_each(|a| add(a.clone(), &mut hosts)),
            "include" => {
                for argument in &words[1..] {
                    include(argument)
                        .into_iter()
                        .for_each(|a| add(a, &mut hosts));
                }
            }
            _ => {}
        }
    }
    hosts
}

fn is_concrete(alias: &str) -> bool {
    !alias.contains(['*', '?', '!']) && destination::validate(alias).is_ok()
}

/// Splits an OpenSSH configuration line into its keyword and arguments,
/// honouring `keyword=value`, double quotes, and comments.
fn tokens(line: &str) -> Vec<String> {
    let mut words = Vec::new();
    let mut current = String::new();
    let mut quoted = false;
    let mut keyword_done = false;
    let finish = |current: &mut String, words: &mut Vec<String>| {
        if !current.is_empty() {
            words.push(std::mem::take(current));
        }
    };
    for character in line.chars() {
        if quoted {
            if character == '"' {
                quoted = false;
                finish(&mut current, &mut words);
            } else {
                current.push(character);
            }
            continue;
        }
        match character {
            '#' if current.is_empty() => {
                finish(&mut current, &mut words);
                return words;
            }
            '"' => quoted = true,
            '=' if !keyword_done || (words.len() == 1 && current.is_empty()) => {
                finish(&mut current, &mut words);
                keyword_done = true;
            }
            c if c.is_whitespace() => {
                finish(&mut current, &mut words);
                if !words.is_empty() {
                    keyword_done = true;
                }
            }
            c => current.push(c),
        }
    }
    finish(&mut current, &mut words);
    words
}

fn collect(
    path: &str,
    ssh_directory: &str,
    visited: &mut HashSet<String>,
    hosts: &mut Vec<String>,
    depth: usize,
) {
    if depth >= 16 || visited.contains(path) {
        return;
    }
    let Ok(text) = std::fs::read_to_string(path) else {
        return;
    };
    visited.insert(path.to_owned());
    let mut nested: HashMap<String, Vec<String>> = HashMap::new();
    for line in text.lines() {
        let words = tokens(line);
        if words.first().map(|w| w.to_lowercase()).as_deref() != Some("include") {
            continue;
        }
        for argument in &words[1..] {
            let mut included = Vec::new();
            for file in expand(argument, ssh_directory) {
                collect(&file, ssh_directory, visited, &mut included, depth + 1);
            }
            nested.insert(argument.clone(), included);
        }
    }
    for alias in parse(&text, &|argument| {
        nested.get(argument).cloned().unwrap_or_default()
    }) {
        if !hosts.contains(&alias) {
            hosts.push(alias);
        }
    }
}

/// Resolves an Include argument: `~` expands to the home directory and
/// relative paths are relative to ~/.ssh, as in ssh_config(5).
fn expand(pattern: &str, ssh_directory: &str) -> Vec<String> {
    let resolved = if let Some(rest) = pattern.strip_prefix("~/") {
        let home = ssh_directory.strip_suffix("/.ssh").unwrap_or(ssh_directory);
        format!("{home}/{rest}")
    } else if pattern.starts_with('/') {
        pattern.to_owned()
    } else {
        format!("{ssh_directory}/{pattern}")
    };
    glob(&resolved)
}

fn glob(pattern: &str) -> Vec<String> {
    let Ok(pattern) = CString::new(pattern) else {
        return Vec::new();
    };
    // SAFETY: glob_t is plain data that glob fills in and globfree releases.
    unsafe {
        let mut matches: libc::glob_t = std::mem::zeroed();
        let found = if libc::glob(pattern.as_ptr(), 0, None, &mut matches) == 0 {
            (0..matches.gl_pathc as usize)
                .filter_map(|i| {
                    let path = *matches.gl_pathv.add(i);
                    (!path.is_null()).then(|| CStr::from_ptr(path).to_string_lossy().into_owned())
                })
                .collect()
        } else {
            Vec::new()
        };
        libc::globfree(&mut matches);
        found
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lists_concrete_aliases_in_order() {
        let config = r#"# Personal machines
Host foo bar
    User narasaka
    LocalForward 3000 localhost:3000
Host *.internal !secret
host=baz
Host = spaced
Host "quoted"  foo   # duplicate is ignored
Match host foo
Host -oProxyCommand=evil
Host *
    ServerAliveInterval 15
"#;
        assert_eq!(
            parse(config, &|_| Vec::new()),
            ["foo", "bar", "baz", "spaced", "quoted"]
        );
    }

    #[test]
    fn follows_includes_in_place() {
        let home = tempfile::tempdir().unwrap();
        let ssh = home.path().join(".ssh");
        std::fs::create_dir_all(ssh.join("config.d")).unwrap();
        std::fs::write(
            ssh.join("config"),
            "Include config.d/*\nHost main\nInclude ~/.ssh/config\n",
        )
        .unwrap();
        std::fs::write(ssh.join("config.d/b-work"), "Host work-box\n").unwrap();
        std::fs::write(ssh.join("config.d/a-lab"), "Host lab\n").unwrap();

        assert_eq!(
            load(home.path().to_str().unwrap()),
            ["lab", "work-box", "main"]
        );
    }

    #[test]
    fn missing_config_has_no_hosts() {
        assert!(load("/nonexistent-kamui-test-home").is_empty());
    }
}
