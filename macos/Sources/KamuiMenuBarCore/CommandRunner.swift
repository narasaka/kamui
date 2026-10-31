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

/// Runs external commands. Tests substitute a fake.
public protocol CommandRunning: Sendable {
    func run(_ executable: URL, arguments: [String], environment: [String: String], timeout: Duration) async throws -> CommandResult
}

/// Runs a process with its output captured in temporary files.
///
/// A kamui invocation may start the background controller, which inherits the
/// invocation's standard streams for its lifetime. Pipes would therefore never
/// reach end-of-file, so this runner waits for the direct child to exit and
/// then reads files instead.
public struct ProcessRunner: CommandRunning {
    public init() {}

    /// How long a timed-out process may take to exit after SIGTERM before it
    /// receives SIGKILL.
    static let terminationGrace: DispatchTimeInterval = .seconds(2)

    public func run(_ executable: URL, arguments: [String], environment: [String: String], timeout: Duration) async throws -> CommandResult {
        // The wait blocks a dispatch thread rather than Swift's cooperative pool.
        try await withCheckedThrowingContinuation { continuation in
            DispatchQueue.global(qos: .utility).async {
                continuation.resume(with: Result {
                    try Self.runBlocking(executable, arguments: arguments, environment: environment, timeout: timeout)
                })
            }
        }
    }

    private static func runBlocking(_ executable: URL, arguments: [String], environment: [String: String], timeout: Duration) throws -> CommandResult {
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
        try process.run()

        let components = timeout.components
        let seconds = Double(components.seconds) + Double(components.attoseconds) / 1e18
        if exited.wait(timeout: .now() + seconds) == .timedOut {
            process.terminate()
            if exited.wait(timeout: .now() + terminationGrace) == .timedOut {
                kill(process.processIdentifier, SIGKILL)
                _ = exited.wait(timeout: .now() + terminationGrace)
            }
            throw KamuiError.timedOut(([executable.lastPathComponent] + arguments).joined(separator: " "))
        }
        return CommandResult(
            exitStatus: process.terminationStatus,
            standardOutput: (try? Data(contentsOf: outputURL)) ?? Data(),
            standardError: String(decoding: (try? Data(contentsOf: errorURL)) ?? Data(), as: UTF8.self)
        )
    }
}
