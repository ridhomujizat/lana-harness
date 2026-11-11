import AppKit
import LanaMenuCore
import SwiftUI

/// MenuContent is the window of the menu bar item: status, usage, pages,
/// and the daemon controls.
struct MenuContent: View {
    @ObservedObject var model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
                .padding(.horizontal, 14)
                .padding(.top, 12)
                .padding(.bottom, 10)

            if model.status.running, let usage = model.usage {
                Divider().padding(.horizontal, 10)
                UsageSection(usage: usage, cpuHistory: model.cpuHistory, ramHistory: model.ramHistory)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
            }

            if let message = model.message {
                MessageBanner(text: message) { model.message = nil }
                    .padding(.horizontal, 10)
                    .padding(.bottom, 6)
            }

            Divider().padding(.horizontal, 10)
            section {
                MenuRow(
                    title: "Open lana",
                    symbol: "macwindow.on.rectangle",
                    shortcut: "⌘O",
                    help: "Opens the dashboard in the lana window, with its own Dock icon."
                ) {
                    model.openWindow()
                }
                .keyboardShortcut("o", modifiers: .command)
                ForEach([DashboardPage.chat, .workflows, .calendar], id: \.self) { page in
                    pageRow(page)
                }
            }
            Divider().padding(.horizontal, 10)
            section {
                pageRow(.dashboard)
                pageRow(.router)
                MenuRow(
                    title: "Sign In with One-Time Link…",
                    symbol: "key",
                    enabled: model.status.running && model.status.localControl,
                    help: "Opens a one-time link that sets a new dashboard password. A browser that is already signed in goes straight to the dashboard."
                ) {
                    model.signIn(next: "#/home")
                }
            }
            Divider().padding(.horizontal, 10)
            section {
                if model.status.running {
                    MenuRow(title: "Stop lana…", symbol: "stop.circle", enabled: model.busy == nil) {
                        model.stop()
                    }
                } else {
                    MenuRow(
                        title: "Start lana",
                        symbol: "play.circle",
                        enabled: model.busy == nil && model.binary.path != nil,
                        help: model.binary.path.map { "Runs \($0) daemon" } ?? "lana binary not found; set it in Settings."
                    ) {
                        model.start()
                    }
                }
                MenuRow(title: "Settings…", symbol: "gearshape", shortcut: "⌘,") {
                    SettingsWindow.show(model: model)
                }
                .keyboardShortcut(",", modifiers: .command)
                MenuRow(title: "Quit lana Menu", symbol: "power", shortcut: "⌘Q") {
                    NSApp.terminate(nil)
                }
                .keyboardShortcut("q", modifiers: .command)
            }
        }
        .frame(width: 300)
        .background(WindowObserver { visible in
            if visible {
                model.menuOpened()
            } else {
                model.menuClosed()
            }
        })
        .onAppear { model.menuOpened() }
        .onDisappear { model.menuClosed() }
    }

    private func section<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        VStack(spacing: 0) { content() }
            .padding(.horizontal, 5)
            .padding(.vertical, 5)
    }

    private func pageRow(_ page: DashboardPage) -> some View {
        let signIn = model.optionDown && model.status.running && model.status.localControl
        return MenuRow(
            title: signIn ? "Sign In and Open \(page.title.replacingOccurrences(of: "Open ", with: ""))…" : page.title,
            symbol: signIn ? "key" : page.symbol,
            enabled: model.status.running && model.status.webEnabled,
            help: (model.settings.openInWindow ? "Opens \(page.hash) in the lana window." : "Opens \(model.status.dashboardBase)\(page.hash).") + " Hold ⌥ to sign in to the browser with a one-time link first."
        ) {
            model.open(page)
        }
    }

    private var header: some View {
        HStack(spacing: 10) {
            Image(nsImage: MarkImages.tile)
                .resizable()
                .frame(width: 32, height: 32)
            VStack(alignment: .leading, spacing: 2) {
                Text(AppVariant.current.displayName)
                    .font(.system(size: 14, weight: .semibold))
                HStack(spacing: 5) {
                    Circle()
                        .fill(model.status.running ? Color.green : Color.secondary.opacity(0.5))
                        .frame(width: 7, height: 7)
                    Text(statusLine)
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            Spacer(minLength: 0)
        }
        .help(model.status.home)
    }

    private var statusLine: String {
        if let busy = model.busy {
            return busy
        }
        let status = model.status
        guard status.running else {
            return "Not running"
        }
        var parts = ["Running"]
        if let version = status.version {
            parts.append(version.first?.isNumber == true ? "v" + version : version)
        }
        if let uptime = status.uptimeSeconds {
            parts.append("up " + formatUptime(uptime))
        }
        if !status.webEnabled {
            parts.append("web off")
        }
        return parts.joined(separator: " · ")
    }
}

/// UsageSection shows the CPU and RAM of the daemon and its child processes.
struct UsageSection: View {
    let usage: Usage
    let cpuHistory: [Double]
    let ramHistory: [Double]

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Usage on this Mac")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(.secondary)
            metric(
                label: "CPU",
                value: usage.cpuPercent.map(formatPercent) ?? "…",
                detail: nil,
                history: cpuHistory,
                floor: 5
            )
            .help("CPU time of lana and its child processes over the last 2 seconds. 100% is one full core.")
            metric(
                label: "RAM",
                value: formatBytes(usage.residentBytes),
                detail: formatPercent(usage.memoryPercent) + " of " + formatBytes(usage.physicalMemory),
                history: ramHistory,
                floor: nil
            )
            .help("Resident memory of lana and its child processes (the RSS that ps and Activity Monitor show).")
            Text(describeProcesses(usage))
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
        }
    }

    private func metric(label: String, value: String, detail: String?, history: [Double], floor: Double?) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text(label)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .frame(width: 30, alignment: .leading)
            Text(value)
                .font(.system(size: 13, weight: .medium).monospacedDigit())
            if let detail {
                Text(detail)
                    .font(.system(size: 11).monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 4)
            Sparkline(values: history, capacity: AppModel.historyLength, floor: floor)
                .frame(width: 70, height: 16)
                .alignmentGuide(.firstTextBaseline) { $0[.bottom] - 3 }
        }
    }
}

