import AppKit
import LanaMenuCore
import WebKit

/// WebWindowController shows the dashboard in a native window inside Lana.app
/// (spec #52), so lana opens as its own app with its own Dock icon instead of
/// a browser tab. The window is a WKWebView with the app's own cookie jar; it
/// signs in with a one-time app link from the daemon (API-DSK-001), so no
/// password is typed and none is changed.
@MainActor
final class WebWindowController: NSObject {
    static let shared = WebWindowController()

    /// defaultHash is the page a first open shows.
    static let defaultHash = "#/chats"
    private static let lastHashKey = "windowLastHash"
    private static let zoomKey = "windowZoom"

    /// home is the LANA_HOME the window follows.
    var home: () -> LanaHome = { AppSettings().home }
    /// startDaemon runs Start lana from the "not running" page.
    var startDaemon: (() -> Void)?
    /// openSettings shows the app settings (⌘,).
    var openSettings: (() -> Void)?
    /// onPageLoad reports each finished dashboard load (for --window-snapshot).
    var onPageLoad: ((URL?) -> Void)?

    private(set) var window: NSWindow?
    private(set) var webView: WKWebView?
    private var popups: [ObjectIdentifier: NSWindow] = [:]
    private var observations: [NSKeyValueObservation] = []
    private var downloads: [ObjectIdentifier: URL] = [:]
    /// signedInAddr is the daemon address whose app link the window loaded.
    private var signedInAddr: String?
    private var waitTimer: Timer?
    private var showingPlaceholder = false

    private lazy var configuration: WKWebViewConfiguration = {
        let config = WKWebViewConfiguration()
        // The persistent store of this app (~/Library/WebKit/<bundle id>): the
        // dashboard's local storage survives restarts. The session cookie has
        // no expiry, so it lives while the app runs; an app link renews it.
        config.websiteDataStore = .default()
        config.applicationNameForUserAgent = "Lana/" + (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "dev")
        config.userContentController.add(ScriptMessageProxy(target: self), name: "lana")
        // The title-bar-less window (spec #52): mark the page and let it move
        // the window from its drag regions.
        config.userContentController.add(ScriptMessageProxy(target: self, shell: true), name: "lanaShell")
        config.userContentController.addUserScript(WKUserScript(
            source: shellBootstrapScript(variant: AppVariant.current, lightsWidth: lightsWidth),
            injectionTime: .atDocumentStart,
            forMainFrameOnly: true
        ))
        return config
    }()

    /// lightsWidth is the inset the page leaves for the traffic lights,
    /// measured from the real buttons when the window is made.
    private(set) var lightsWidth = ShellLayout.lightsWidth(zoomMaxX: 74)
    /// lastMouseDown is the newest primary mouse-down in a lana window, which
    /// a "drag" message from the page turns into a window drag.
    private var lastMouseDown: NSEvent?
    private var mouseMonitor: Any?
    /// lightObservers watch the traffic light buttons: AppKit puts them back
    /// at its own place on several layouts (first display, key changes,
    /// appearance changes), and this moves them back to the top row.
    private var lightObservers: [NSObjectProtocol] = []

    var isVisible: Bool {
        window?.isVisible == true
    }

    // MARK: Show and load

    /// show brings the window to the front and opens hash, or keeps the page
    /// it shows when hash is nil.
    func show(hash: String? = nil) {
        let window = ensureWindow()
        becomeRegularApp()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        load(hash: hash)
    }

    /// load opens hash on the running daemon. On the dashboard already, it
    /// changes the hash in place, so the page keeps its state; otherwise it
    /// loads a fresh app link. force always goes through an app link (⌘R), which
    /// also signs in again after a logout or a daemon reset.
    func load(hash: String?, force: Bool = false) {
        guard let webView else {
            return
        }
        let status = readStatus(home: home())
        guard status.running else {
            showPlaceholder(
                title: "lana is not running",
                detail: "Start the daemon here or from the lana menu bar item. This window opens the dashboard when it is up.",
                canStart: startDaemon != nil
            )
            waitForDaemon(hash: hash)
            return
        }
        guard status.webEnabled else {
            showPlaceholder(title: "The dashboard is off", detail: "Set [web] enabled = true in config.toml and restart lana.", canStart: false)
            return
        }
        stopWaiting()
        if !force, !showingPlaceholder, signedInAddr == status.addr, let current = webView.url,
           classifyNavigation(current, dashboardAddr: status.addr) == .inApp
        {
            if let hash {
                webView.evaluateJavaScript("window.location.hash = \(Self.jsString(hash))", completionHandler: nil)
            }
            return
        }
        let target = hash ?? currentHash() ?? UserDefaults.standard.string(forKey: Self.lastHashKey) ?? Self.defaultHash
        let homeNow = home()
        Task {
            var url: URL
            do {
                url = try await requestAppLink(home: homeNow, next: target)
            } catch {
                // A daemon older than spec #52 has no app link: the dashboard
                // shows its password sign-in.
                NSLog("lana: app link: %@", error.localizedDescription)
                guard let plain = status.dashboardURL(hash: target) else {
                    return
                }
                url = plain
            }
            self.signedInAddr = status.addr
            self.showingPlaceholder = false
            webView.load(URLRequest(url: url))
        }
    }

