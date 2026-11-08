import AppKit
import LanaMenuCore
import SwiftUI

struct LanaMenuApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @StateObject private var model = AppModel()

    var body: some Scene {
        MenuBarExtra {
            MenuContent(model: model)
        } label: {
            Image(nsImage: MarkImages.menuBar)
                .accessibilityLabel("lana")
        }
        .menuBarExtraStyle(.window)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        // The bundle sets LSUIElement; this keeps the Dock icon away when the
        // binary runs outside the bundle too (swift run, --dump-status tests).
        NSApp.setActivationPolicy(.accessory)
    }
}

/// MarkImages are the lana mark images the app draws itself from the logo paths.
enum MarkImages {
    /// menuBar is the monochrome template icon for the status bar (20 × 16 pt).
    /// It is drawn on demand, so it stays crisp at every scale.
    static let menuBar: NSImage = {
        let image = NSImage(size: NSSize(width: 20, height: 16), flipped: false) { rect in
            guard let ctx = NSGraphicsContext.current?.cgContext else {
                return false
            }
            LanaMark.draw(in: ctx, rect: rect.insetBy(dx: 0.5, dy: 1), style: .template)
            return true
        }
        image.isTemplate = true
        image.accessibilityDescription = "lana"
        return image
    }()

    /// tile is the colour app icon used in the menu header.
    static let tile: NSImage = {
        let image = NSImage(size: NSSize(width: 32, height: 32), flipped: false) { rect in
            guard let ctx = NSGraphicsContext.current?.cgContext, let icon = LanaMark.renderAppIcon(pixels: 128) else {
                return false
            }
            ctx.interpolationQuality = .high
            ctx.draw(icon, in: rect)
            return true
        }
        return image
    }()
}
