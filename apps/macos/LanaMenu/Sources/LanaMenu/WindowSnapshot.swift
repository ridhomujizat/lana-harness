import AppKit
import LanaMenuCore
import WebKit

/// WindowSnapshot opens the lana window against the running daemon, waits for
/// the dashboard, writes a PNG of the window content, and prints what it
/// loaded as JSON (`LanaMenu --window-snapshot out.png [--page HASH]
/// [--seconds N]`). It checks the app link sign-in of spec #52 end to end.
enum WindowSnapshot {
    struct Report: Encodable {
        var url: String?
        var title: String?
        var hash: String?
        var signedIn: Bool
        var text: String
        var menus: [String]
        var activationPolicy: String
        var png: String
        /// lights are the traffic light frames [x, y, w, h] from the window's top left.
        var lights: [String: [Int]]
        var lightsWidth: Int
        /// windowFrame is the window on screen [x, y, w, h], top-left origin
        /// (for `screencapture -R` while --hold keeps the window open).
        var windowFrame: [Int]
        /// shell is html[data-shell], data-shell-variant, and the page's inset.
        var shell: String
    }

    static func screenFrame(_ window: NSWindow?) -> [Int] {
        guard let window, let screen = window.screen ?? NSScreen.main else {
            return []
        }
        let f = window.frame
        let top = screen.frame.maxY - f.maxY
        return [Int(f.minX), Int(top), Int(f.width), Int(f.height)]
    }

    static func run(path: String, hash: String?, seconds: Double, hold: Double = 0, eval: String? = nil) -> Int32 {
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        var code: Int32 = 1
        DispatchQueue.main.async {
            MainActor.assumeIsolated {
                let controller = WebWindowController.shared
                var finished = false
                let finish = {
                    guard !finished else {
                        return
                    }
                    finished = true
                    if let eval {
                        controller.webView?.evaluateJavaScript(eval, completionHandler: nil)
                    }
                    DispatchQueue.main.asyncAfter(deadline: .now() + max(1, seconds)) {
                        MainActor.assumeIsolated {
                            capture(controller: controller, path: path) { result in
                                code = result
                                fflush(stdout)
                                if hold > 0 {
                                    Thread.sleep(forTimeInterval: hold)
                                }
                                app.stop(nil)
                                app.postEvent(NSEvent.otherEvent(with: .applicationDefined, location: .zero, modifierFlags: [], timestamp: 0, windowNumber: 0, context: nil, subtype: 0, data1: 0, data2: 0)!, atStart: false)
                            }
                        }
                    }
                }
                controller.onPageLoad = { _ in finish() }
                controller.show(hash: hash)
                // The "not running" page never reports a dashboard load.
                DispatchQueue.main.asyncAfter(deadline: .now() + 20) { finish() }
            }
        }
        app.run()
        return code
    }

    @MainActor
    private static func capture(controller: WebWindowController, path: String, done: @escaping (Int32) -> Void) {
        guard let webView = controller.webView else {
            done(1)
            return
        }
        let script = "JSON.stringify({text: (document.body ? document.body.innerText : '').replace(/\\s+/g, ' ').slice(0, 240), login: !!document.querySelector('input[type=password]'), shell: [document.documentElement.dataset.shell, document.documentElement.dataset.shellVariant || '', getComputedStyle(document.documentElement).getPropertyValue('--shell-lights-w'), document.querySelectorAll('[data-drag-region]').length + ' drag regions'].join(' ')})"
        webView.evaluateJavaScript(script) { value, _ in
            struct Page: Decodable { var text: String; var login: Bool; var shell: String }
            let page = (value as? String).flatMap { try? JSONDecoder().decode(Page.self, from: Data($0.utf8)) }
            webView.takeSnapshot(with: nil) { image, error in
                var wrote = false
                if let image, let tiff = image.tiffRepresentation, let rep = NSBitmapImageRep(data: tiff),
                   let png = rep.representation(using: .png, properties: [:])
                {
                    wrote = (try? png.write(to: URL(fileURLWithPath: path))) != nil
                } else if let error {
                    fputs("error: snapshot: \(error)\n", stderr)
                }
                let url = webView.url
                let report = Report(
                    url: url?.absoluteString,
                    title: webView.title,
                    hash: url.flatMap(dashboardHashOf),
                    signedIn: page.map { !$0.login && !$0.text.isEmpty } ?? false,
                    text: page?.text ?? "",
                    menus: MainMenu.titles,
                    activationPolicy: NSApp.activationPolicy() == .regular ? "regular" : "accessory",
                    png: wrote ? path : "",
                    lights: controller.trafficLightFrames(),
                    lightsWidth: controller.lightsWidth,
                    windowFrame: screenFrame(controller.window),
                    shell: page?.shell ?? ""
                )
                print(NotifyDebug.encode(report))
                done(wrote ? 0 : 1)
            }
        }
    }
}
