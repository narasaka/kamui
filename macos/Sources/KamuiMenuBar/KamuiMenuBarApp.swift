import KamuiMenuBarCore
import SwiftUI

@main
struct KamuiMenuBarApp: App {
    @StateObject private var model = AppModel()

    var body: some Scene {
        MenuBarExtra {
            MenuContent(model: model)
        } label: {
            Image(systemName: model.indicator.symbolName)
                .accessibilityLabel("Kamui")
        }
        .menuBarExtraStyle(.menu)
    }
}

struct MenuContent: View {
    @ObservedObject var model: AppModel

    var body: some View {
        if let problem = model.problem {
            Button(Self.truncated(problem)) { model.showDetails(title: "Kamui", detail: problem) }
            Divider()
        }

        if model.sessions.isEmpty && model.pending.isEmpty {
            Text("No active sessions")
        }
        ForEach(model.sessions) { session in
            SessionMenu(model: model, session: session)
        }
        ForEach(model.pending.subtracting(model.sessions.map(\.destination)).sorted(), id: \.self) { destination in
            Text("\(destination) — Connecting…")
        }

        Divider()

        Menu("Connect") {
            ForEach(model.hosts, id: \.self) { host in
                Button(host) { model.connect(host) }
                    .disabled(model.pending.contains(host) || model.sessions.contains { $0.destination == host && $0.state == .connected })
            }
            if !model.hosts.isEmpty {
                Divider()
            }
            Button("Other Destination…") { model.promptForDestination() }
        }
        .disabled(model.executable == nil)
        Button("Stop All Sessions") { model.stopAll() }
            .disabled(model.sessions.isEmpty)

        Divider()

        Menu("kamui Executable") {
            Text(model.executable?.path ?? "Not found")
            Button("Choose…") { model.chooseExecutable() }
            Button("Find Automatically") { model.useAutomaticExecutable() }
                .disabled(!model.usesExecutableOverride)
        }
        Toggle("Launch at Login", isOn: Binding(
            get: { model.launchesAtLogin },
            set: { model.setLaunchesAtLogin($0) }
        ))
        Button("Quit Kamui") { NSApplication.shared.terminate(nil) }
            .keyboardShortcut("q")
    }

    static func truncated(_ message: String, limit: Int = 70) -> String {
        let line = message.split(separator: "\n").first.map(String.init) ?? message
        return line.count > limit ? String(line.prefix(limit - 1)) + "…" : line
    }
}

struct SessionMenu: View {
    @ObservedObject var model: AppModel
    let session: Session

    var body: some View {
        Menu {
            if session.state.needsTerminal {
                Button("Reconnect in Terminal…") { model.openInTerminal(session.destination) }
                Divider()
            }
            if session.mirror.mirroredPorts.isEmpty {
                Text(session.mirror.enabled ? "No mirrored ports" : "Mirroring is off")
            } else {
                Section("Open in Browser") {
                    ForEach(session.mirror.mirroredPorts, id: \.self) { port in
                        Button("localhost:\(String(port))") { model.open(port: port) }
                    }
                }
            }
            if !session.mirror.conflicts.isEmpty {
                Section("Ports Held Elsewhere") {
                    ForEach(session.mirror.conflicts, id: \.port) { conflict in
                        Text(String(conflict.port))
                    }
                }
            }
            if let error = session.mirror.lastError {
                Button("Mirror Error…") { model.showDetails(title: "Mirror error for \(session.destination)", detail: error) }
            }
            if let error = session.lastError {
                Button("Last Error…") { model.showDetails(title: "Last error for \(session.destination)", detail: error) }
            }
            Divider()
            Button("Stop \(session.destination)") { model.stop(session.destination) }
        } label: {
            Label("\(session.destination) — \(session.state.label)", systemImage: Self.symbol(for: session.state))
        }
    }

    static func symbol(for state: SessionState) -> String {
        switch state {
        case .connected: "circle.fill"
        case .connecting: "circle.dotted"
        default: "exclamationmark.circle"
        }
    }
}