    /// reload signs in again and reloads the current page (⌘R).
    @objc func reloadPage(_ sender: Any?) {
        load(hash: currentHash(), force: true)
    }

    private func currentHash() -> String? {
        guard !showingPlaceholder, let url = webView?.url else {
            return nil
        }
        return dashboardHashOf(url)
    }

    private func showPlaceholder(title: String, detail: String, canStart: Bool) {
        showingPlaceholder = true
        signedInAddr = nil
        webView?.loadHTMLString(placeholderHTML(title: title, detail: detail, canStart: canStart), baseURL: nil)
    }

    /// waitForDaemon polls the daemon files every 2 s while the window shows
    /// the "not running" page, and loads the dashboard once lana is up.
    private func waitForDaemon(hash: String?) {
        guard waitTimer == nil else {
            return
        }
        let timer = Timer(timeInterval: 2, repeats: true) { [weak self] _ in
            Task { @MainActor in
                guard let self else {
                    return
                }
                guard self.isVisible else {
                    self.stopWaiting()
                    return
                }
                let status = readStatus(home: self.home())
                if status.running, status.localControl {
                    self.stopWaiting()
                    self.load(hash: hash)
                }
            }
        }
        RunLoop.main.add(timer, forMode: .common)
        waitTimer = timer
    }

    private func stopWaiting() {
        waitTimer?.invalidate()
        waitTimer = nil
    }

    // MARK: Window

