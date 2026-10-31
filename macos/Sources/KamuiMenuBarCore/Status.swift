import Foundation

/// The document printed by `kamui status --json`.
public struct StatusDocument: Decodable, Equatable, Sendable {
    /// The newest `format` value this app understands.
    public static let supportedFormat = 1

    public var format: Int
    public var sessions: [Session]

    public init(format: Int = StatusDocument.supportedFormat, sessions: [Session]) {
        self.format = format
        self.sessions = sessions
    }

    public static func decode(_ data: Data) throws -> StatusDocument {
        let document: StatusDocument
        do {
            document = try JSONDecoder().decode(StatusDocument.self, from: data)
        } catch {
            throw KamuiError.malformedStatus(String(describing: error))
        }
        guard document.format <= supportedFormat else {
            throw KamuiError.unsupportedStatusFormat(document.format)
        }
        return document
    }
}

public struct Session: Decodable, Equatable, Identifiable, Sendable {
    public var destination: String
    public var state: SessionState
    public var lastError: String?
    public var browser: String?
    public var mirror: Mirror

    public var id: String { destination }

    public init(destination: String, state: SessionState, lastError: String? = nil, browser: String? = nil, mirror: Mirror = Mirror()) {
        self.destination = destination
        self.state = state
        self.lastError = lastError
        self.browser = browser
        self.mirror = mirror
    }
}

public enum SessionState: Equatable, Sendable, Decodable {
    case connecting
    case connected
    case authenticationRequired
    case unavailable
    case unknown(String)

    public init(from decoder: Decoder) throws {
        let value = try decoder.singleValueContainer().decode(String.self)
        switch value {
        case "connecting": self = .connecting
        case "connected": self = .connected
        case "authentication-required": self = .authenticationRequired
        case "unavailable": self = .unavailable
        default: self = .unknown(value)
        }
    }

    public var label: String {
        switch self {
        case .connecting: "Connecting"
        case .connected: "Connected"
        case .authenticationRequired: "Authentication required"
        case .unavailable: "Unavailable"
        case .unknown(let value): value
        }
    }

    /// Whether the user must reconnect in a terminal so OpenSSH can prompt.
    public var needsTerminal: Bool {
        switch self {
        case .authenticationRequired, .unavailable: true
        default: false
        }
    }
}

public struct Mirror: Decodable, Equatable, Sendable {
    public var enabled: Bool
    public var mirroredPorts: [UInt16]
    public var excludedPorts: [UInt16]
    public var conflicts: [Conflict]
    public var lastError: String?

    public init(enabled: Bool = false, mirroredPorts: [UInt16] = [], excludedPorts: [UInt16] = [], conflicts: [Conflict] = [], lastError: String? = nil) {
        self.enabled = enabled
        self.mirroredPorts = mirroredPorts
        self.excludedPorts = excludedPorts
        self.conflicts = conflicts
        self.lastError = lastError
    }
}

public struct Conflict: Decodable, Equatable, Sendable {
    public var port: UInt16
    public var error: String?

    public init(port: UInt16, error: String? = nil) {
        self.port = port
        self.error = error
    }
}

/// The aggregate state shown by the menu bar icon.
public enum Indicator: Equatable, Sendable {
    case idle
    case connecting
    case connected
    case attention

    public init(sessions: [Session], pending: Set<String> = [], failed: Bool = false) {
        if failed || sessions.contains(where: { $0.state.needsTerminal || $0.mirror.lastError != nil }) {
            self = .attention
        } else if !pending.isEmpty || sessions.contains(where: { $0.state != .connected }) {
            self = .connecting
        } else if sessions.isEmpty {
            self = .idle
        } else {
            self = .connected
        }
    }
}
