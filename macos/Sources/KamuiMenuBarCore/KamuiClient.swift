import Foundation

/// Drives the installed kamui CLI. The app never talks to the controller
/// socket directly, so it stays compatible with the CLI's controller handoff.
public struct KamuiClient: Sendable {
    public var executable: URL
    public var environment: [String: String]
    public var runner: CommandRunning

    public init(executable: URL, environment: [String: String], runner: CommandRunning = ProcessRunner()) {
        self.executable = executable
        self.environment = environment
        self.runner = runner
    }

    /// Reports sessions without starting the controller.
    public func status() async throws -> StatusDocument {
        let result = try await run(["status", "--json"], timeout: .seconds(20))
        return try StatusDocument.decode(result.standardOutput)
    }

    /// Starts or reuses a mirroring session and returns any warnings kamui
    /// printed, such as enabled agent forwarding. OpenSSH may not prompt, so
    /// a session that needs a passphrase or host-key decision fails promptly
    /// and the user can retry in a terminal. The explicit `mirror` command
    /// keeps a destination named like a subcommand, such as `status`, a
    /// destination.
    ///
    /// A Tailscale SSH check prints a sign-in link and holds the connection
    /// until the user signs in. `onSignInURL` receives each such link once,
    /// while the command is still waiting.
    public func connect(_ destination: String, onSignInURL: @escaping @Sendable (URL) -> Void = { _ in }) async throws -> [String] {
        let destination = try Destination.validate(destination)
        let seen = SeenURLs()
        let result = try await run(["mirror", "--non-interactive", destination], timeout: .seconds(120)) { line in
            if let url = Self.signInURL(in: line), seen.insert(url) {
                onSignInURL(url)
            }
        }
        return result.standardError
            .split(separator: "\n")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { $0.hasPrefix("WARNING:") }
    }

    public func stop(_ destination: String) async throws {
        let destination = try Destination.validate(destination)
        _ = try await run(["stop", destination], timeout: .seconds(30))
    }

    public func stopAll() async throws {
        _ = try await run(["stop", "--all"], timeout: .seconds(30))
    }

    private func run(_ arguments: [String], timeout: Duration, onStandardErrorLine: StandardErrorLineHandler? = nil) async throws -> CommandResult {
        let result = try await runner.run(executable, arguments: arguments, environment: environment, timeout: timeout, onStandardErrorLine: onStandardErrorLine)
        guard result.exitStatus == 0 else {
            throw KamuiError.commandFailed(Self.failureMessage(result))
        }
        return result
    }

    /// Finds the link in Tailscale SSH's check message, such as
    /// `# To authenticate, visit: https://login.tailscale.com/a/…`. Only HTTPS
    /// links are accepted.
    static func signInURL(in line: String) -> URL? {
        var text = Substring(line.trimmingCharacters(in: .whitespaces))
        if text.hasPrefix("#") {
            text = text.dropFirst().drop(while: { $0 == " " })
        }
        let prefix = "To authenticate, visit:"
        guard text.hasPrefix(prefix) else { return nil }
        let link = text.dropFirst(prefix.count).trimmingCharacters(in: .whitespaces)
        guard let url = URL(string: link), url.scheme?.lowercased() == "https", url.host?.isEmpty == false else { return nil }
        return url
    }

    static func failureMessage(_ result: CommandResult) -> String {
        let lines = result.standardError
            .split(separator: "\n", omittingEmptySubsequences: false)
            .map { line in line.hasPrefix("✗ ") ? String(line.dropFirst(2)) : String(line) }
        let message = lines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
        return message.isEmpty ? "kamui exited with status \(result.exitStatus)." : message
    }
}

private final class SeenURLs: @unchecked Sendable {
    private let lock = NSLock()
    private var urls: Set<URL> = []

    /// Records a URL and reports whether it is new.
    func insert(_ url: URL) -> Bool {
        lock.withLock { urls.insert(url).inserted }
    }
}