    private func ensureWindow() -> NSWindow {
        if let window {
            return window
        }
        // The window first: the page's inset comes from the real lights.
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1200, height: 800),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        // No title bar: the dashboard draws the whole window and the traffic
        // lights sit in its top row. The title stays for the Window menu and
        // Mission Control.
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .hidden
        window.titlebarSeparatorStyle = .none
        window.backgroundColor = Self.shellBackground
        layoutTrafficLights(in: window)
        if let zoomButton = window.standardWindowButton(.zoomButton) {
            lightsWidth = ShellLayout.lightsWidth(zoomMaxX: Double(zoomButton.frame.maxX))
        }
        let webView = WKWebView(frame: NSRect(x: 0, y: 0, width: 1200, height: 800), configuration: configuration)
        // The window background shows until the page paints, so no white
        // flash and no seam.
        webView.setValue(false, forKey: "drawsBackground")
        webView.navigationDelegate = self
        webView.uiDelegate = self
        webView.allowsBackForwardNavigationGestures = true
        webView.allowsMagnification = true
        webView.underPageBackgroundColor = Self.shellBackground
        if #available(macOS 13.3, *) {
            webView.isInspectable = true
        }
        let zoom = UserDefaults.standard.double(forKey: Self.zoomKey)
        if zoom > 0 {
            webView.pageZoom = zoom
        }
        window.title = AppVariant.current.displayName
        window.contentView = webView
        window.minSize = NSSize(width: 420, height: 400)
        window.isReleasedWhenClosed = false
        window.tabbingMode = .disallowed
        window.collectionBehavior.insert(.fullScreenPrimary)
        window.delegate = self
        window.center()
        window.setFrameAutosaveName("LanaDashboard")
        observations.append(webView.observe(\.title, options: [.new]) { [weak window] view, _ in
            Task { @MainActor in
                // The dev app says so in every window title (spec #52).
                let name = AppVariant.current.displayName
                let title = view.title ?? ""
                window?.title = title.isEmpty ? name : (AppVariant.current == .dev ? "\(title) · dev" : title)
            }
        })
        observations.append(webView.observe(\.url, options: [.new]) { view, _ in
            Task { @MainActor in
                if let url = view.url, let hash = dashboardHashOf(url) {
                    UserDefaults.standard.set(hash, forKey: Self.lastHashKey)
                }
            }
        })
        self.window = window
        self.webView = webView
        startMouseMonitor()
        watchTrafficLights(in: window)
        layoutTrafficLights(in: window)
        MainMenu.install(controller: self)
        return window
    }

    // MARK: Title-bar-less window (spec #52)

    /// shellBackground is the sidebar colour of the dashboard theme
    /// (--bg-side), so the window shows no seam while the page loads.
    static let shellBackground = NSColor(name: "LanaShellBackground") { appearance in
        appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            ? NSColor(srgbRed: 0x15 / 255, green: 0x15 / 255, blue: 0x14 / 255, alpha: 1)
            : NSColor(srgbRed: 0xF4 / 255, green: 0xF3 / 255, blue: 0xF0 / 255, alpha: 1)
    }

    /// layoutTrafficLights centres the close, minimize, and zoom buttons in a
    /// 52 pt top row, 20 pt from the left, as Electron's trafficLightPosition
    /// does: the title bar container grows to the row and each button moves.
    /// AppKit lays them out again on resize and full screen changes, so the
    /// window delegate calls this again.
    func layoutTrafficLights(in window: NSWindow) {
        guard !window.styleMask.contains(.fullScreen),
              let close = window.standardWindowButton(.closeButton),
              let mini = window.standardWindowButton(.miniaturizeButton),
              let zoom = window.standardWindowButton(.zoomButton),
              let container = close.superview?.superview
        else {
            return
        }
        let row = CGFloat(ShellLayout.rowHeight)
        var frame = container.frame
        frame.size.height = row
        frame.origin.y = window.frame.height - row
        if container.frame != frame {
            container.frame = frame
        }
        let spacing = lightSpacing ?? max(mini.frame.minX - close.frame.minX, 20)
        lightSpacing = spacing
        for (index, button) in [close, mini, zoom].enumerated() {
            let origin = NSPoint(
                x: CGFloat(ShellLayout.leftInset) + CGFloat(index) * spacing,
                y: CGFloat(ShellLayout.buttonY(height: Double(button.frame.height)))
            )
            // Moving only when needed keeps the frame observer from looping.
            if button.frame.origin != origin {
                button.setFrameOrigin(origin)
            }
        }
    }

    /// lightSpacing is the system distance between two traffic lights,
    /// read once before the first move.
    private var lightSpacing: CGFloat?

    private func watchTrafficLights(in window: NSWindow) {
        let buttons: [NSView] = [NSWindow.ButtonType.closeButton, .miniaturizeButton, .zoomButton]
            .compactMap { window.standardWindowButton($0) }
        let views = buttons + buttons.compactMap { $0.superview?.superview }
        for view in views {
            view.postsFrameChangedNotifications = true
            lightObservers.append(NotificationCenter.default.addObserver(
                forName: NSView.frameDidChangeNotification, object: view, queue: .main
            ) { [weak self, weak window] _ in
                MainActor.assumeIsolated {
                    guard let self, let window else {
                        return
                    }
                    self.layoutTrafficLights(in: window)
                }
            })
        }
    }

    /// trafficLightFrames reports the button frames in window coordinates
    /// (top-left origin), for --window-snapshot.
    func trafficLightFrames() -> [String: [Int]] {
        guard let window else {
            return [:]
        }
        var out: [String: [Int]] = [:]
        let names: [(String, NSWindow.ButtonType)] = [("close", .closeButton), ("minimize", .miniaturizeButton), ("zoom", .zoomButton)]
        for (name, type) in names {
            guard let button = window.standardWindowButton(type), let superview = button.superview else {
                continue
            }
            let rect = superview.convert(button.frame, to: nil)
            let top = window.frame.height - rect.maxY
            out[name] = [Int(rect.minX.rounded()), Int(top.rounded()), Int(rect.width.rounded()), Int(rect.height.rounded())]
        }
        return out
    }

    private func startMouseMonitor() {
        guard mouseMonitor == nil else {
            return
        }
        mouseMonitor = NSEvent.addLocalMonitorForEvents(matching: .leftMouseDown) { [weak self] event in
            self?.lastMouseDown = event
            return event
        }
    }

    fileprivate func handleShellMessage(_ body: Any) {
        guard let message = ShellMessage.parse(body) else {
            return
        }
        let target = lastMouseDown?.window ?? NSApp.keyWindow ?? window
        switch message {
        case .drag:
            guard let event = lastMouseDown, let window = event.window, window === target else {
                return
            }
            // The page posts during the mouse-down; performDrag follows the
            // mouse until the button goes up.
            window.performDrag(with: event)
        case .doubleClick:
            guard let window = target else {
                return
            }
            switch DoubleClickAction.from(setting: UserDefaults.standard.string(forKey: "AppleActionOnDoubleClick")) {
            case .zoom: window.performZoom(nil)
            case .minimize: window.performMiniaturize(nil)
            case .none: break
            }
        case let .theme(dark):
            // The lights and the loading background follow the dashboard
            // theme, which may differ from the system one.
            window?.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        }
    }

    private func syncFullscreenAttribute() {
        guard let window, let webView else {
            return
        }
        webView.evaluateJavaScript(fullscreenScript(window.styleMask.contains(.fullScreen)), completionHandler: nil)
    }

    /// becomeRegularApp gives the app a Dock icon and a menu bar while a
    /// window is open; the menu bar item stays either way.
    private func becomeRegularApp() {
        if NSApp.activationPolicy() != .regular {
            NSApp.setActivationPolicy(.regular)
        }
        MainMenu.install(controller: self)
    }

    /// becomeAccessoryIfIdle drops the Dock icon when no lana window is open.
    fileprivate func becomeAccessoryIfIdle(closing: NSWindow) {
        let others = ([window] + popups.values.map { $0 }).compactMap { $0 }.filter { $0 !== closing && $0.isVisible }
        if others.isEmpty {
            NSApp.setActivationPolicy(.accessory)
        }
    }

    // MARK: Menu actions

    @objc func openPage(_ sender: NSMenuItem) {
        guard let hash = sender.representedObject as? String else {
            return
        }
        show(hash: hash)
    }

    @objc func zoomIn(_ sender: Any?) {
        setZoom((webView?.pageZoom ?? 1) * 1.1)
    }

    @objc func zoomOut(_ sender: Any?) {
        setZoom((webView?.pageZoom ?? 1) / 1.1)
    }

    @objc func actualSize(_ sender: Any?) {
        setZoom(1)
    }

    private func setZoom(_ value: CGFloat) {
        let zoom = min(3, max(0.5, value))
        webView?.pageZoom = zoom
        UserDefaults.standard.set(Double(zoom), forKey: Self.zoomKey)
    }

    @objc func goBack(_ sender: Any?) {
        webView?.goBack()
    }

    @objc func goForward(_ sender: Any?) {
        webView?.goForward()
    }

    @objc func openInBrowser(_ sender: Any?) {
        let status = readStatus(home: home())
        if let url = status.dashboardURL(hash: currentHash() ?? Self.defaultHash) {
            NSWorkspace.shared.open(url)
        }
    }

    @objc func showSettings(_ sender: Any?) {
        openSettings?()
    }

    // MARK: Helpers

    static func jsString(_ value: String) -> String {
        let data = (try? JSONSerialization.data(withJSONObject: [value])) ?? Data("[\"\"]".utf8)
        let array = String(decoding: data, as: UTF8.self)
        return String(array.dropFirst().dropLast())
    }

    private var dashboardAddr: String {
        readStatus(home: home()).addr
    }

    private func uniqueDownloadURL(_ suggested: String) -> URL {
        let folder = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first
            ?? URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("Downloads")
        let name = (suggested as NSString).lastPathComponent.isEmpty ? "download" : (suggested as NSString).lastPathComponent
        let base = (name as NSString).deletingPathExtension
        let ext = (name as NSString).pathExtension
        var candidate = folder.appendingPathComponent(name)
        var n = 2
        while FileManager.default.fileExists(atPath: candidate.path) {
            let numbered = ext.isEmpty ? "\(base) \(n)" : "\(base) \(n).\(ext)"
            candidate = folder.appendingPathComponent(numbered)
            n += 1
        }
        return candidate
    }

    fileprivate func handleScriptMessage(_ body: Any) {
        // Only the "not running" page has the button; the dashboard never
        // posts to this handler.
        guard showingPlaceholder, body as? String == "start" else {
            return
        }
        startDaemon?()
    }
}

