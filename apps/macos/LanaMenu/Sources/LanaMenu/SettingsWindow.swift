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
            window.title = "lana Menu Settings"
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
                Text("Empty fields use the defaults: LANA_HOME from the environment or ~/.lana, and lana from your login shell PATH, ~/.local/bin, ~/go/bin, /opt/homebrew/bin, or /usr/local/bin.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

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
