import Foundation

/// Drives the kamui CLI: the user's installed one or the copy inside the app.
/// The app never talks to the controller socket directly, so it stays
/// compatible with the CLI's controller handoff.
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
    /// printed, such as enabled agent forwarding. The explicit `mirror`
    /// command keeps a destination named like a subcommand, such as `status`,
    /// a destination.
    ///
    /// With a prompt handler, kamui relays OpenSSH's passphrase, password,
    /// host-key, and other prompts to it (`--json-prompts`). Answers pass
    /// through memory to kamui's standard input and are never stored or
    /// logged. Time spent waiting for an answer does not count toward the
    /// timeout. Without one, OpenSSH may not prompt (`--non-interactive`).
    ///
    /// A Tailscale SSH check prints a sign-in link and holds the connection
    /// until the user signs in. `onSignInURL` receives each such link once,
    /// while the command is still waiting.
    public func connect(_ destination: String, prompts: SSHPromptHandler? = nil, onSignInURL: @escaping @Sendable (URL) -> Void = { _ in }) async throws -> [String] {
        let destination = try Destination.validate(destination)
        let seen = SeenURLs()
        var io = CommandIO(onStandardErrorLine: { line in
            if let url = Self.signInURL(in: line), seen.insert(url) {
                onSignInURL(url)
            }
        })
        let tasks = PromptTasks()
        defer { tasks.cancelAll() }
        var mode = "--non-interactive"
        if let prompts {
            mode = "--json-prompts"
            let input = CommandInput()
            io.input = input
            io.onStandardOutputLine = { line in
                switch PromptProtocol.message(from: line) {
                case .prompt(let prompt) where prompt.kind == .notice:
                    prompts.notice(prompt.text)
                case .prompt(let prompt):
                    input.holdTimeout()
                    tasks.start(prompt.id) {
                        defer { input.releaseTimeout() }
                        let reply = await prompts.answer(prompt)
                        guard !Task.isCancelled else { return }
                        try? input.write(PromptProtocol.line(replying: reply, to: prompt.id))
                    }
                case .cancel(let id):
                    tasks.cancel(id)
                case nil:
                    break
                }
            }
        }
        let result: CommandResult
        do {
            result = try await run(["mirror", mode, destination], timeout: .seconds(120), io: io)
        } catch KamuiError.commandFailed(let message) where prompts != nil && Self.rejectsJSONPrompts(message) {
            throw KamuiError.promptsUnsupported(executable.path)
        }
        return result.standardError
            .split(separator: "\n")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { $0.hasPrefix("WARNING:") }
    }

    /// Reports whether an older kamui rejected `--json-prompts`.
    static func rejectsJSONPrompts(_ message: String) -> Bool {
        message.contains("flag provided but not defined") && message.contains("json-prompts")
    }

    public func stop(_ destination: String) async throws {
        let destination = try Destination.validate(destination)
        _ = try await run(["stop", destination], timeout: .seconds(30))
    }

    public func stopAll() async throws {
        _ = try await run(["stop", "--all"], timeout: .seconds(30))
    }

    private func run(_ arguments: [String], timeout: Duration, io: CommandIO = CommandIO()) async throws -> CommandResult {
        let result = try await runner.run(executable, arguments: arguments, environment: environment, timeout: timeout, io: io)
        guard result.exitStatus == 0 else {
            throw KamuiError.commandFailed(Self.failureMessage(result))
        }
        return result
    }

    /// Finds the link in Tailscale SSH's check message, such as
    /// `# To authenticate, visit: https://login.tailscale.com/a/…`. Only HTTPS
    /// links are accepted.
    static func signInURL(in line: String) -> URL? {
        var text = Substring(line.trimmingCharacters(in: .whitespacesAndNewlines))
        if text.hasPrefix("#") {
            text = text.dropFirst().drop(while: { $0 == " " })
        }
        let prefix = "To authenticate, visit:"
        guard text.hasPrefix(prefix) else { return nil }
        let link = text.dropFirst(prefix.count).trimmingCharacters(in: .whitespaces)
        guard let url = URL(string: link), url.scheme?.lowercased() == "https", url.host?.isEmpty == false else { return nil }
        return url
    }

    /// Reports whether a sign-in link goes to Tailscale's own login service,
    /// which the app opens without asking. The SSH server chooses the link, so
    /// the app confirms any other host, such as a self-hosted Headscale server.
    public static func isTailscaleLogin(_ url: URL) -> Bool {
        url.host?.lowercased() == "login.tailscale.com"
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
