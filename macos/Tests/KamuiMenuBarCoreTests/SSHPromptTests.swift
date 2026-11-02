import Foundation
import XCTest
@testable import KamuiMenuBarCore

final class PromptProtocolTests: XCTestCase {
    func testDecodesPromptsAndCancellations() {
        XCTAssertEqual(
            PromptProtocol.message(from: #"{"prompt":{"id":1,"kind":"secret","text":"Enter passphrase for key '/k': "}}"#),
            .prompt(SSHPrompt(id: 1, kind: .secret, text: "Enter passphrase for key '/k': "))
        )
        XCTAssertEqual(PromptProtocol.message(from: #"{"prompt":{"id":2,"kind":"confirm","text":"Allow?"}}"#), .prompt(SSHPrompt(id: 2, kind: .confirm, text: "Allow?")))
        XCTAssertEqual(PromptProtocol.message(from: #"{"prompt":{"id":3,"kind":"future","text":"?"}}"#), .prompt(SSHPrompt(id: 3, kind: .secret, text: "?")), "unknown kinds stay hidden")
        XCTAssertEqual(PromptProtocol.message(from: #"{"cancel":4}"#), .cancel(4))
        XCTAssertNil(PromptProtocol.message(from: "reyna mirroring enabled"))
        XCTAssertNil(PromptProtocol.message(from: "{}"))
    }

    func testEncodesRepliesAsOneLine() {
        XCTAssertEqual(String(decoding: PromptProtocol.line(replying: .answer("open \"sesame\""), to: 2), as: UTF8.self), #"{"answer":"open \"sesame\"","id":2}"# + "\n")
        XCTAssertEqual(String(decoding: PromptProtocol.line(replying: .decline, to: 3), as: UTF8.self), #"{"declined":true,"id":3}"# + "\n")
    }
}

final class PromptRelayTests: XCTestCase {
    /// A stand-in for `kamui mirror --json-prompts` that checks each reply on
    /// its standard input and, like the real CLI, may leave a background
    /// process holding its output.
    func fakeKamui(_ body: String) throws -> URL {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
        let script = directory.appendingPathComponent("kamui")
        try ("#!/bin/sh\n" + body).write(to: script, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: script.path)
        return script
    }

    func testConnectAnswersPromptsThroughStandardInput() async throws {
        let kamui = try fakeKamui(#"""
        [ "$1 $2 $3" = "mirror --json-prompts reyna" ] || { echo "arguments: $*" >&2; exit 2; }
        (sleep 30 &)
        echo '{"prompt":{"id":1,"kind":"notice","text":"Confirm user presence for key"}}'
        echo '{"prompt":{"id":2,"kind":"secret","text":"Enter passphrase for key '"'"'/k'"'"': "}}'
        read -r reply
        [ "$reply" = '{"answer":"open sesame","id":2}' ] || { echo "reply 2: $reply" >&2; exit 3; }
        echo '{"prompt":{"id":3,"kind":"secret","text":"Password: "}}'
        sleep 1
        echo '{"cancel":3}'
        echo '{"prompt":{"id":4,"kind":"confirm","text":"Allow use of key?"}}'
        read -r reply
        [ "$reply" = '{"declined":true,"id":4}' ] || { echo "reply 4: $reply" >&2; exit 4; }
        echo 'WARNING: SSH agent forwarding is enabled for reyna' >&2
        """#)
        let asked = Collector<SSHPrompt>()
        let cancelled = Collector<Int>()
        let notices = Collector<String>()
        let client = KamuiClient(executable: kamui, environment: [:])

        let warnings = try await client.connect("reyna", prompts: SSHPromptHandler(
            answer: { prompt in
                asked.append(prompt)
                switch prompt.id {
                case 2:
                    return .answer("open sesame")
                case 3:
                    // Waits until kamui cancels the prompt.
                    try? await Task.sleep(for: .seconds(30))
                    if Task.isCancelled { cancelled.append(prompt.id) }
                    return .answer("too late")
                default:
                    return .decline
                }
            },
            notice: { notices.append($0) }
        ))

        XCTAssertEqual(warnings, ["WARNING: SSH agent forwarding is enabled for reyna"])
        XCTAssertEqual(asked.values.map(\.id), [2, 3, 4])
        XCTAssertEqual(asked.values.map(\.kind), [.secret, .secret, .confirm])
        XCTAssertEqual(cancelled.values, [3])
        XCTAssertEqual(notices.values, ["Confirm user presence for key"])
    }

    func testOlderKamuiWithoutJSONPromptsIsReported() async throws {
        let kamui = try fakeKamui("echo 'Incorrect Usage: flag provided but not defined: -json-prompts' >&2; exit 1\n")
        let client = KamuiClient(executable: kamui, environment: [:])

        do {
            _ = try await client.connect("reyna", prompts: SSHPromptHandler { _ in .decline })
            XCTFail("connect succeeded")
        } catch {
            XCTAssertEqual(error as? KamuiError, .promptsUnsupported(kamui.path))
        }
    }

    func testTimeoutPausesWhileInputHoldsIt() async throws {
        let input = CommandInput()
        let started = ContinuousClock.now
        let result = try await ProcessRunner().run(
            URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", "echo ask; read -r answer; echo \"got $answer\" >&2"],
            environment: [:],
            timeout: .milliseconds(500),
            io: CommandIO(onStandardOutputLine: { line in
                guard line == "ask" else { return }
                input.holdTimeout()
                Task {
                    // The user takes longer than the timeout to answer.
                    try? await Task.sleep(for: .seconds(1))
                    try? input.write(Data("42\n".utf8))
                    input.releaseTimeout()
                }
            }, input: input)
        )
        XCTAssertEqual(result.standardError, "got 42\n")
        XCTAssertGreaterThan(ContinuousClock.now - started, .milliseconds(900))
        XCTAssertThrowsError(try input.write(Data("late\n".utf8)), "input stays open after the command exits")
    }
}

final class SharedSessionTests: XCTestCase {
    /// The app shows whatever the shared controller reports, so sessions
    /// started or stopped from a terminal appear and disappear on the next
    /// poll, and the app's stop goes to the same controller.
    func testStatusFollowsSessionsChangedFromTheTerminal() async throws {
        let documents = Collector<String>()
        documents.append(#"{"format":1,"sessions":[{"destination":"started-in-terminal","state":"connected","mirror":{"enabled":true,"mirroredPorts":[3000],"excludedPorts":[],"conflicts":[]}}]}"#)
        documents.append(#"{"format":1,"sessions":[]}"#)
        let calls = Collector<Int>()
        let runner = FakeRunner { arguments in
            guard arguments.first == "status" else { return CommandResult(exitStatus: 0) }
            calls.append(0)
            return CommandResult(exitStatus: 0, standardOutput: Data(documents.values[min(calls.values.count, 2) - 1].utf8))
        }
        let client = KamuiClient(executable: URL(fileURLWithPath: "/opt/homebrew/bin/kamui"), environment: [:], runner: runner)

        let before = try await client.status()
        XCTAssertEqual(before.sessions.map(\.destination), ["started-in-terminal"])
        XCTAssertEqual(before.sessions.first?.mirror.mirroredPorts, [3000])
        try await client.stop("started-in-terminal")
        let after = try await client.status()
        XCTAssertEqual(after.sessions, [])
        XCTAssertEqual(runner.invocations.map(\.arguments), [["status", "--json"], ["stop", "started-in-terminal"], ["status", "--json"]])
    }
}