/// Sparkline draws the last minute of samples.
struct Sparkline: View {
    let values: [Double]
    let capacity: Int
    /// floor is the smallest top of the scale (CPU uses 5 %), or nil to scale
    /// between the minimum and maximum (RAM).
    let floor: Double?

    var body: some View {
        GeometryReader { geo in
            if values.count >= 2 {
                let top = floor.map { max(values.max() ?? 0, $0) } ?? (values.max() ?? 0)
                let bottom = floor == nil ? (values.min() ?? 0) : 0
                let span = max(top - bottom, floor == nil ? max(top * 0.05, 1) : 0.0001)
                let step = geo.size.width / CGFloat(max(capacity - 1, 1))
                let offset = geo.size.width - step * CGFloat(values.count - 1)
                let flat = floor == nil && top - bottom < span * 0.5
                let points = values.enumerated().map { index, value in
                    CGPoint(
                        x: offset + step * CGFloat(index),
                        // A flat RAM line sits in the middle, not on the floor.
                        y: flat ? geo.size.height / 2 : geo.size.height - 1 - CGFloat((value - bottom) / span) * (geo.size.height - 2)
                    )
                }
                Path { path in
                    path.addLines(points)
                }
                .stroke(Color.accentColor, style: StrokeStyle(lineWidth: 1.25, lineCap: .round, lineJoin: .round))
                Path { path in
                    path.addLines(points)
                    path.addLine(to: CGPoint(x: points.last!.x, y: geo.size.height))
                    path.addLine(to: CGPoint(x: points.first!.x, y: geo.size.height))
                    path.closeSubpath()
                }
                .fill(Color.accentColor.opacity(0.12))
            }
        }
        .accessibilityHidden(true)
    }
}

/// MenuRow is a menu-like row: icon, title, and a hover highlight in the
/// accent colour, as in a native menu.
struct MenuRow: View {
    let title: String
    let symbol: String
    var enabled = true
    var shortcut: String?
    var help: String?
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                Image(systemName: symbol)
                    .font(.system(size: 13))
                    .frame(width: 18)
                Text(title)
                    .font(.system(size: 13))
                Spacer(minLength: 8)
                if let shortcut {
                    Text(shortcut)
                        .font(.system(size: 12))
                        .opacity(0.6)
                }
            }
            .foregroundStyle(highlighted ? Color.white : (enabled ? Color.primary : Color.secondary.opacity(0.6)))
            .padding(.horizontal, 9)
            .frame(height: 24)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(
                RoundedRectangle(cornerRadius: 5, style: .continuous)
                    .fill(highlighted ? Color.accentColor : Color.clear)
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .onHover { hovering = $0 }
        .help(help ?? "")
    }

    private var highlighted: Bool {
        hovering && enabled
    }
}

struct MessageBanner: View {
    let text: String
    let dismiss: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 6) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.orange)
                .font(.system(size: 11))
            Text(text)
                .font(.system(size: 11))
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
            Button(action: dismiss) {
                Image(systemName: "xmark").font(.system(size: 9, weight: .bold))
            }
            .buttonStyle(.plain)
            .foregroundStyle(.secondary)
        }
        .padding(8)
        .background(RoundedRectangle(cornerRadius: 6).fill(Color.orange.opacity(0.12)))
    }
}

/// WindowObserver reports when the menu window becomes visible or hidden, so
/// sampling runs only while the menu is open.
struct WindowObserver: NSViewRepresentable {
    let onChange: (Bool) -> Void

    func makeNSView(context: Context) -> ObserverView {
        let view = ObserverView()
        view.onChange = onChange
        return view
    }

    func updateNSView(_ nsView: ObserverView, context: Context) {
        nsView.onChange = onChange
    }

    final class ObserverView: NSView {
        var onChange: ((Bool) -> Void)?
        private var tokens: [NSObjectProtocol] = []

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            tokens.forEach { NotificationCenter.default.removeObserver($0) }
            tokens = []
            guard let window else {
                return
            }
            let center = NotificationCenter.default
            tokens.append(center.addObserver(forName: NSWindow.didChangeOcclusionStateNotification, object: window, queue: .main) { [weak self, weak window] _ in
                guard let window else {
                    return
                }
                self?.onChange?(window.occlusionState.contains(.visible) && window.isVisible)
            })
            tokens.append(center.addObserver(forName: NSWindow.willCloseNotification, object: window, queue: .main) { [weak self] _ in
                self?.onChange?(false)
            })
        }

        deinit {
            tokens.forEach { NotificationCenter.default.removeObserver($0) }
        }
    }
}
