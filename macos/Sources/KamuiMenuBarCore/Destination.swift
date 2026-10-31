import Foundation

/// Validates an OpenSSH destination before it reaches the kamui CLI, which
/// performs the same checks.
public enum Destination {
    public static func validate(_ raw: String) throws -> String {
        let value = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if value.isEmpty {
            throw KamuiError.invalidDestination("Enter an SSH destination, such as an alias from ~/.ssh/config or user@host.")
        }
        if value.hasPrefix("-") {
            throw KamuiError.invalidDestination("SSH destinations cannot begin with \"-\".")
        }
        if value.unicodeScalars.contains(where: { CharacterSet.whitespacesAndNewlines.contains($0) || CharacterSet.controlCharacters.contains($0) }) {
            throw KamuiError.invalidDestination("SSH destinations cannot contain spaces or control characters.")
        }
        return value
    }
}
