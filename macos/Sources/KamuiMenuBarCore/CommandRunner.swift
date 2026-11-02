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

/// Receives each line of a command's output while the command runs.
public typealias LineHandler = @Sendable (String) -> Void
public typealias StandardErrorLineHandler = LineHandler

/// Optional interaction with a running command.
public struct CommandIO: Sendable {
    /// Receives each line of standard error as it is written.
    public var onStandardErrorLine: LineHandler?
    /// Receives each line of standard output as it is written.
    public var onStandardOutputLine: LineHandler?
    /// Connects the command's standard input to a pipe; otherwise it reads
    /// from the null device.
    public var input: CommandInput?

    public init(onStandardErrorLine: LineHandler? = nil, onStandardOutputLine: LineHandler? = nil, input: CommandInput? = nil) {
        self.onStandardErrorLine = onStandardErrorLine
        self.onStandardOutputLine = onStandardOutputLine
        self.input = input
    }
}

/// Writes to a running command's standard input and pauses its timeout while
/// the command waits for the user.
public final class CommandInput: @unchecked Sendable {
    private let lock = NSLock()
    private var writer: ((Data) throws -> Void)?
    private var closer: (() -> Void)?
    private var holds = 0

    public init() {}

    /// Connects the input to a destination. The runner calls this before the
    /// command starts; tests may attach a recorder.
    public func attach(write: @escaping (Data) throws -> Void, close: @escaping () -> Void = {}) {
        lock.withLock {
            writer = write
            closer = close
        }
    }

    /// Writes data, or throws when the command has finished.
    public func write(_ data: Data) throws {
        try lock.withLock {
            guard let writer else { throw KamuiError.commandFinished }
            try writer(data)
        }
    }

    /// Stops accepting writes and closes the destination.
    public func close() {
        let close = lock.withLock {
            defer {
                writer = nil
                self.closer = nil
            }
            return self.closer
        }
        close?()
    }

    /// Pauses the command's timeout until a matching `releaseTimeout`.
    public func holdTimeout() {
        lock.withLock { holds += 1 }
    }

    public func releaseTimeout() {
        lock.withLock { holds = max(0, holds - 1) }
    }

    var holdsTimeout: Bool {
        lock.withLock { holds > 0 }
    }
}

/// Runs external commands. Tests substitute a fake.
public protocol CommandRunning: Sendable {
    func run(_ executable: URL, arguments: [String], environment: [String: String], timeout: Duration, io: CommandIO) async throws -> CommandResult
}

extension CommandRunning {
    public func run(_ executable: URL, arguments: [String], environment: [String: String], timeout: Duration) async throws -> CommandResult {
        try await run(executable, arguments: arguments, environment: environment, timeout: timeout, io: CommandIO())
    }

    public func run(_ executable: URL, arguments: [String], environment: [String: String], timeout: Duration, onStandardErrorLine: LineHandler?) async throws -> CommandResult {
        try await run(executable, arguments: arguments, environment: environment, timeout: timeout, io: CommandIO(onStandardErrorLine: onStandardErrorLine))
    }
}

/// Runs a process with its output captured in temporary files.
///
/// A kamui invocation may start the background controller, which inherits the
/// invocation's standard streams for its lifetime. Pipes would therefore never
/// reach end-of-file, so this runner waits for the direct child to exit and
/// then reads files instead. Line handlers receive complete lines as the files
/// grow, so the caller can act on output, such as a sign-in link or an OpenSSH
/// prompt, before the command finishes. Standard input, when requested, is a
/// pipe; the timeout does not count time in which the input holds it.
public struct ProcessRunner: CommandRunning {
    public init() {}

    /// How long a timed-out process may take to exit after SIGTERM before it
    /// receives SIGKILL.
    static let terminationGrace: DispatchTimeInterval = .seconds(2)

    /// How often line handlers see new output.
    static let linePollInterval: DispatchTimeInterval = .milliseconds(100)

    public func run(_ executable: URL, arguments: [String], environment: [String: String], timeout: Duration, io: CommandIO) async throws -> CommandResult {
        // The wait blocks a dispatch thread rather than Swift's cooperative pool.
        try await withCheckedThrowingContinuation { continuation in
            DispatchQueue.global(qos: .utility).async {
                continuation.resume(with: Result {
                    try Self.runBlocking(executable, arguments: arguments, environment: environment, timeout: timeout, io: io)
                })
            }
        }
    }

    private static func runBlocking(_ executable: URL, arguments: [String], environment: [String: String], timeout: Duration, io: CommandIO) throws -> CommandResult {
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
        process.standardOutput = output
        process.standardError = error
        if let input = io.input {
            let pipe = Pipe()
            let writer = pipe.fileHandleForWriting
            // A write after the command exits fails instead of raising SIGPIPE.
            _ = fcntl(writer.fileDescriptor, F_SETNOSIGPIPE, 1)
            input.attach(write: { try writer.write(contentsOf: $0) }, close: { try? writer.close() })
            process.standardInput = pipe
        } else {
            process.standardInput = FileHandle.nullDevice
        }
        defer { io.input?.close() }
        let exited = DispatchSemaphore(value: 0)
        process.terminationHandler = { _ in exited.signal() }
        let errorLines = try io.onStandardErrorLine.map { try LineReader(url: errorURL, handler: $0) }
        defer { try? errorLines?.file.close() }
        let outputLines = try io.onStandardOutputLine.map { try LineReader(url: outputURL, handler: $0) }
        defer { try? outputLines?.file.close() }
        let readers = [errorLines, outputLines].compactMap { $0 }
        let polls = !readers.isEmpty || io.input != nil
        try process.run()

        let components = timeout.components
        let seconds = Double(components.seconds) + Double(components.attoseconds) / 1e18
        var deadline = DispatchTime.now() + seconds
        var checked = DispatchTime.now()
        var result = DispatchTimeoutResult.timedOut
        repeat {
            let next = polls ? min(deadline, .now() + linePollInterval) : deadline
            result = exited.wait(timeout: next)
            readers.forEach { $0.drain() }
            let now = DispatchTime.now()
            if io.input?.holdsTimeout == true {
                // Time spent waiting for the user does not count.
                deadline = deadline + .nanoseconds(Int(now.uptimeNanoseconds - checked.uptimeNanoseconds))
            }
            checked = now
        } while result == .timedOut && DispatchTime.now() < deadline
        if result == .timedOut {
            process.terminate()
            if exited.wait(timeout: .now() + terminationGrace) == .timedOut {
                kill(process.processIdentifier, SIGKILL)
                _ = exited.wait(timeout: .now() + terminationGrace)
            }
            throw KamuiError.timedOut(([executable.lastPathComponent] + arguments).joined(separator: " "))
        }
        readers.forEach {
            $0.drain()
            $0.finish()
        }
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
    private let handler: LineHandler
    private var pending = Data()

    init(url: URL, handler: @escaping LineHandler) throws {
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
