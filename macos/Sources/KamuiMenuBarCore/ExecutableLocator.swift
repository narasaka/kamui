import Foundation

/// Finds the kamui CLI and the environment it should run with.
///
/// Apps launched from Finder or at login receive a minimal PATH. The CLI and
/// the controller it may start should see the same PATH and SSH agent as the
/// user's login shell, so the app adopts those values from a login shell.
public struct ExecutableLocator: Sendable {
    public static let fallbackDirectories = ["/opt/homebrew/bin", "/usr/local/bin", "~/go/bin", "~/.local/bin"]
    static let importedVariables = ["PATH", "SSH_AUTH_SOCK"]

    public var runner: CommandRunning
    public var isExecutable: @Sendable (String) -> Bool

    public init(
        runner: CommandRunning = ProcessRunner(),
        isExecutable: @escaping @Sendable (String) -> Bool = { FileManager.default.isExecutableFile(atPath: $0) }
    ) {
        self.runner = runner
        self.isExecutable = isExecutable
    }

    /// Returns `base` with PATH and SSH_AUTH_SOCK taken from the login shell
    /// when it reports them.
    ///
    /// The login shell execs a POSIX shell to print the exported values, so
    /// fish and other shells report a colon-separated PATH. A random marker
    /// separates the values from anything the shell's startup files print.
    public func environment(base: [String: String]) async -> [String: String] {
        let shell = base["SHELL"].flatMap { $0.isEmpty ? nil : $0 } ?? "/bin/zsh"
        let marker = "kamui-environment-\(UUID().uuidString)"
        let fields = (["$0"] + Self.importedVariables.map { "$\($0)" }).map { "\"\($0)\"" }.joined(separator: " ")
        let script = "exec /bin/sh -c 'printf \"%s\\0\" \(fields)' \(marker)"
        guard let result = try? await runner.run(URL(fileURLWithPath: shell), arguments: ["-l", "-c", script], environment: base, timeout: .seconds(10)),
              result.exitStatus == 0,
              let imported = Self.parse(result.standardOutput, marker: marker)
        else {
            return base
        }
        var environment = base
        for (name, value) in imported {
            environment[name] = value
        }
        return environment
    }

    /// Extracts the variables printed after `marker`. Returns nil when the
    /// payload is missing, truncated, or reports an unusable PATH.
    static func parse(_ output: Data, marker: String) -> [String: String]? {
        let start = Data((marker + "\0").utf8)
        guard let range = output.range(of: start, options: .backwards) else { return nil }
        let values = output[range.upperBound...].split(separator: 0, omittingEmptySubsequences: false)
        guard values.count == importedVariables.count + 1, values.last?.isEmpty == true else { return nil }
        var imported: [String: String] = [:]
        for (name, value) in zip(importedVariables, values) where !value.isEmpty {
            imported[name] = String(decoding: value, as: UTF8.self)
        }
        guard let path = imported["PATH"],
              !path.contains(where: \.isNewline),
              path.split(separator: ":").contains(where: { $0.hasPrefix("/") })
        else { return nil }
        return imported
    }

    /// Returns an explicit executable when one is configured, otherwise the
    /// first kamui on PATH or in a common installation directory.
    public func locate(override: String?, environment: [String: String]) -> URL? {
        if let override, !override.isEmpty {
            let path = Self.expandingTilde(override, environment: environment)
            return isExecutable(path) ? URL(fileURLWithPath: path) : nil
        }
        let searchPath = (environment["PATH"] ?? "").split(separator: ":").map(String.init)
        for directory in searchPath + Self.fallbackDirectories {
            let path = Self.expandingTilde(directory, environment: environment) + "/kamui"
            if isExecutable(path) {
                return URL(fileURLWithPath: path)
            }
        }
        return nil
    }

    static func expandingTilde(_ path: String, environment: [String: String]) -> String {
        guard path == "~" || path.hasPrefix("~/") else { return path }
        let home = environment["HOME"] ?? NSHomeDirectory()
        return home + path.dropFirst()
    }
}