// MARK: - NSWindowDelegate

extension WebWindowController: NSWindowDelegate {
    func windowDidResize(_ notification: Notification) {
        if let resized = notification.object as? NSWindow, resized === window {
            layoutTrafficLights(in: resized)
        }
    }

    /// windowDidUpdate runs once per event loop pass of the window: the last
    /// net for a layout that moved the lights without a frame notification
    /// (AppKit sometimes swaps the zoom button). It only compares frames when
    /// nothing moved.
    func windowDidUpdate(_ notification: Notification) {
        if let updated = notification.object as? NSWindow, updated === window {
            layoutTrafficLights(in: updated)
        }
    }

    func windowDidBecomeKey(_ notification: Notification) {
        if let key = notification.object as? NSWindow, key === window {
            layoutTrafficLights(in: key)
        }
    }

    func windowDidResignKey(_ notification: Notification) {
        if let other = notification.object as? NSWindow, other === window {
            layoutTrafficLights(in: other)
        }
    }

    func windowWillEnterFullScreen(_ notification: Notification) {
        webView?.evaluateJavaScript(fullscreenScript(true), completionHandler: nil)
    }

    func windowDidExitFullScreen(_ notification: Notification) {
        if let window {
            layoutTrafficLights(in: window)
        }
        webView?.evaluateJavaScript(fullscreenScript(false), completionHandler: nil)
    }

