import Foundation
import XCTest
@testable import KamuiMenuBarCore

final class StatusTests: XCTestCase {
    func testDecodesCLIStatusDocument() throws {
        let json = """
        {
          "format": 1,
          "sessions": [
            {
              "destination": "reyna",
              "state": "authentication-required",
              "lastError": "SSH authentication failed for reyna",
              "mirror": {
                "enabled": true,
                "mirroredPorts": [3000, 5173],
                "excludedPorts": [22],
                "conflicts": [{"port": 5432, "error": "address already in use"}],
                "lastError": "discovery stalled"
              }
            },
            {
              "destination": "nathan@192.168.1.50",
              "state": "connected",
              "browser": "firefox",
              "futureField": true,
              "mirror": {"enabled": false, "mirroredPorts": [], "excludedPorts": [], "conflicts": []}
            }
          ]
        }
        """
        let document = try StatusDocument.decode(Data(json.utf8))
        XCTAssertEqual(document.sessions, [
            Session(
                destination: "reyna", state: .authenticationRequired, lastError: "SSH authentication failed for reyna",
                mirror: Mirror(enabled: true, mirroredPorts: [3000, 5173], excludedPorts: [22],
                               conflicts: [Conflict(port: 5432, error: "address already in use")], lastError: "discovery stalled")
            ),
            Session(destination: "nathan@192.168.1.50", state: .connected, browser: "firefox"),
        ])
    }

    func testDecodesGoldenOutputFromGoCLI() throws {
        // internal/cliapp/status_test.go asserts that kamui prints this file.
        let fixture = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("internal/cliapp/testdata/status.json")
        let document = try StatusDocument.decode(try Data(contentsOf: fixture))
        XCTAssertEqual(document.sessions, [
            Session(
                destination: "reyna", state: .authenticationRequired, lastError: "SSH authentication failed for reyna",
                mirror: Mirror(enabled: true, mirroredPorts: [3000, 5173], excludedPorts: [],
                               conflicts: [Conflict(port: 5432, error: "address already in use")], lastError: "discovery stalled")
            ),
        ])
    }

    func testRejectsNewerStatusFormat() {
        XCTAssertThrowsError(try StatusDocument.decode(Data(#"{"format": 2, "sessions": []}"#.utf8))) { error in
            XCTAssertEqual(error as? KamuiError, .unsupportedStatusFormat(2))
        }
    }

    func testUnknownStatesRemainVisible() throws {
        let document = try StatusDocument.decode(Data(#"{"format":1,"sessions":[{"destination":"a","state":"paused","mirror":{"enabled":false,"mirroredPorts":[],"excludedPorts":[],"conflicts":[]}}]}"#.utf8))
        XCTAssertEqual(document.sessions.first?.state, .unknown("paused"))
        XCTAssertEqual(document.sessions.first?.state.label, "paused")
    }

    func testIndicatorPrioritizesProblems() {
        let connected = Session(destination: "a", state: .connected)
        let connecting = Session(destination: "b", state: .connecting)
        let needsAuth = Session(destination: "c", state: .authenticationRequired)
        var mirrorFailure = connected
        mirrorFailure.mirror.lastError = "discovery failed"

        XCTAssertEqual(Indicator(sessions: []), .idle)
        XCTAssertEqual(Indicator(sessions: [connected]), .connected)
        XCTAssertEqual(Indicator(sessions: [connected], pending: ["b"]), .connecting)
        XCTAssertEqual(Indicator(sessions: [connected, connecting]), .connecting)
        XCTAssertEqual(Indicator(sessions: [connecting, needsAuth]), .attention)
        XCTAssertEqual(Indicator(sessions: [mirrorFailure]), .attention)
        XCTAssertEqual(Indicator(sessions: [connected], failed: true), .attention)
    }
}

final class DestinationTests: XCTestCase {
    func testAcceptsOpenSSHDestinationForms() throws {
        for destination in ["foo", "nathan@192.168.1.50", "nathan@foo.example.com", "monitoring"] {
            XCTAssertEqual(try Destination.validate(destination), destination)
        }
        XCTAssertEqual(try Destination.validate("  foo\n"), "foo")
    }

    func testRejectsValuesOpenSSHWouldReadAsOptions() {
        for destination in ["", "   ", "-oProxyCommand=evil", "foo bar", "foo\tbar", "foo\u{7}"] {
            XCTAssertThrowsError(try Destination.validate(destination), destination)
        }
    }
}
