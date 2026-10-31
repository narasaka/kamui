import Foundation
import XCTest
@testable import KamuiMenuBarCore

final class SSHConfigHostsTests: XCTestCase {
    func testListsConcreteAliasesInOrder() {
        let config = """
        # Personal machines
        Host foo bar
            User narasaka
            LocalForward 3000 localhost:3000
        Host *.internal !secret
        host=baz
        Host = spaced
        Host "quoted"  foo   # duplicate is ignored
        Match host foo
        Host -oProxyCommand=evil
        Host *
            ServerAliveInterval 15
        """
        XCTAssertEqual(SSHConfigHosts.parse(config), ["foo", "bar", "baz", "spaced", "quoted"])
    }

    func testFollowsIncludesInPlace() throws {
        let home = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let ssh = home.appendingPathComponent(".ssh")
        try FileManager.default.createDirectory(at: ssh.appendingPathComponent("config.d"), withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: home) }
        try "Include config.d/*\nHost main\nInclude ~/.ssh/config\n".write(to: ssh.appendingPathComponent("config"), atomically: true, encoding: .utf8)
        try "Host work-box\n".write(to: ssh.appendingPathComponent("config.d/b-work"), atomically: true, encoding: .utf8)
        try "Host lab\n".write(to: ssh.appendingPathComponent("config.d/a-lab"), atomically: true, encoding: .utf8)

        XCTAssertEqual(SSHConfigHosts.load(home: home.path), ["lab", "work-box", "main"])
    }

    func testMissingConfigHasNoHosts() {
        XCTAssertEqual(SSHConfigHosts.load(home: "/nonexistent-\(UUID().uuidString)"), [])
    }
}

final class TerminalHandoffTests: XCTestCase {
    func testScriptRunsKamuiWithQuotedArguments() throws {
        let script = try TerminalHandoff.script(executable: URL(fileURLWithPath: "/Users/o'neil/go/bin/kamui"), destination: "o'neil@host")
        XCTAssertEqual(script, "#!/bin/sh\n'/Users/o'\\''neil/go/bin/kamui' mirror 'o'\\''neil@host'\n")
    }

    func testScriptRejectsOptionLikeDestination() {
        XCTAssertThrowsError(try TerminalHandoff.script(executable: URL(fileURLWithPath: "/usr/local/bin/kamui"), destination: "-oProxyCommand=evil"))
    }

    func testWritesPrivateExecutableCommandFile() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }

        let file = try TerminalHandoff.writeScript(executable: URL(fileURLWithPath: "/usr/local/bin/kamui"), destination: "foo", directory: directory)

        XCTAssertEqual(file.pathExtension, "command")
        let permissions = try FileManager.default.attributesOfItem(atPath: file.path)[.posixPermissions] as? Int
        XCTAssertEqual(permissions, 0o700)
        XCTAssertEqual(try String(contentsOf: file, encoding: .utf8), "#!/bin/sh\n'/usr/local/bin/kamui' mirror 'foo'\n")
    }

    func testRemovesOnlyStaleScripts() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let kamui = URL(fileURLWithPath: "/usr/local/bin/kamui")
        let old = try TerminalHandoff.writeScript(executable: kamui, destination: "old", directory: directory)
        let recent = try TerminalHandoff.writeScript(executable: kamui, destination: "recent", directory: directory)
        try FileManager.default.setAttributes([.modificationDate: Date().addingTimeInterval(-3600)], ofItemAtPath: old.path)

        let new = try TerminalHandoff.writeScript(executable: kamui, destination: "new", directory: directory)

        XCTAssertFalse(FileManager.default.fileExists(atPath: old.path))
        XCTAssertTrue(FileManager.default.fileExists(atPath: recent.path))
        XCTAssertTrue(FileManager.default.fileExists(atPath: new.path))
    }
}