    func windowWillClose(_ notification: Notification) {
        guard let closing = notification.object as? NSWindow else {
            return
        }
        if closing === window {
            stopWaiting()
        } else {
            popups.removeValue(forKey: ObjectIdentifier(closing))
        }
        becomeAccessoryIfIdle(closing: closing)
    }
}

// MARK: - WKNavigationDelegate

extension WebWindowController: WKNavigationDelegate {
    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction, decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
        if navigationAction.shouldPerformDownload {
            decisionHandler(.download)
            return
        }
        guard let url = navigationAction.request.url else {
            decisionHandler(.allow)
            return
        }
        switch classifyNavigation(url, dashboardAddr: dashboardAddr) {
        case .inApp, .allow:
            decisionHandler(.allow)
        case .external:
            // A frame inside the page (an embedded preview) may load what it
            // wants; a link the owner follows leaves for the browser.
            if navigationAction.targetFrame?.isMainFrame == false {
                decisionHandler(.allow)
                return
            }
            NSWorkspace.shared.open(url)
            decisionHandler(.cancel)
        }
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationResponse: WKNavigationResponse, decisionHandler: @escaping @MainActor (WKNavigationResponsePolicy) -> Void) {
        decisionHandler(navigationResponse.canShowMIMEType ? .allow : .download)
    }

    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        if webView === self.webView {
            syncFullscreenAttribute()
        }
        if webView === self.webView, !showingPlaceholder {
            onPageLoad?(webView.url)
        }
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        guard webView === self.webView else {
            return
        }
        let code = (error as NSError).code
        // A cancelled load (a new load replaced it) is not a failure.
        if code == NSURLErrorCancelled || code == 102 {
            return
        }
        // The daemon went away between the status read and the load.
        load(hash: currentHash())
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        webView.reload()
    }
}

// MARK: - WKUIDelegate

extension WebWindowController: WKUIDelegate {
    /// target=_blank and window.open: the dashboard origin (a file preview)
    /// opens in a second window with the same cookie jar; anything else goes
    /// to the browser.
    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration, for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        guard let url = navigationAction.request.url else {
            return nil
        }
        guard classifyNavigation(url, dashboardAddr: dashboardAddr) == .inApp else {
            NSWorkspace.shared.open(url)
            return nil
        }
        let popup = WKWebView(frame: NSRect(x: 0, y: 0, width: 960, height: 720), configuration: configuration)
        popup.navigationDelegate = self
        popup.uiDelegate = self
        popup.allowsMagnification = true
        let window = NSWindow(
            contentRect: popup.frame,
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = url.lastPathComponent.isEmpty ? "lana" : url.lastPathComponent
        window.contentView = popup
        window.isReleasedWhenClosed = false
        window.delegate = self
        observations.append(popup.observe(\.title, options: [.new]) { [weak window] view, _ in
            Task { @MainActor in
                if let title = view.title, !title.isEmpty {
                    window?.title = title
                }
            }
        })
        if let parent = self.window {
            window.setFrameTopLeftPoint(NSPoint(x: parent.frame.minX + 40, y: parent.frame.maxY - 40))
        } else {
            window.center()
        }
        popups[ObjectIdentifier(window)] = window
        window.makeKeyAndOrderFront(nil)
        return popup
    }

