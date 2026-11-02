import Foundation

/// One OpenSSH question relayed by `kamui mirror --json-prompts`.
public struct SSHPrompt: Equatable, Sendable {
    public enum Kind: String, Sendable {
        /// Typed input, always hidden. Prompt text comes partly from the
        /// server, so it never decides whether input is shown.
        case secret
        /// A yes-or-no question.
        case confirm
        /// Information, such as "touch your security key", that needs no
        /// answer.
        case notice
    }

    public var id: Int
    public var kind: Kind
    public var text: String

    public init(id: Int, kind: Kind, text: String) {
        self.id = id
        self.kind = kind
        self.text = text
    }
}

/// The user's response to a prompt.
public enum SSHPromptReply: Equatable, Sendable {
    case answer(String)
    case decline
}

/// Answers prompts for one connection. `answer` is cancelled when kamui no
/// longer needs the answer, for example because the attempt timed out or the
/// session was stopped.
public struct SSHPromptHandler: Sendable {
    public var answer: @Sendable (SSHPrompt) async -> SSHPromptReply
    public var notice: @Sendable (String) -> Void

    public init(answer: @escaping @Sendable (SSHPrompt) async -> SSHPromptReply, notice: @escaping @Sendable (String) -> Void = { _ in }) {
        self.answer = answer
        self.notice = notice
    }
}

/// The JSON lines that `kamui mirror --json-prompts` writes and reads.
enum PromptProtocol {
    enum Message: Equatable {
        case prompt(SSHPrompt)
        case cancel(Int)
    }

    private struct Line: Decodable {
        struct Prompt: Decodable {
            var id: Int
            var kind: String
            var text: String
        }

        var prompt: Prompt?
        var cancel: Int?
    }

    private struct Reply: Encodable {
        var id: Int
        var answer: String?
        var declined: Bool?
    }

    /// Decodes one line, or returns nil for anything else. An unknown kind is
    /// treated as hidden input.
    static func message(from line: String) -> Message? {
        guard let line = try? JSONDecoder().decode(Line.self, from: Data(line.utf8)) else { return nil }
        if let prompt = line.prompt {
            return .prompt(SSHPrompt(id: prompt.id, kind: SSHPrompt.Kind(rawValue: prompt.kind) ?? .secret, text: prompt.text))
        }
        if let cancel = line.cancel, cancel > 0 {
            return .cancel(cancel)
        }
        return nil
    }

    /// Encodes a reply as one line. The answer is never logged.
    static func line(replying reply: SSHPromptReply, to id: Int) -> Data {
        let value: Reply
        switch reply {
        case .answer(let answer): value = Reply(id: id, answer: answer)
        case .decline: value = Reply(id: id, declined: true)
        }
        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys
        var data = (try? encoder.encode(value)) ?? Data()
        data.append(UInt8(ascii: "\n"))
        return data
    }
}

/// Answers each prompt in its own task, so kamui can cancel one.
final class PromptTasks: @unchecked Sendable {
    private let lock = NSLock()
    private var tasks: [Int: Task<Void, Never>] = [:]

    func start(_ id: Int, operation: @escaping @Sendable () async -> Void) {
        let task = Task { await operation() }
        lock.withLock { tasks[id] = task }
    }

    func cancel(_ id: Int) {
        lock.withLock { tasks.removeValue(forKey: id) }?.cancel()
    }

    func cancelAll() {
        let all = lock.withLock {
            defer { tasks.removeAll() }
            return Array(tasks.values)
        }
        all.forEach { $0.cancel() }
    }
}
