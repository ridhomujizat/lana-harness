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
                .accessibilityLabel(AppVariant.current.displayName)
        }
        .menuBarExtraStyle(.window)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        // The bundle sets LSUIElement; this keeps the Dock icon away when the
        // binary runs outside the bundle too (swift run, --dump-status tests).
        // The lana window turns the Dock icon on while it is open (spec #52).
        NSApp.setActivationPolicy(.accessory)
        let atLogin = Self.launchedAsLoginItem()
        guard !atLogin, Bundle.main.bundleURL.pathExtension == "app", AppSettings().windowAtLaunch else {
            return
        }
        DispatchQueue.main.async {
            MainActor.assumeIsolated { WebWindowController.shared.show() }
        }
    }

    /// Opening Lana.app again (Finder, Spotlight, the Dock, `open -a`) while it
    /// runs shows the lana window.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        MainActor.assumeIsolated { WebWindowController.shared.show() }
        return true
    }

    /// launchedAsLoginItem reads the open-application Apple event: macOS marks
    /// a launch at login with keyAELaunchedAsLogInItem.
    static func launchedAsLoginItem() -> Bool {
        guard let event = NSAppleEventManager.shared().currentAppleEvent, event.eventID == AEEventID(kAEOpenApplication) else {
            return false
        }
        return event.paramDescriptor(forKeyword: AEKeyword(keyAEPropData))?.enumCodeValue == OSType(keyAELaunchedAsLogInItem)
    }
}

/// MarkImages are the lana mark images the app draws itself from the logo paths.
enum MarkImages {
    /// menuBar is the monochrome template icon for the status bar (20 × 16 pt).
    /// It is drawn on demand, so it stays crisp at every scale. The dev app
    /// (spec #52) draws it in yellow instead, so it never looks like the
    /// production app: #F5C400 on a dark menu bar, a darker amber on a light one.
    static let menuBar: NSImage = {
        let dev = AppVariant.current == .dev
        let image = NSImage(size: NSSize(width: 20, height: 16), flipped: false) { rect in
            guard let ctx = NSGraphicsContext.current?.cgContext else {
                return false
            }
            guard dev else {
                LanaMark.draw(in: ctx, rect: rect.insetBy(dx: 0.5, dy: 1), style: .template)
                return true
            }
            let dark = NSAppearance.currentDrawing().bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            // Draw the template shape (with its gap) into a layer, then colour it.
            ctx.beginTransparencyLayer(auxiliaryInfo: nil)
            LanaMark.draw(in: ctx, rect: rect.insetBy(dx: 0.5, dy: 1), style: .template)
            ctx.setBlendMode(.sourceIn)
            ctx.setFillColor(dark ? AppVariant.devYellow : AppVariant.devAmber)
            ctx.fill(rect)
            ctx.endTransparencyLayer()
            return true
        }
        image.isTemplate = !dev
        image.accessibilityDescription = AppVariant.current.displayName
        return image
    }()

    /// tile is the colour app icon used in the menu header.
    static let tile: NSImage = {
        let image = NSImage(size: NSSize(width: 32, height: 32), flipped: false) { rect in
            guard let ctx = NSGraphicsContext.current?.cgContext, let icon = LanaMark.renderAppIcon(pixels: 128, variant: AppVariant.current) else {
                return false
            }
            ctx.interpolationQuality = .high
            ctx.draw(icon, in: rect)
            return true
        }
        return image
    }()
}
