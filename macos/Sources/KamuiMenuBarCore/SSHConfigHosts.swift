import Darwin
import Foundation

/// Lists concrete `Host` aliases from OpenSSH client configuration so the menu
/// can offer them as destinations. Patterns and negations are skipped because
/// they do not name one host.
public enum SSHConfigHosts {
    public static func load(home: String = NSHomeDirectory()) -> [String] {
        var visited = Set<String>()
        var hosts: [String] = []
        collect(path: home + "/.ssh/config", sshDirectory: home + "/.ssh", visited: &visited, hosts: &hosts, depth: 0)
        return hosts
    }

    /// Parses one configuration text. `include` resolves Include arguments.
    public static func parse(_ text: String, include: (String) -> [String] = { _ in [] }) -> [String] {
        var hosts: [String] = []
        for line in text.split(whereSeparator: \.isNewline) {
            let words = tokens(String(line))
            guard let keyword = words.first?.lowercased() else { continue }
            let arguments = words.dropFirst()
            switch keyword {
            case "host":
                for alias in arguments where isConcrete(alias) && !hosts.contains(alias) {
                    hosts.append(alias)
                }
            case "include":
                for argument in arguments {
                    for alias in include(argument) where !hosts.contains(alias) {
                        hosts.append(alias)
                    }
                }
            default:
                continue
            }
        }
        return hosts
    }

    static func isConcrete(_ alias: String) -> Bool {
        !alias.contains(where: { "*?!".contains($0) }) && (try? Destination.validate(alias)) != nil
    }

    /// Splits an OpenSSH configuration line into its keyword and arguments,
    /// honouring `keyword=value`, double quotes, and comments.
    static func tokens(_ line: String) -> [String] {
        var words: [String] = []
        var current = ""
        var quoted = false
        var keywordDone = false
        func finish() {
            if !current.isEmpty { words.append(current) }
            current = ""
        }
        for character in line {
            if quoted {
                if character == "\"" { quoted = false; finish() } else { current.append(character) }
                continue
            }
            switch character {
            case "#" where current.isEmpty:
                finish()
                return words
            case "\"":
                quoted = true
            case "=" where !keywordDone || (words.count == 1 && current.isEmpty):
                finish()
                keywordDone = true
            case _ where character.isWhitespace:
                finish()
                if !words.isEmpty { keywordDone = true }
            default:
                current.append(character)
            }
        }
        finish()
        return words
    }

    private static func collect(path: String, sshDirectory: String, visited: inout Set<String>, hosts: inout [String], depth: Int) {
        guard depth < 16, !visited.contains(path), let text = try? String(contentsOfFile: path, encoding: .utf8) else { return }
        visited.insert(path)
        var nested: [String: [String]] = [:]
        for line in text.split(whereSeparator: \.isNewline) {
            let words = tokens(String(line))
            guard words.first?.lowercased() == "include" else { continue }
            for argument in words.dropFirst() {
                var included: [String] = []
                for file in expand(argument, sshDirectory: sshDirectory) {
                    collect(path: file, sshDirectory: sshDirectory, visited: &visited, hosts: &included, depth: depth + 1)
                }
                nested[argument] = included
            }
        }
        for alias in parse(text, include: { nested[$0] ?? [] }) where !hosts.contains(alias) {
            hosts.append(alias)
        }
    }

    /// Resolves an Include argument: `~` expands to the home directory and
    /// relative paths are relative to ~/.ssh, as in ssh_config(5).
    static func expand(_ pattern: String, sshDirectory: String) -> [String] {
        var resolved = pattern
        if resolved.hasPrefix("~/") {
            resolved = (sshDirectory as NSString).deletingLastPathComponent + resolved.dropFirst()
        } else if !resolved.hasPrefix("/") {
            resolved = sshDirectory + "/" + resolved
        }
        var matches = glob_t()
        defer { globfree(&matches) }
        guard glob(resolved, 0, nil, &matches) == 0 else { return [] }
        return (0..<Int(matches.gl_pathc)).compactMap { index in
            matches.gl_pathv[index].map { String(cString: $0) }
        }
    }
}
