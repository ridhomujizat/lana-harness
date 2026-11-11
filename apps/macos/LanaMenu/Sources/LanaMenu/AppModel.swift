import AppKit
import Foundation
import LanaMenuCore
import ServiceManagement

/// DashboardPage is one dashboard page the menu opens.
enum DashboardPage: String, CaseIterable {
    case chat, workflows, calendar, dashboard, router

    var hash: String {
        switch self {
        case .chat: return "#/chats"
        case .workflows: return "#/workflows"
        case .calendar: return "#/calendar"
        case .dashboard: return "#/home"
        case .router: return "#/router"
        }
    }

    var title: String {
        switch self {
        case .chat: return "Chat"
        case .workflows: return "Workflows"
        case .calendar: return "Calendar"
        case .dashboard: return "Open Dashboard"
        case .router: return "Router"
        }
    }

    var symbol: String {
        switch self {
        case .chat: return "bubble.left.and.bubble.right"
        case .workflows: return "point.3.connected.trianglepath.dotted"
        case .calendar: return "calendar"
        case .dashboard: return "macwindow"
        case .router: return "arrow.triangle.branch"
        }
    }
}

/// AppSettings are the app's own settings in UserDefaults
/// (dev.lana.menubar). Empty values mean "use the default".
struct AppSettings {
    static let homeKey = "lanaHome"
    static let binaryKey = "lanaBinary"
    static let openInWindowKey = "openPagesInWindow"
    static let windowAtLaunchKey = "openWindowAtLaunch"
    private let defaults = UserDefaults.standard

    /// openInWindow sends the menu pages and notification clicks to the lana
    /// window (spec #52) instead of the browser. On by default.
    var openInWindow: Bool {
        get { defaults.object(forKey: Self.openInWindowKey) as? Bool ?? true }
        nonmutating set { defaults.set(newValue, forKey: Self.openInWindowKey) }
    }

    /// windowAtLaunch opens the lana window when the owner starts Lana.app
    /// (not when it starts at login). On by default.
    var windowAtLaunch: Bool {
        get { defaults.object(forKey: Self.windowAtLaunchKey) as? Bool ?? true }
        nonmutating set { defaults.set(newValue, forKey: Self.windowAtLaunchKey) }
    }

    var homeOverride: String? {
        get { defaults.string(forKey: Self.homeKey) }
        nonmutating set { defaults.set(newValue?.isEmpty == true ? nil : newValue, forKey: Self.homeKey) }
    }

    var binaryOverride: String? {
        get { defaults.string(forKey: Self.binaryKey) }
        nonmutating set { defaults.set(newValue?.isEmpty == true ? nil : newValue, forKey: Self.binaryKey) }
    }

    /// home is the setting, then LANA_HOME from the launch environment, then ~/.lana.
    var home: LanaHome {
        LanaHome.resolve(override: homeOverride, environment: ProcessInfo.processInfo.environment, userHome: NSHomeDirectory())
    }

    /// defaultHome is the home without the setting, shown as the placeholder.
    var defaultHome: String {
        LanaHome.resolve(override: nil, environment: ProcessInfo.processInfo.environment, userHome: NSHomeDirectory()).path
    }
}

/// AppModel holds what the menu shows. It samples only while the menu is open.
@MainActor
final class AppModel: ObservableObject {
    static let refreshInterval: TimeInterval = 2
    /// historyLength keeps one minute of samples for the sparklines.
    static let historyLength = 30

    @Published private(set) var status: DaemonStatus
    @Published private(set) var usage: Usage?
    @Published private(set) var cpuHistory: [Double] = []
    @Published private(set) var ramHistory: [Double] = []
    @Published private(set) var binary = BinaryResolution(path: nil, source: "looking…")
    @Published private(set) var busy: String?
    @Published var message: String?
    @Published private(set) var optionDown = false
    @Published private(set) var launchAtLogin = false

