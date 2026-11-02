import Foundation

public struct CommandResult: Equatable, Sendable {
    public var exitStatus: Int32
    public var standardOutput: Data
    public var standardError: String

    public init(exitStatus: Int32, standardOutput: Data = Data(), standardError: String = "") {
        self.exitStatus = exitStatus
        self.standardOutput = standardOutput
        self.standardError = standardError
    }
}

/// Receives each line of a command's standard error while the command runs.
public typealias StandardErrorLineHandler = @Sendable (String) -> Void

/// Runs external commands. Tests substitute a fake.
public protocol CommandRunning: Sendable {
    func run(_ executable: URL, arguments: [String], environment: [String: String], timeout: Duration, onStandardErrorLine: StandardErrorLineHandler?) async throws -> CommandResult
}

extension CommandRunning {
    public func run(_ executable: URL, arguments: [String], environment: [String: String], timeout: Duration) async throws -> CommandResult {
        try await run(executable, arguments: arguments, environment: environment, timeout: timeout, onStandardErrorLine: nil)
    }
}

/// Runs a process with its output captured in temporary files.
///
/// A kamui invocation may start the background controller, which inherits the
/// invocation's standard streams for its lifetime. Pipes would therefore never
/// reach end-of-file, so this runner waits for the direct child to exit and
/// then reads files instead. A standard error handler receives complete lines
/// as the file grows, so the caller can act on diagnostics, such as a sign-in
/// link, before the command finishes.
public struct ProcessRunner: CommandRunning {
    public init() {}

    /// How long a timed-out process may take to exit after SIGTERM before it
    /// receives SIGKILL.
    static let terminationGrace: DispatchTimeInterval = .seconds(2)

    /// How often a standard error handler sees new output.
    static let standardErrorPollInterval: DispatchTimeInterval = .milliseconds(100)

    public func run(_ executable: URL, arguments: [String], environment: [String: String], timeout: Duration, onStandardErrorLine: StandardErrorLineHandler?) async throws -> CommandResult {
        // The wait blocks a dispatch thread rather than Swift's cooperative pool.
        try await withCheckedThrowingContinuation { continuation in
            DispatchQueue.global(qos: .utility).async {
                continuation.resume(with: Result {
                    try Self.runBlocking(executable, arguments: arguments, environment: environment, timeout: timeout, onStandardErrorLine: onStandardErrorLine)
                })
            }
        }
    }

    private static func runBlocking(_ executable: URL, arguments: [String], environment: [String: String], timeout: Duration, onStandardErrorLine: StandardErrorLineHandler?) throws -> CommandResult {
        let fileManager = FileManager.default
        let directory = fileManager.temporaryDirectory.appendingPathComponent("kamui-\(UUID().uuidString)", isDirectory: true)
        try fileManager.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        defer { try? fileManager.removeItem(at: directory) }
        let outputURL = directory.appendingPathComponent("stdout")
        let errorURL = directory.appendingPathComponent("stderr")
        fileManager.createFile(atPath: outputURL.path, contents: nil, attributes: [.posixPermissions: 0o600])
        fileManager.createFile(atPath: errorURL.path, contents: nil, attributes: [.posixPermissions: 0o600])
        let output = try FileHandle(forWritingTo: outputURL)
        let error = try FileHandle(forWritingTo: errorURL)
        defer {
            try? output.close()
            try? error.close()
        }

        let process = Process()
        process.executableURL = executable
        process.arguments = arguments
        process.environment = environment
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = output
        process.standardError = error
        let exited = DispatchSemaphore(value: 0)
        process.terminationHandler = { _ in exited.signal() }
        let lines = try onStandardErrorLine.map { try LineReader(url: errorURL, handler: $0) }
        defer { try? lines?.file.close() }
        try process.run()

        let components = timeout.components
        let seconds = Double(components.seconds) + Double(components.attoseconds) / 1e18
        let deadline = DispatchTime.now() + seconds
        var result = DispatchTimeoutResult.timedOut
        repeat {
            let next = lines == nil ? deadline : min(deadline, .now() + standardErrorPollInterval)
            result = exited.wait(timeout: next)
            lines?.drain()
        } while result == .timedOut && DispatchTime.now() < deadline
        if result == .timedOut {
            process.terminate()
            if exited.wait(timeout: .now() + terminationGrace) == .timedOut {
                kill(process.processIdentifier, SIGKILL)
                _ = exited.wait(timeout: .now() + terminationGrace)
            }
            throw KamuiError.timedOut(([executable.lastPathComponent] + arguments).joined(separator: " "))
        }
        lines?.drain()
        lines?.finish()
        return CommandResult(
            exitStatus: process.terminationStatus,
            standardOutput: (try? Data(contentsOf: outputURL)) ?? Data(),
            standardError: String(decoding: (try? Data(contentsOf: errorURL)) ?? Data(), as: UTF8.self)
        )
    }
}

/// Reads lines appended to a file and passes each complete one to a handler.
private final class LineReader {
    let file: FileHandle
    private let handler: StandardErrorLineHandler
    private var pending = Data()

    init(url: URL, handler: @escaping StandardErrorLineHandler) throws {
        file = try FileHandle(forReadingFrom: url)
        self.handler = handler
    }

    func drain() {
        guard let data = try? file.readToEnd(), !data.isEmpty else { return }
        pending.append(data)
        while let newline = pending.firstIndex(of: UInt8(ascii: "\n")) {
            handler(String(decoding: pending[pending.startIndex..<newline], as: UTF8.self))
            pending.removeSubrange(pending.startIndex...newline)
        }
    }

    /// Passes a final line that has no newline.
    func finish() {
        if !pending.isEmpty {
            handler(String(decoding: pending, as: UTF8.self))
            pending.removeAll()
        }
    }
}
