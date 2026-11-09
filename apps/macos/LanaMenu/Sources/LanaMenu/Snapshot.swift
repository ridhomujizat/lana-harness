import AppKit
import SwiftUI

/// Snapshot renders the menu window offscreen to a PNG with live data, for
/// docs and checks (`LanaMenu --snapshot out.png [--dark] [--seconds N]`). It samples every
/// two seconds for N seconds (at least 2.6), as the open menu does, so CPU %
/// and the sparklines are filled in.
enum Snapshot {
    static func run(path: String, dark: Bool, seconds: Double) -> Int32 {
        let app = NSApplication.shared
        app.setActivationPolicy(.prohibited)
        var code: Int32 = 1
        DispatchQueue.main.async {
            MainActor.assumeIsolated {
                let model = AppModel()
                model.menuOpened()
                // Wait for the second sample and the version lookup.
                DispatchQueue.main.asyncAfter(deadline: .now() + max(2.6, seconds)) {
                    MainActor.assumeIsolated {
                        code = render(model: model, path: path, dark: dark)
                        model.menuClosed()
                        app.stop(nil)
                        // stop takes effect after the next event.
                        app.postEvent(NSEvent.otherEvent(with: .applicationDefined, location: .zero, modifierFlags: [], timestamp: 0, windowNumber: 0, context: nil, subtype: 0, data1: 0, data2: 0)!, atStart: false)
                    }
                }
            }
        }
        app.run()
        return code
    }

    @MainActor
    private static func render(model: AppModel, path: String, dark: Bool) -> Int32 {
        let root = MenuContent(model: model)
            .padding(.vertical, 2)
            .background(Color(nsColor: .windowBackgroundColor))
        let host = NSHostingView(rootView: root)
        host.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        let size = host.fittingSize
        host.frame = NSRect(origin: .zero, size: size)
        let window = NSWindow(contentRect: host.frame, styleMask: [.borderless], backing: .buffered, defer: false)
        window.contentView = host
        window.appearance = host.appearance
        host.layoutSubtreeIfNeeded()
        host.display()
        guard let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds) else {
            return 1
        }
        host.cacheDisplay(in: host.bounds, to: rep)
        guard let data = rep.representation(using: .png, properties: [:]) else {
            return 1
        }
        do {
            try data.write(to: URL(fileURLWithPath: path))
        } catch {
            fputs("error: \(error)\n", stderr)
            return 1
        }
        print("wrote \(path) (\(Int(size.width))×\(Int(size.height)) pt)")
        return 0
    }
}
