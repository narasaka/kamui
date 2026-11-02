import Foundation
import XCTest
@testable import KamuiMenuBarCore

final class FakeRunner: CommandRunning, @unchecked Sendable {
    struct Invocation: Equatable {
        var executable: String
        var arguments: [String]
        var environment: [String: String]
    }

    private let lock = NSLock()
    private var recorded: [Invocation] = []
    private let respond: @Sendable ([String]) -> CommandResult

    init(respond: @escaping @Sendable ([String]) -> CommandResult) {
        self.respond = respond
    }

    var invocations: [Invocation] {
        lock.withLock { recorded }
    }

    func run(_ executable: URL, arguments: [String], environment: [String: String], timeout: Duration, onStandardErrorLine: StandardErrorLineHandler?) async throws -> CommandResult {
        lock.withLock { recorded.append(Invocation(executable: executable.path, arguments: arguments, environment: environment)) }
        let result = respond(arguments)
        if let onStandardErrorLine {
            result.standardError.split(separator: "\n").forEach { onStandardErrorLine(String($0)) }
        }
        return result
    }
}

final class Collector<Value: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var collected: [Value] = []

    var values: [Value] { lock.withLock { collected } }

    func append(_ value: Value) {
        lock.withLock { collected.append(value) }
    }
}

final class KamuiClientTests: XCTestCase {
    let executable = URL(fileURLWithPath: "/opt/homebrew/bin/kamui")

