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