    func webViewDidClose(_ webView: WKWebView) {
        webView.window?.close()
    }

    func webView(_ webView: WKWebView, runOpenPanelWith parameters: WKOpenPanelParameters, initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping @MainActor ([URL]?) -> Void) {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = parameters.allowsDirectories
        panel.allowsMultipleSelection = parameters.allowsMultipleSelection
        guard let window = webView.window else {
            completionHandler(panel.runModal() == .OK ? panel.urls : nil)
            return
        }
        panel.beginSheetModal(for: window) { response in
            completionHandler(response == .OK ? panel.urls : nil)
        }
    }

    func webView(_ webView: WKWebView, runJavaScriptAlertPanelWithMessage message: String, initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping @MainActor () -> Void) {
        let alert = NSAlert()
        alert.messageText = message
        alert.addButton(withTitle: "OK")
        runSheet(alert, in: webView) { _ in completionHandler() }
    }

    func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String, initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping @MainActor (Bool) -> Void) {
        let alert = NSAlert()
        alert.messageText = message
        alert.addButton(withTitle: "OK")
        alert.addButton(withTitle: "Cancel")
        runSheet(alert, in: webView) { completionHandler($0 == .alertFirstButtonReturn) }
    }

    func webView(_ webView: WKWebView, runJavaScriptTextInputPanelWithPrompt prompt: String, defaultText: String?, initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping @MainActor (String?) -> Void) {
        let alert = NSAlert()
        alert.messageText = prompt
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 260, height: 24))
        field.stringValue = defaultText ?? ""
        alert.accessoryView = field
        alert.addButton(withTitle: "OK")
        alert.addButton(withTitle: "Cancel")
        runSheet(alert, in: webView) { completionHandler($0 == .alertFirstButtonReturn ? field.stringValue : nil) }
    }

    private func runSheet(_ alert: NSAlert, in webView: WKWebView, completion: @escaping (NSApplication.ModalResponse) -> Void) {
        if let window = webView.window {
            alert.beginSheetModal(for: window, completionHandler: completion)
        } else {
            completion(alert.runModal())
        }
    }
}

// MARK: - WKDownloadDelegate

extension WebWindowController: WKDownloadDelegate {
    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse, suggestedFilename: String, completionHandler: @escaping @MainActor (URL?) -> Void) {
        let url = uniqueDownloadURL(suggestedFilename)
        downloads[ObjectIdentifier(download)] = url
        completionHandler(url)
    }

    func downloadDidFinish(_ download: WKDownload) {
        guard let url = downloads.removeValue(forKey: ObjectIdentifier(download)) else {
            return
        }
        // Bounce the Downloads stack in the Dock, as browsers do.
        DistributedNotificationCenter.default().post(name: .init("com.apple.DownloadFileFinished"), object: url.path)
    }

    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        downloads.removeValue(forKey: ObjectIdentifier(download))
        NSLog("lana: download failed: %@", error.localizedDescription)
    }
}

/// ScriptMessageProxy breaks the retain cycle between the user content
/// controller and the window controller.
private final class ScriptMessageProxy: NSObject, WKScriptMessageHandler {
    weak var target: WebWindowController?
    /// shell routes the message to the window chrome handler (lanaShell).
    let shell: Bool

    init(target: WebWindowController, shell: Bool = false) {
        self.target = target
        self.shell = shell
    }

    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        let body = message.body
        let shell = self.shell
        // Run the drag on the main thread right away: performDrag needs the
        // mouse button still down.
        MainActor.assumeIsolated {
            if shell {
                self.target?.handleShellMessage(body)
            } else {
                self.target?.handleScriptMessage(body)
            }
        }
    }
}

// MARK: - Main menu

/// MainMenu is the menu bar of the app while a lana window is open. A menu bar
/// app has none of its own, and without an Edit menu ⌘C and ⌘V do nothing in
/// the web view.
@MainActor
enum MainMenu {
    private static var installed: NSMenu?

    static func install(controller: WebWindowController) {
        if let installed, NSApp.mainMenu === installed {
            return
        }
        let menu = build(controller: controller)
        NSApp.mainMenu = menu
        installed = menu
    }

