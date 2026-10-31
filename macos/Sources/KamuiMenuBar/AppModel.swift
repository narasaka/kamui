import AppKit
import KamuiMenuBarCore
import ServiceManagement
import SwiftUI

@MainActor
final class AppModel: ObservableObject {
    static let executableOverrideKey = "kamuiExecutablePath"
    static let pollInterval: Duration = .seconds(3)

    @Published private(set) var sessions: [Session] = []
    @Published private(set) var pending: Set<String> = []
    @Published private(set) var hosts: [String] = []
    @Published private(set) var problem: String?
    @Published private(set) var executable: URL?
    @Published private(set) var launchesAtLogin = SMAppService.mainApp.status == .enabled

    private var environment: [String: String] = ProcessInfo.processInfo.environment
    private var refreshing = false
    private var poller: Task<Void, Never>?

    var indicator: Indicator {
        Indicator(sessions: sessions, pending: pending, failed: problem != nil)
    }

    init() {
        poller = Task { [weak self] in
            await self?.resolveExecutable()
            while !Task.isCancelled {
                guard let self else { return }
                await self.refresh()
                try? await Task.sleep(for: AppModel.pollInterval)
            }
        }
    }

    deinit { poller?.cancel() }

    private var client: KamuiClient? {
        executable.map { KamuiClient(executable: $0, environment: environment) }
    }

    var usesExecutableOverride: Bool {
        UserDefaults.standard.string(forKey: Self.executableOverrideKey) != nil
    }

    func resolveExecutable() async {
        let locator = ExecutableLocator()
        environment = await locator.environment(base: ProcessInfo.processInfo.environment)
        locateExecutable()
        reloadHosts()
    }

    private func locateExecutable() {
        let located = ExecutableLocator().locate(override: UserDefaults.standard.string(forKey: Self.executableOverrideKey), environment: environment)
        if located != executable {
            executable = located
        }
    }

    func reloadHosts() {
        let loaded = SSHConfigHosts.load()
        if loaded != hosts {
            hosts = loaded
        }
    }

    func refresh() async {
        guard !refreshing else { return }
        refreshing = true
        defer { refreshing = false }
        reloadHosts()
        let loginStatus = SMAppService.mainApp.status == .enabled
        if loginStatus != launchesAtLogin {
            launchesAtLogin = loginStatus
        }
        if executable == nil {
            locateExecutable()
        }
        guard let client else {
            update(sessions: [], problem: KamuiError.executableNotFound.localizedDescription)
            return
        }
        do {
            update(sessions: try await client.status().sessions, problem: nil)
        } catch {
            update(sessions: sessions, problem: error.localizedDescription)
        }
    }

    /// Publishes only changes so that polling does not rebuild an open menu.
    private func update(sessions: [Session], problem: String?) {
        if sessions != self.sessions {
            self.sessions = sessions
        }
        if problem != self.problem {
            self.problem = problem
        }
    }

    func connect(_ input: String) {
        guard let client else { return presentError(KamuiError.executableNotFound) }
        let destination: String
        do {
            destination = try Destination.validate(input)
        } catch {
            return presentError(error)
        }
        pending.insert(destination)
        Task {
            defer { pending.remove(destination) }
            do {
                let warnings = try await client.connect(destination)
                if !warnings.isEmpty {
                    showDetails(title: "Connected to \(destination)", detail: warnings.joined(separator: "\n"))
                }
            } catch {
                offerTerminal(destination: destination, title: "Could not connect to \(destination)", detail: error.localizedDescription)
            }
            await refresh()
        }
    }

    func promptForDestination() {
        let alert = NSAlert()
        alert.messageText = "Connect to an SSH destination"
        alert.informativeText = "Enter the value you pass to ssh: an alias from ~/.ssh/config, a hostname, or user@host."
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 280, height: 24))
        field.placeholderString = "user@host"
        alert.accessoryView = field
        alert.addButton(withTitle: "Connect")
        alert.addButton(withTitle: "Cancel")
        alert.window.initialFirstResponder = field
        NSApp.activate(ignoringOtherApps: true)
        if alert.runModal() == .alertFirstButtonReturn {
            connect(field.stringValue)
        }
    }

    func stop(_ destination: String) {
        guard let client else { return }
        Task {
            do {
                try await client.stop(destination)
            } catch {
                presentError(error)
            }
            await refresh()
        }
    }

    func stopAll() {
        guard let client else { return }
        Task {
            do {
                try await client.stopAll()
            } catch {
                presentError(error)
            }
            await refresh()
        }
    }

    func open(port: UInt16) {
        if let url = URL(string: "http://localhost:\(port)") {
            NSWorkspace.shared.open(url)
        }
    }

    func openInTerminal(_ destination: String) {
        guard let executable else { return presentError(KamuiError.executableNotFound) }
        do {
            let directory = FileManager.default.temporaryDirectory.appendingPathComponent("kamui-terminal", isDirectory: true)
            let script = try TerminalHandoff.writeScript(executable: executable, destination: destination, directory: directory)
            NSWorkspace.shared.open(script)
        } catch {
            presentError(error)
        }
    }

    func chooseExecutable() {
        let panel = NSOpenPanel()
        panel.title = "Choose the kamui executable"
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.showsHiddenFiles = true
        panel.directoryURL = URL(fileURLWithPath: "/opt/homebrew/bin")
        NSApp.activate(ignoringOtherApps: true)
        guard panel.runModal() == .OK, let url = panel.url else { return }
        UserDefaults.standard.set(url.path, forKey: Self.executableOverrideKey)
        locateExecutable()
        Task { await refresh() }
    }

    func useAutomaticExecutable() {
        UserDefaults.standard.removeObject(forKey: Self.executableOverrideKey)
        locateExecutable()
        Task { await refresh() }
    }

    func setLaunchesAtLogin(_ enabled: Bool) {
        do {
            if enabled {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
        } catch {
            presentError(error)
        }
        launchesAtLogin = SMAppService.mainApp.status == .enabled
    }

    func showDetails(title: String, detail: String) {
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = detail
        NSApp.activate(ignoringOtherApps: true)
        alert.runModal()
    }

    private func offerTerminal(destination: String, title: String, detail: String) {
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = detail + "\n\nIf OpenSSH needs a passphrase, host-key confirmation, or another prompt, connect in a terminal, where kamui asks for it."
        alert.addButton(withTitle: "Connect in Terminal")
        alert.addButton(withTitle: "Cancel")
        NSApp.activate(ignoringOtherApps: true)
        if alert.runModal() == .alertFirstButtonReturn {
            openInTerminal(destination)
        }
    }

    private func presentError(_ error: Error) {
        showDetails(title: "Kamui", detail: error.localizedDescription)
    }
}
