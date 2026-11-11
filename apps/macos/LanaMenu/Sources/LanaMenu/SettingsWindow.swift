import AppKit
import LanaMenuCore
import SwiftUI

/// SettingsWindow is a small AppKit-owned window, so it opens in front even
/// though the app has no Dock icon.
enum SettingsWindow {
    private static var window: NSWindow?

    @MainActor
    static func show(model: AppModel) {
        if window == nil {
            let host = NSHostingController(rootView: SettingsView(model: model))
            let window = NSWindow(contentViewController: host)
            window.title = AppVariant.current == .dev ? "lana dev Settings" : "lana Menu Settings"
            window.styleMask = [.titled, .closable]
            window.isReleasedWhenClosed = false
            window.center()
            self.window = window
        }
        NSApp.activate(ignoringOtherApps: true)
        window?.makeKeyAndOrderFront(nil)
    }
}

struct SettingsView: View {
    @ObservedObject var model: AppModel
    @State private var home: String
    @State private var binary: String

    init(model: AppModel) {
        self.model = model
        _home = State(initialValue: model.settings.homeOverride ?? "")
        _binary = State(initialValue: model.settings.binaryOverride ?? "")
    }

    var body: some View {
        Form {
            Section {
                LabeledContent("LANA_HOME") {
                    TextField("LANA_HOME", text: $home, prompt: Text(model.settings.defaultHome))
                        .labelsHidden()
                        .onChange(of: home) { value in
                            model.settings.homeOverride = value.trimmingCharacters(in: .whitespaces)
                            model.settingsChanged()
                        }
                }
                LabeledContent("lana binary") {
                    HStack {
                        TextField("lana binary", text: $binary, prompt: Text("Find automatically"))
                            .labelsHidden()
                            .onChange(of: binary) { value in
                                model.settings.binaryOverride = value.trimmingCharacters(in: .whitespaces)
                                model.settingsChanged()
                            }
                        Button("Choose…") { chooseBinary() }
                    }
                }
                LabeledContent("Starts") {
                    Text(binaryLine)
                        .font(.callout)
                        .foregroundStyle(model.binary.path == nil ? Color.orange : Color.secondary)
                        .textSelection(.enabled)
                        .lineLimit(2)
                }
            } footer: {
                Text("Empty fields use the defaults: LANA_HOME from the environment or ~/.lana, and lana from Lana.app, your login shell PATH, ~/.local/bin, ~/go/bin, /opt/homebrew/bin, or /usr/local/bin.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            WindowSection(settings: model.settings)

            NotificationSection(notifier: model.notifier)

            Section {
                Toggle("Open lana Menu at login", isOn: Binding(
                    get: { model.launchAtLogin },
                    set: { model.setLaunchAtLogin($0) }
                ))
                .disabled(!model.canLaunchAtLogin)
                if !model.canLaunchAtLogin {
                    Text("Available when the app runs from Lana.app (make macos-app).")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Button("Show Daemon Log") { model.revealLog() }
            }
        }
        .formStyle(.grouped)
        .frame(width: 520)
        .fixedSize(horizontal: false, vertical: true)
    }

    private var binaryLine: String {
        guard let path = model.binary.path else {
            return model.binary.source == "setting" ? "Not an executable file" : "lana not found"
        }
        return "\(path) (\(model.binary.source))"
    }

    private func chooseBinary() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.treatsFilePackagesAsDirectories = true
        panel.message = "Choose the lana binary"
        if panel.runModal() == .OK, let url = panel.url {
            binary = url.path
        }
    }
}

/// WindowSection holds the lana window settings of spec #52.
struct WindowSection: View {
    let settings: AppSettings
    @State private var openInWindow: Bool
    @State private var windowAtLaunch: Bool

    init(settings: AppSettings) {
        self.settings = settings
        _openInWindow = State(initialValue: settings.openInWindow)
        _windowAtLaunch = State(initialValue: settings.windowAtLaunch)
    }

    var body: some View {
        Section {
            Toggle("Open pages and notifications in the lana window", isOn: $openInWindow)
                .onChange(of: openInWindow) { settings.openInWindow = $0 }
            Toggle("Open the lana window when Lana.app starts", isOn: $windowAtLaunch)
                .onChange(of: windowAtLaunch) { settings.windowAtLaunch = $0 }
        } header: {
            Text("Window")
        } footer: {
            Text("Off sends Chat, Workflows, Calendar, and notification clicks to the browser. A start at login never opens the window.")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }
}

/// NotificationSection holds the notification toggles of spec #42 and the
/// permission and subscription state.
struct NotificationSection: View {
    @ObservedObject var notifier: Notifier

    var body: some View {
        Section {
            Toggle("Messages", isOn: $notifier.settings.messages)
            Toggle("Show message preview", isOn: $notifier.settings.showPreview)
                .disabled(!notifier.settings.messages && !notifier.settings.questions)
            Toggle("Approvals", isOn: $notifier.settings.approvals)
            Toggle("Questions", isOn: $notifier.settings.questions)
            Toggle("Workflow started (silent)", isOn: $notifier.settings.workflowStarted)
            Toggle("Workflow finished or failed", isOn: $notifier.settings.workflowFinished)
            LabeledContent("Permission") {
                HStack {
                    Text(permissionLine)
                        .foregroundStyle(permissionOK ? Color.secondary : Color.orange)
                    Button("Open System Settings…") { notifier.openSystemSettings() }
                }
            }
            LabeledContent("Daemon events") {
                Text(subscriptionLine)
                    .font(.callout)
                    .foregroundStyle(notifier.subscription.state == "subscribed" ? Color.secondary : Color.orange)
                    .lineLimit(2)
                    .textSelection(.enabled)
            }
        } header: {
            Text("Notifications")
        } footer: {
            Text(footer)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .onAppear { notifier.refreshPermission() }
    }

    private var permissionOK: Bool {
        notifier.permission == "authorized" || notifier.permission == "provisional"
    }

    private var permissionLine: String {
        switch notifier.permission {
        case "authorized": return "Allowed"
        case "provisional": return "Allowed quietly"
        case "denied": return "Denied"
        case "not_determined": return "Not asked yet"
        case "unavailable": return "Needs Lana.app"
        default: return "Checking…"
        }
    }

    private var subscriptionLine: String {
        let s = notifier.subscription
        switch s.state {
        case "subscribed": return "Connected" + (s.addr.map { " to " + $0 } ?? "") + " · \(s.received) received"
        case "waiting_for_daemon": return "Waiting for lana to start"
        case "connecting": return "Connecting…"
        case "retrying": return "Reconnecting" + (s.detail.map { ": " + $0 } ?? "")
        default: return s.state
        }
    }

    private var footer: String {
        if notifier.permission == "denied" {
            return "macOS blocks lana's notifications. Turn on Allow notifications for lana in System Settings → Notifications."
        }
        if let error = notifier.lastError {
            return error
        }
        return "A reply does not notify while its chat is open in a dashboard tab. Telegram chats notify only for approvals and questions."
    }
}