    func testStatusUsesJSONAndEnvironment() async throws {
        let runner = FakeRunner { _ in CommandResult(exitStatus: 0, standardOutput: Data(#"{"format":1,"sessions":[]}"#.utf8)) }
        let client = KamuiClient(executable: executable, environment: ["PATH": "/opt/homebrew/bin:/usr/bin"], runner: runner)

        let document = try await client.status()

        XCTAssertEqual(document.sessions, [])
        XCTAssertEqual(runner.invocations, [.init(executable: "/opt/homebrew/bin/kamui", arguments: ["status", "--json"], environment: ["PATH": "/opt/homebrew/bin:/usr/bin"])])
    }

    func testConnectForbidsOpenSSHPromptsAndPassesDestinationAsOneArgument() async throws {
        let runner = FakeRunner { _ in CommandResult(exitStatus: 0) }
        let client = KamuiClient(executable: executable, environment: [:], runner: runner)

        _ = try await client.connect(" nathan@192.168.1.50 ")
        _ = try await client.connect("status")
        try await client.stop("nathan@192.168.1.50")
        try await client.stopAll()

        XCTAssertEqual(runner.invocations.map(\.arguments), [
            ["mirror", "--non-interactive", "nathan@192.168.1.50"],
            ["mirror", "--non-interactive", "status"],
            ["stop", "nathan@192.168.1.50"],
            ["stop", "--all"],
        ])
    }

    func testConnectReturnsCLIWarnings() async throws {
        let runner = FakeRunner { _ in
            CommandResult(exitStatus: 0, standardError: "Tailscale check passed\nWARNING: SSH agent forwarding is enabled for reyna; the remote host can access the forwarded agent.\n")
        }
        let client = KamuiClient(executable: executable, environment: [:], runner: runner)

        let warnings = try await client.connect("reyna")

        XCTAssertEqual(warnings, ["WARNING: SSH agent forwarding is enabled for reyna; the remote host can access the forwarded agent."])
    }

    func testConnectOpensEachTailscaleSignInLinkOnceEvenWhenConnectionFails() async {
        let runner = FakeRunner { _ in
            CommandResult(exitStatus: 1, standardError: """
            # Tailscale SSH requires an additional check.
            # To authenticate, visit: https://login.tailscale.com/a/example
            # To authenticate, visit: https://login.tailscale.com/a/example
            To authenticate, visit: https://headscale.example.com/a/other
            ✗ SSH authentication or connection failed for reyna
            """)
        }
        let client = KamuiClient(executable: executable, environment: [:], runner: runner)
        let opened = Collector<URL>()

        _ = try? await client.connect("reyna") { opened.append($0) }

        XCTAssertEqual(opened.values.map(\.absoluteString), [
            "https://login.tailscale.com/a/example",
            "https://headscale.example.com/a/other",
        ])
    }

    func testSignInURLAcceptsOnlyHTTPSLinksInTailscaleCheckMessage() {
        XCTAssertEqual(KamuiClient.signInURL(in: "# To authenticate, visit: https://login.tailscale.com/a/abc123")?.absoluteString, "https://login.tailscale.com/a/abc123")
        XCTAssertEqual(KamuiClient.signInURL(in: "  To authenticate, visit:  https://login.tailscale.com/a/abc123  ")?.absoluteString, "https://login.tailscale.com/a/abc123")
        XCTAssertEqual(KamuiClient.signInURL(in: "# To authenticate, visit: https://login.tailscale.com/a/abc123\r")?.absoluteString, "https://login.tailscale.com/a/abc123")
        XCTAssertNil(KamuiClient.signInURL(in: "# To authenticate, visit: http://login.tailscale.com/a/abc123"))
        XCTAssertNil(KamuiClient.signInURL(in: "# To authenticate, visit: file:///Applications/Calculator.app"))
        XCTAssertNil(KamuiClient.signInURL(in: "# To authenticate, visit: https:///missing-host"))
        XCTAssertNil(KamuiClient.signInURL(in: "# Tailscale SSH requires an additional check."))
        XCTAssertNil(KamuiClient.signInURL(in: "Welcome! See https://example.com"))
    }

    func testOnlyTailscaleLoginHostOpensWithoutConfirmation() throws {
        XCTAssertTrue(KamuiClient.isTailscaleLogin(try XCTUnwrap(URL(string: "https://login.tailscale.com/a/abc123"))))
        XCTAssertTrue(KamuiClient.isTailscaleLogin(try XCTUnwrap(URL(string: "https://LOGIN.tailscale.com/a/abc123"))))
        XCTAssertFalse(KamuiClient.isTailscaleLogin(try XCTUnwrap(URL(string: "https://headscale.example.com/a/abc123"))))
        XCTAssertFalse(KamuiClient.isTailscaleLogin(try XCTUnwrap(URL(string: "https://login.tailscale.com.evil.example/a/abc123"))))
        XCTAssertFalse(KamuiClient.isTailscaleLogin(try XCTUnwrap(URL(string: "https://login.tailscale.com@evil.example/a/abc123"))))
    }

    func testConnectRejectsOptionLikeDestinationWithoutRunningKamui() async {
        let runner = FakeRunner { _ in CommandResult(exitStatus: 0) }
        let client = KamuiClient(executable: executable, environment: [:], runner: runner)

        do {
            _ = try await client.connect("-oProxyCommand=touch /tmp/pwned")
            XCTFail("connect accepted an option-like destination")
        } catch {
            XCTAssertTrue(runner.invocations.isEmpty)
        }
    }

    func testFailureReportsCLIErrorWithoutMarker() async {
        let runner = FakeRunner { _ in
            CommandResult(exitStatus: 1, standardError: "✗ SSH authentication or connection failed for reyna: SSH authentication failed for reyna: exit status 255\nPermission denied (publickey).\n")
        }
        let client = KamuiClient(executable: executable, environment: [:], runner: runner)

        do {
            _ = try await client.connect("reyna")
            XCTFail("connect succeeded")
        } catch {
            XCTAssertEqual(error as? KamuiError, .commandFailed("SSH authentication or connection failed for reyna: SSH authentication failed for reyna: exit status 255\nPermission denied (publickey)."))
        }
    }

    func testFailureWithoutOutputReportsExitStatus() {
        XCTAssertEqual(KamuiClient.failureMessage(CommandResult(exitStatus: 3)), "kamui exited with status 3.")
    }
}

final class ProcessRunnerTests: XCTestCase {
    func testCapturesOutputAfterDirectChildExitsEvenWhenADescendantKeepsStreamsOpen() async throws {
        // The background child mimics the kamui controller, which inherits the
        // invoking command's standard streams and outlives it.
        let started = ContinuousClock.now
        let result = try await ProcessRunner().run(
            URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", "printf out; printf err >&2; (sleep 30 &) ; exit 4"],
            environment: [:],
            timeout: .seconds(10)
        )
        XCTAssertLessThan(ContinuousClock.now - started, .seconds(10))
        XCTAssertEqual(result, CommandResult(exitStatus: 4, standardOutput: Data("out".utf8), standardError: "err"))
    }

    func testStreamsStandardErrorLinesBeforeChildExits() async throws {
        let lines = Collector<(String, Duration)>()
        let started = ContinuousClock.now
        let result = try await ProcessRunner().run(
            URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", "printf 'visit: https://login.tailscale.com/a/x\\n' >&2; sleep 2; printf 'last' >&2"],
            environment: [:],
            timeout: .seconds(10)
        ) { line in lines.append((line, ContinuousClock.now - started)) }

        XCTAssertEqual(lines.values.map(\.0), ["visit: https://login.tailscale.com/a/x", "last"])
        XCTAssertLessThan(lines.values[0].1, .seconds(1), "the first line arrived only after the child exited")
        XCTAssertEqual(result.standardError, "visit: https://login.tailscale.com/a/x\nlast")
    }

    func testTimeoutKillsChildThatIgnoresTermination() async {
        let started = ContinuousClock.now
        do {
            _ = try await ProcessRunner().run(URL(fileURLWithPath: "/bin/sh"), arguments: ["-c", "trap '' TERM; while :; do sleep 1; done"], environment: [:], timeout: .milliseconds(100))
            XCTFail("the child finished before the timeout")
        } catch {
            XCTAssertEqual(error as? KamuiError, .timedOut("sh -c trap '' TERM; while :; do sleep 1; done"))
        }
        XCTAssertLessThan(ContinuousClock.now - started, .seconds(5))
    }

    func testTimesOut() async {
        do {
            _ = try await ProcessRunner().run(URL(fileURLWithPath: "/bin/sleep"), arguments: ["5"], environment: [:], timeout: .milliseconds(100))
            XCTFail("sleep finished before the timeout")
        } catch {
            XCTAssertEqual(error as? KamuiError, .timedOut("sleep 5"))
        }
    }
}

final class ExecutableLocatorTests: XCTestCase {
    func testPrefersOverrideThenPathThenFallbacks() {
        let available: Set<String> = ["/custom/kamui", "/Users/n/go/bin/kamui", "/opt/homebrew/bin/kamui"]
        let locator = ExecutableLocator(runner: FakeRunner { _ in CommandResult(exitStatus: 1) }, isExecutable: { available.contains($0) })
        let environment = ["PATH": "/usr/bin:/Users/n/go/bin", "HOME": "/Users/n"]

        XCTAssertEqual(locator.locate(override: "/custom/kamui", environment: environment)?.path, "/custom/kamui")
        XCTAssertNil(locator.locate(override: "/missing/kamui", environment: environment))
        XCTAssertEqual(locator.locate(override: nil, environment: environment)?.path, "/Users/n/go/bin/kamui")
        XCTAssertEqual(locator.locate(override: nil, environment: ["HOME": "/Users/n"])?.path, "/opt/homebrew/bin/kamui")
    }

    func testImportsPathAndAgentFromRealLoginShellDespiteStartupOutput() async throws {
        let home = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: home, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: home) }
        try "echo 'Welcome back'\nprintf 'no newline'\nexport PATH=/opt/kamui-test/bin:$PATH\nexport SSH_AUTH_SOCK=/tmp/agent.sock\n"
            .write(to: home.appendingPathComponent(".profile"), atomically: true, encoding: .utf8)

        let environment = await ExecutableLocator().environment(base: ["SHELL": "/bin/sh", "HOME": home.path, "PATH": "/usr/bin:/bin"])

        XCTAssertEqual(environment["SSH_AUTH_SOCK"], "/tmp/agent.sock")
        XCTAssertEqual(environment["PATH"]?.split(separator: ":").first, "/opt/kamui-test/bin")
    }

    func testParsesOnlyCompleteMarkedPayload() {
        let marker = "kamui-environment-test"
        func payload(_ text: String) -> Data { Data(text.utf8) }

        XCTAssertEqual(
            ExecutableLocator.parse(payload("Welcome\n\(marker)\0/opt/homebrew/bin:/usr/bin\0/tmp/agent.sock\0"), marker: marker),
            ["PATH": "/opt/homebrew/bin:/usr/bin", "SSH_AUTH_SOCK": "/tmp/agent.sock"]
        )
        XCTAssertEqual(ExecutableLocator.parse(payload("\(marker)\0/usr/bin\0\0"), marker: marker), ["PATH": "/usr/bin"])
        XCTAssertNil(ExecutableLocator.parse(payload("/usr/bin\0/tmp/agent.sock\0"), marker: marker), "missing marker")
        XCTAssertNil(ExecutableLocator.parse(payload("\(marker)\0/usr/bin\0"), marker: marker), "truncated payload")
        XCTAssertEqual(
            ExecutableLocator.parse(payload("\(marker)\0/usr/bin:/Applications/My Tools/bin:~/.dotnet/tools\0\0"), marker: marker),
            ["PATH": "/usr/bin:/Applications/My Tools/bin:~/.dotnet/tools"]
        )
        XCTAssertNil(ExecutableLocator.parse(payload("\(marker)\0Welcome\n/usr/bin\0\0"), marker: marker), "PATH with startup output")
        XCTAssertNil(ExecutableLocator.parse(payload("\(marker)\0\0\0"), marker: marker), "empty PATH")
    }

    func testKeepsBaseEnvironmentWhenLoginShellFails() async {
        let failing = ExecutableLocator(runner: FakeRunner { _ in CommandResult(exitStatus: 1) })
        let base = ["PATH": "/usr/bin", "SSH_AUTH_SOCK": "/launchd/agent"]
        let afterFailure = await failing.environment(base: base)
        XCTAssertEqual(afterFailure, base)

        let unmarked = ExecutableLocator(runner: FakeRunner { _ in CommandResult(exitStatus: 0, standardOutput: Data("/opt/homebrew/bin\0\0".utf8)) })
        let afterUnmarked = await unmarked.environment(base: base)
        XCTAssertEqual(afterUnmarked, base)
    }
}
