import Foundation

public enum KamuiError: Error, Equatable, LocalizedError, Sendable {
    case executableNotFound
    case invalidDestination(String)
    case commandFailed(String)
    case timedOut(String)
    case malformedStatus(String)
    case unsupportedStatusFormat(Int)

    public var errorDescription: String? {
        switch self {
        case .executableNotFound:
            "The kamui command-line tool was not found. Install it with `brew install narasaka/tap/kamui` or choose its location."
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
        }
    }
}
