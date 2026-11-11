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
    }

    static func run(path: String, hash: String?, seconds: Double) -> Int32 {
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
                    DispatchQueue.main.asyncAfter(deadline: .now() + max(1, seconds)) {
                        MainActor.assumeIsolated {
                            capture(controller: controller, path: path) { result in
                                code = result
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
        let script = "JSON.stringify({text: (document.body ? document.body.innerText : '').replace(/\\s+/g, ' ').slice(0, 240), login: !!document.querySelector('input[type=password]')})"
        webView.evaluateJavaScript(script) { value, _ in
            struct Page: Decodable { var text: String; var login: Bool }
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
                    png: wrote ? path : ""
                )
                print(NotifyDebug.encode(report))
                done(wrote ? 0 : 1)
            }
        }
    }
}