    /// titles lists the top-level menus, for --window-snapshot checks.
    static var titles: [String] {
        NSApp.mainMenu?.items.map { $0.submenu?.title ?? $0.title } ?? []
    }

    private static func build(controller: WebWindowController) -> NSMenu {
        let main = NSMenu()

        let app = submenu(main, "lana")
        app.addItem(withTitle: "About lana", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: "")
        app.addItem(.separator())
        item(app, "Settings…", #selector(WebWindowController.showSettings(_:)), ",", target: controller)
        app.addItem(.separator())
        app.addItem(withTitle: "Hide lana", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        let others = app.addItem(withTitle: "Hide Others", action: #selector(NSApplication.hideOtherApplications(_:)), keyEquivalent: "h")
        others.keyEquivalentModifierMask = [.command, .option]
        app.addItem(withTitle: "Show All", action: #selector(NSApplication.unhideAllApplications(_:)), keyEquivalent: "")
        app.addItem(.separator())
        app.addItem(withTitle: "Quit lana Menu", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")

        let file = submenu(main, "File")
        let browser = item(file, "Open in Browser", #selector(WebWindowController.openInBrowser(_:)), "o", target: controller)
        browser.keyEquivalentModifierMask = [.command, .shift]
        file.addItem(.separator())
        file.addItem(withTitle: "Close Window", action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w")

        let edit = submenu(main, "Edit")
        edit.addItem(withTitle: "Undo", action: Selector(("undo:")), keyEquivalent: "z")
        let redo = edit.addItem(withTitle: "Redo", action: Selector(("redo:")), keyEquivalent: "z")
        redo.keyEquivalentModifierMask = [.command, .shift]
        edit.addItem(.separator())
        edit.addItem(withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        edit.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        edit.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        let pastePlain = edit.addItem(withTitle: "Paste and Match Style", action: #selector(NSTextView.pasteAsPlainText(_:)), keyEquivalent: "v")
        pastePlain.keyEquivalentModifierMask = [.command, .option, .shift]
        edit.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")

        let view = submenu(main, "View")
        item(view, "Reload", #selector(WebWindowController.reloadPage(_:)), "r", target: controller)
        view.addItem(.separator())
        item(view, "Actual Size", #selector(WebWindowController.actualSize(_:)), "0", target: controller)
        item(view, "Zoom In", #selector(WebWindowController.zoomIn(_:)), "+", target: controller)
        item(view, "Zoom Out", #selector(WebWindowController.zoomOut(_:)), "-", target: controller)
        view.addItem(.separator())
        let full = view.addItem(withTitle: "Enter Full Screen", action: #selector(NSWindow.toggleFullScreen(_:)), keyEquivalent: "f")
        full.keyEquivalentModifierMask = [.command, .control]

        let go = submenu(main, "Go")
        item(go, "Back", #selector(WebWindowController.goBack(_:)), "[", target: controller)
        item(go, "Forward", #selector(WebWindowController.goForward(_:)), "]", target: controller)
        go.addItem(.separator())
        for (index, page) in [DashboardPage.chat, .workflows, .calendar, .dashboard, .router].enumerated() {
            let entry = item(go, page == .dashboard ? "Dashboard" : page.title, #selector(WebWindowController.openPage(_:)), String(index + 1), target: controller)
            entry.representedObject = page.hash
        }

        let window = submenu(main, "Window")
        window.addItem(withTitle: "Minimize", action: #selector(NSWindow.performMiniaturize(_:)), keyEquivalent: "m")
        window.addItem(withTitle: "Zoom", action: #selector(NSWindow.performZoom(_:)), keyEquivalent: "")
        window.addItem(.separator())
        window.addItem(withTitle: "Bring All to Front", action: #selector(NSApplication.arrangeInFront(_:)), keyEquivalent: "")
        NSApp.windowsMenu = window
        return main
    }

    private static func submenu(_ main: NSMenu, _ title: String) -> NSMenu {
        let menu = NSMenu(title: title)
        let holder = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        holder.submenu = menu
        main.addItem(holder)
        return menu
    }

    @discardableResult
    private static func item(_ menu: NSMenu, _ title: String, _ action: Selector, _ key: String, target: AnyObject) -> NSMenuItem {
        let entry = menu.addItem(withTitle: title, action: action, keyEquivalent: key)
        entry.target = target
        return entry
    }
}
