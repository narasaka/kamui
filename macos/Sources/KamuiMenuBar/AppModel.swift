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
    /// OpenSSH notices, such as "Confirm user presence for key", shown while
    /// a connection waits.
    @Published private(set) var notices: [String: String] = [:]
    @Published private(set) var hosts: [String] = []
    @Published private(set) var problem: String?
    @Published private(set) var executable: URL?
    @Published private(set) var launchesAtLogin = SMAppService.mainApp.status == .enabled

    /// Connections the user stopped while they were starting, whose failure
    /// is expected rather than reported.
    private var stopping: Set<String> = []
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

    /// The kamui inside this app, when the app runs from a bundle.
    let bundledExecutable: URL? = {
        let url = ExecutableLocator.bundledExecutable(in: Bundle.main.bundleURL)
        return FileManager.default.isExecutableFile(atPath: url.path) ? url : nil
    }()

    var usesBundledExecutable: Bool {
        executable != nil && executable == bundledExecutable
    }

    func resolveExecutable() async {
        let locator = ExecutableLocator()
        environment = await locator.environment(base: ProcessInfo.processInfo.environment)
        locateExecutable()
        reloadHosts()
    }

    /// Finds kamui again on every refresh, so that the app follows a CLI the
    /// user installs or removes and keeps using the same build as the
    /// terminal.
    private func locateExecutable() {
        let located = ExecutableLocator().locate(
            override: UserDefaults.standard.string(forKey: Self.executableOverrideKey),
            environment: environment,
            bundled: bundledExecutable
        )
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
        locateExecutable()
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
        guard !pending.contains(destination) else { return }
        pending.insert(destination)
        notices[destination] = nil
        let prompts = SSHPromptHandler(
            answer: { prompt in
                let reply = await PromptPanel.ask(prompt, destination: destination)
                if case .decline = reply, prompt.kind != .confirm, !Task.isCancelled {
                    // Cancel means stop connecting. Stopping first ends the
                    // attempt, so OpenSSH never tries an empty answer and
                    // asks again.
                    await self.stopAndWait(destination)
                }
                return reply
            },
            notice: { text in Task { @MainActor in self.notices[destination] = text } }
        )
        Task {
            defer {
                pending.remove(destination)
                notices[destination] = nil
                stopping.remove(destination)
            }
            do {
                let warnings = try await client.connect(destination, prompts: prompts) { url in
                    // A Tailscale SSH check waits until the user signs in.
                    Task { @MainActor in self.openSignIn(url, for: destination) }
                }
                if stopping.contains(destination) {
                    // Stop was requested before this connection reached the
                    // controller, so it had nothing to stop then.
                    try? await client.stop(destination)
                } else if !warnings.isEmpty {
                    showDetails(title: "Connected to \(destination)", detail: warnings.joined(separator: "\n"))
                }
            } catch where !stopping.contains(destination) {
                showDetails(title: "Could not connect to \(destination)", detail: error.localizedDescription)
            } catch {}
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
        Task { await stopAndWait(destination) }
    }

    func stopAndWait(_ destination: String) async {
        guard let client else { return }
        if pending.contains(destination) {
            stopping.insert(destination)
        }
        do {
            try await client.stop(destination)
        } catch where stopping.contains(destination) {
            // The connection has not reached the controller yet; connect
            // stops it when it finishes.
        } catch {
            presentError(error)
        }
        await refresh()
    }

    func stopAll() {
        guard let client else { return }
        stopping.formUnion(pending)
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

    private func openSignIn(_ url: URL, for destination: String) {
        if KamuiClient.isTailscaleLogin(url) {
            NSWorkspace.shared.open(url)
            return
        }
        let alert = NSAlert()
        alert.messageText = "Sign in to connect to \(destination)?"
        alert.informativeText = "\(destination) asks you to sign in at:\n\n\(url.absoluteString)\n\nOpen it only if you expect this host to ask for a sign-in."
        alert.addButton(withTitle: "Open Link")
        alert.addButton(withTitle: "Cancel")
        NSApp.activate(ignoringOtherApps: true)
        if alert.runModal() == .alertFirstButtonReturn {
            NSWorkspace.shared.open(url)
        }
    }

    func chooseExecutable() {
        let panel = NSOpenPanel()
        panel.title = "Choose the kamui executable"
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.showsHiddenFiles = true
        panel.treatsFilePackagesAsDirectories = true
        panel.directoryURL = URL(fileURLWithPath: "/opt/homebrew/bin")
        NSApp.activate(ignoringOtherApps: true)
        guard panel.runModal() == .OK, let url = panel.url else { return }
        UserDefaults.standard.set(url.path, forKey: Self.executableOverrideKey)
        locateExecutable()
        Task { await refresh() }
    }

    func useBundledExecutable() {
        guard let bundledExecutable else { return }
        UserDefaults.standard.set(bundledExecutable.path, forKey: Self.executableOverrideKey)
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

    private func presentError(_ error: Error) {
        showDetails(title: "Kamui", detail: error.localizedDescription)
    }
}
