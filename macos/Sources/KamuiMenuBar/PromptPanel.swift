import AppKit
import KamuiMenuBarCore

/// Asks the user one OpenSSH question in a floating alert window.
///
/// The window is not modal, so polling, other connections, and kamui's
/// cancellation keep working while it is open. Typed answers always use a
/// secure text field: prompt text comes partly from the server, so it never
/// decides whether input is shown. Answers live only in memory until they are
/// written to kamui's standard input.
@MainActor
final class PromptPanel: NSObject {
    private let alert = NSAlert()
    private var field: NSSecureTextField?
    private var continuation: CheckedContinuation<SSHPromptReply, Never>?

    /// Shows the prompt and returns the user's reply. Cancelling the calling
    /// task closes the window and returns `.decline`.
    static func ask(_ prompt: SSHPrompt, destination: String) async -> SSHPromptReply {
        let panel = PromptPanel(prompt: prompt, destination: destination)
        return await withTaskCancellationHandler {
            await withCheckedContinuation { continuation in
                panel.present(continuation)
            }
        } onCancel: {
            Task { @MainActor in panel.finish(.decline) }
        }
    }

    private init(prompt: SSHPrompt, destination: String) {
        super.init()
        alert.messageText = Self.title(for: prompt, destination: destination)
        alert.informativeText = prompt.text.trimmingCharacters(in: .whitespacesAndNewlines)
        switch prompt.kind {
        case .confirm:
            alert.addButton(withTitle: "Allow")
            alert.addButton(withTitle: "Deny")
        default:
            let field = NSSecureTextField(frame: NSRect(x: 0, y: 0, width: 300, height: 24))
            alert.accessoryView = field
            self.field = field
            alert.addButton(withTitle: "Continue")
            alert.addButton(withTitle: "Cancel")
        }
        for (index, button) in alert.buttons.enumerated() {
            button.tag = index
            button.target = self
            button.action = #selector(buttonPressed(_:))
        }
    }

    static func title(for prompt: SSHPrompt, destination: String) -> String {
        prompt.kind == .confirm ? "\(destination) asks for permission" : "SSH needs your answer to connect to \(destination)"
    }

    private func present(_ continuation: CheckedContinuation<SSHPromptReply, Never>) {
        self.continuation = continuation
        alert.layout()
        let window = alert.window
        window.level = .floating
        window.center()
        NSApp.activate(ignoringOtherApps: true)
        window.makeKeyAndOrderFront(nil)
        if let field {
            window.makeFirstResponder(field)
        }
    }

    @objc private func buttonPressed(_ sender: NSButton) {
        guard sender.tag == 0 else { return finish(.decline) }
        finish(.answer(field?.stringValue ?? "yes"))
    }

    private func finish(_ reply: SSHPromptReply) {
        guard let continuation else { return }
        self.continuation = nil
        field?.stringValue = ""
        alert.window.orderOut(nil)
        continuation.resume(returning: reply)
    }
}