    let settings = AppSettings()
    /// notifier posts the macOS notifications of spec #42.
    let notifier = Notifier(home: { AppSettings().home })
    private let reader = LibprocReader()
    private let meter = UsageMeter()
    private let versions = VersionCache()
    private var versionPending: Set<String> = []
    private var knownVersions: [String: String] = [:]
    private var shell: LoginShellEnvironment?
    private var timer: Timer?
    private var trackedPID: Int32?
    private var flagsMonitor: Any?
    private(set) var isOpen = false

    init() {
        status = readStatus(home: AppSettings().home)
        probeShell()
        refreshLaunchAtLogin()
        // Ask for notification permission and subscribe once the app runs.
        DispatchQueue.main.async { [notifier] in
            notifier.start()
        }
        let window = WebWindowController.shared
        window.home = { AppSettings().home }
        window.startDaemon = { [weak self] in
            self?.refresh()
            if self?.status.running == false {
                self?.start()
            }
        }
        window.openSettings = { [weak self] in
            guard let self else {
                return
            }
            SettingsWindow.show(model: self)
        }
    }

    // MARK: Open and close

    func menuOpened() {
        guard !isOpen else {
            return
        }
        isOpen = true
        refresh()
        refreshLaunchAtLogin()
        let timer = Timer(timeInterval: Self.refreshInterval, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refresh() }
        }
        RunLoop.main.add(timer, forMode: .common)
        self.timer = timer
        optionDown = NSEvent.modifierFlags.contains(.option)
        flagsMonitor = NSEvent.addLocalMonitorForEvents(matching: .flagsChanged) { [weak self] event in
            self?.optionDown = event.modifierFlags.contains(.option)
            return event
        }
    }

    func menuClosed() {
        guard isOpen else {
            return
        }
        isOpen = false
        timer?.invalidate()
        timer = nil
        if let monitor = flagsMonitor {
            NSEvent.removeMonitor(monitor)
            flagsMonitor = nil
        }
        optionDown = false
        // The next open starts a fresh CPU baseline; the sparkline keeps its points.
        meter.reset()
    }

    // MARK: Refresh

    /// refresh re-reads config.toml, daemon.lock, and daemon.secret, and
    /// samples the daemon's process tree.
    func refresh() {
        var next = readStatus(home: settings.home, reader: reader)
        if next.running, let exe = next.executable {
            next.version = knownVersions[exe]
            if next.version == nil {
                loadVersion(of: exe)
            }
        }
        status = next
        guard next.running, let pid = next.pid else {
            usage = nil
            trackedPID = nil
            cpuHistory = []
            ramHistory = []
            meter.reset()
            if busy == "Stopping lana…" {
                busy = nil
            }
            return
        }
        if busy == "Starting lana…" {
            busy = nil
        }
        if trackedPID != pid {
            trackedPID = pid
            meter.reset()
            cpuHistory = []
            ramHistory = []
        }
        let sample = meter.update(samples: collectTree(root: pid, reader: reader), nowNanos: monotonicNanos())
        usage = sample
        if let cpu = sample.cpuPercent {
            cpuHistory = Array((cpuHistory + [cpu]).suffix(Self.historyLength))
        }
        ramHistory = Array((ramHistory + [sample.residentMB]).suffix(Self.historyLength))
    }

    private func loadVersion(of exe: String) {
        guard !versionPending.contains(exe) else {
            return
        }
        versionPending.insert(exe)
        let versions = self.versions
        DispatchQueue.global(qos: .userInitiated).async {
            let version = versions.version(of: exe)
            DispatchQueue.main.async {
                self.versionPending.remove(exe)
                if let version {
                    self.knownVersions[exe] = version
                    if self.status.executable == exe {
                        self.status.version = version
                    }
                }
            }
        }
    }

    private func probeShell() {
        DispatchQueue.global(qos: .utility).async {
            let env = probeLoginShell()
            DispatchQueue.main.async {
                self.shell = env
                self.resolveBinaryNow()
            }
        }
    }

    func resolveBinaryNow() {
        binary = resolveBinary(setting: settings.binaryOverride, loginShellBinary: shell?.lanaBinary, userHome: NSHomeDirectory(), bundled: bundledBinary(bundleURL: Bundle.main.bundleURL))
    }

    /// settingsChanged applies a new home or binary path.
    func settingsChanged() {
        resolveBinaryNow()
        trackedPID = nil
        refresh()
        notifier.restart()
    }

    // MARK: Actions

    func open(_ page: DashboardPage) {
        if optionDown && status.running {
            signIn(next: page.hash)
            return
        }
        if settings.openInWindow {
            WebWindowController.shared.show(hash: page.hash)
            return
        }
        guard let url = status.dashboardURL(hash: page.hash) else {
            return
        }
        // The browser comes to the front, which dismisses the menu window.
        NSWorkspace.shared.open(url)
    }

    /// openWindow shows the lana window with the page it showed last (spec #52).
    func openWindow() {
        WebWindowController.shared.show()
    }

    /// signIn asks the daemon for a one-time sign-in link and opens it. next
    /// is the page to open after the sign-in.
    func signIn(next: String? = nil) {
        let home = settings.home
        Task {
            do {
                let url = try await requestSignInLink(home: home, next: next)
                NSWorkspace.shared.open(url)
            } catch {
                message = error.localizedDescription
            }
        }
    }

    func start() {
        message = nil
        guard let path = binary.path else {
            message = "lana binary not found. Set its path in Settings."
            return
        }
        do {
            let home = settings.home
            _ = try startDaemon(binary: path, home: home, path: shell?.path, baseEnvironment: ProcessInfo.processInfo.environment)
            busy = "Starting lana…"
            // Stop showing "Starting" after a while even if the daemon failed;
            // its log says why.
            DispatchQueue.main.asyncAfter(deadline: .now() + 20) { [weak self] in
                guard let self, self.busy == "Starting lana…" else {
                    return
                }
                self.busy = nil
                self.refresh()
                if !self.status.running {
                    self.message = "lana did not start. See \(home.daemonLogURL.path)."
                }
            }
        } catch {
            message = error.localizedDescription
        }
    }

    func stop() {
        message = nil
        guard status.running, let pid = status.pid else {
            return
        }
        let alert = NSAlert()
        alert.messageText = "Stop lana?"
        alert.informativeText = "The daemon (pid \(pid)) finishes its running turns and stops. Telegram, the dashboard, workflows, and the calendar stay off until you start lana again."
        alert.alertStyle = .warning
        alert.addButton(withTitle: "Stop lana")
        alert.addButton(withTitle: "Cancel")
        NSApp.activate(ignoringOtherApps: true)
        guard alert.runModal() == .alertFirstButtonReturn else {
            return
        }
        // Read the status again: the daemon may have restarted while the alert was open.
        let current = readStatus(home: settings.home, reader: reader)
        guard current.running, current.pid == pid else {
            refresh()
            return
        }
        do {
            try stopDaemon(pid: pid)
            busy = "Stopping lana…"
        } catch {
            message = error.localizedDescription
        }
    }

    func revealLog() {
        let log = settings.home.daemonLogURL
        if FileManager.default.fileExists(atPath: log.path) {
            NSWorkspace.shared.activateFileViewerSelecting([log])
        } else {
            NSWorkspace.shared.open(URL(fileURLWithPath: settings.home.path))
        }
    }

    // MARK: Launch at login

    var canLaunchAtLogin: Bool {
        Bundle.main.bundleURL.pathExtension == "app"
    }

    func refreshLaunchAtLogin() {
        launchAtLogin = SMAppService.mainApp.status == .enabled
    }

    func setLaunchAtLogin(_ on: Bool) {
        do {
            if on {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
        } catch {
            message = "Launch at login: \(error.localizedDescription)"
        }
        refreshLaunchAtLogin()
    }
}
