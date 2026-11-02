import Foundation

public enum KamuiError: Error, Equatable, LocalizedError, Sendable {
    case executableNotFound
    case invalidDestination(String)
    case commandFailed(String)
    case timedOut(String)
    case malformedStatus(String)
    case unsupportedStatusFormat(Int)
    case promptsUnsupported(String)
    case commandFinished

    public var errorDescription: String? {
        switch self {
        case .executableNotFound:
            "Kamui could not find the kamui command-line tool. Reinstall Kamui or choose the tool's location under kamui Executable."
        case .invalidDestination(let reason):
            reason
        case .commandFailed(let message):
            message
        case .timedOut(let command):
            "`\(command)` did not finish in time."
        case .malformedStatus(let detail):
            "kamui returned status this app could not read: \(detail)"
        case .unsupportedStatusFormat(let format):
            "kamui reports status format \(format), which this app does not understand. Update the Kamui app."
        case .promptsUnsupported(let path):
            "The kamui at \(path) is older than this app and can't pass SSH prompts to it. Update it, or choose Use Bundled kamui under kamui Executable."
        case .commandFinished:
            "kamui finished before the answer could be sent."
        }
    }
}
