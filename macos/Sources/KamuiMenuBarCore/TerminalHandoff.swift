import Foundation

/// Builds the script that reruns `kamui mirror DESTINATION` in a terminal.
/// kamui relays OpenSSH's passphrase, host-key, and other prompts to that
/// terminal even when the background controller has none. The app never
/// handles credentials itself.
public enum TerminalHandoff {
    /// Scripts older than this are removed when a new one is written.
    static let retention: TimeInterval = 10 * 60

    public static func script(executable: URL, destination: String) throws -> String {
        let destination = try Destination.validate(destination)
        return """
        #!/bin/sh
        \(shellQuoted(executable.path)) mirror \(shellQuoted(destination))

        """
    }

    /// Writes an executable `.command` file that opens in the user's default
    /// terminal application.
    public static func writeScript(executable: URL, destination: String, directory: URL, now: Date = Date()) throws -> URL {
        let contents = try script(executable: executable, destination: destination)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        removeStaleScripts(in: directory, now: now)
        let file = directory.appendingPathComponent("kamui-\(UUID().uuidString).command")
        try contents.write(to: file, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: file.path)
        return file
    }

    static func removeStaleScripts(in directory: URL, now: Date) {
        let fileManager = FileManager.default
        guard let files = try? fileManager.contentsOfDirectory(at: directory, includingPropertiesForKeys: [.contentModificationDateKey]) else { return }
        for file in files where file.pathExtension == "command" {
            let modified = (try? file.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate ?? now
            if now.timeIntervalSince(modified) > retention {
                try? fileManager.removeItem(at: file)
            }
        }
    }

    public static func shellQuoted(_ value: String) -> String {
        "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }
}
