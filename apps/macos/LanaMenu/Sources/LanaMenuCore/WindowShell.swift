import Foundation

// The title-bar-less window of spec #52: the dashboard draws the whole window,
// the traffic lights sit in its top row, and rows marked data-drag-region move
// the window. These parts need no AppKit, so the tests cover them.

/// ShellLayout is where the traffic lights go: vertically centred in a top
/// row as tall as the dashboard topbar (46 pt), 20 pt from the left.
public enum ShellLayout {
    public static let rowHeight: Double = 46
    public static let leftInset: Double = 20
    /// gap is the space between the zoom button and the first control after it.
    public static let gap: Double = 10

    /// lightsWidth is the inset the page leaves for the lights: from the left
    /// edge to the zoom button's right edge, plus the gap.
    public static func lightsWidth(zoomMaxX: Double) -> Int {
        Int((zoomMaxX + gap).rounded(.up))
    }

    /// buttonY centres a button of the given height in the top row, in
    /// AppKit's bottom-up coordinates of the row.
    public static func buttonY(height: Double) -> Double {
        ((rowHeight - height) / 2).rounded()
    }
}

/// ShellMessage is what the page posts to window.webkit.messageHandlers.lanaShell.
public enum ShellMessage: Equatable {
    /// drag starts a window drag from the mouse-down in a drag region.
    case drag
    /// doubleClick is a double click in a drag region (zoom or minimize).
    case doubleClick
    /// theme is the dashboard theme the page shows ("dark" or "light").
    case theme(dark: Bool)

    public static func parse(_ body: Any) -> ShellMessage? {
        guard let text = body as? String else {
            return nil
        }
        switch text {
        case "drag": return .drag
        case "dblclick": return .doubleClick
        case "theme:dark": return .theme(dark: true)
        case "theme:light": return .theme(dark: false)
        default: return nil
        }
    }
}

/// DoubleClickAction is what a double click on the title area does, from the
/// macOS setting "Double-click a window's title bar to" (AppleActionOnDoubleClick).
public enum DoubleClickAction: Equatable {
    case zoom, minimize, none

    public static func from(setting: String?) -> DoubleClickAction {
        switch setting {
        case "Minimize": return .minimize
        case "None": return .none
        default: return .zoom
        }
    }
}

/// shellBootstrapScript is the user script the window injects at document
/// start. It marks the page (html[data-shell="macos-app"], data-shell-variant
/// for the dev app), sets --shell-lights-w, reports the dashboard theme, and
/// posts "drag" or "dblclick" for a primary mouse-down or double click on a
/// data-drag-region element that is not interactive itself, so text
/// selection and controls elsewhere work as before.
public func shellBootstrapScript(variant: AppVariant, lightsWidth: Int) -> String {
    let variantLine = variant == .dev ? "d.dataset.shellVariant = \"dev\";" : ""
    return """
    (function () {
      var d = document.documentElement;
      if (!d || d.dataset.shell === "macos-app") { return; }
      d.dataset.shell = "macos-app";
      \(variantLine)
      d.style.setProperty("--shell-lights-w", "\(lightsWidth)px");
      var post = function (m) {
        try { window.webkit.messageHandlers.lanaShell.postMessage(m); } catch (e) {}
      };
      var interactive = "button, a[href], input, textarea, select, label, summary, [role=button], [role=link], [role=tab], [role=menuitem], [contenteditable]:not([contenteditable=false])";
      var inRegion = function (t) {
        if (!(t instanceof Element)) { return false; }
        if (t.closest(interactive)) { return false; }
        return !!t.closest("[data-drag-region]");
      };
      document.addEventListener("mousedown", function (e) {
        if (e.button !== 0 || e.detail > 1 || !inRegion(e.target)) { return; }
        e.preventDefault();
        post("drag");
      }, true);
      document.addEventListener("dblclick", function (e) {
        if (e.button === 0 && inRegion(e.target)) { post("dblclick"); }
      }, true);
      var sent = "";
      var report = function () {
        var t = d.getAttribute("data-theme");
        if ((t === "dark" || t === "light") && t !== sent) { sent = t; post("theme:" + t); }
      };
      new MutationObserver(report).observe(d, { attributes: true, attributeFilter: ["data-theme"] });
      report();
    })();
    """
}

/// fullscreenScript sets or clears html[data-shell-fullscreen]: the lights
/// hide in full screen, so the page drops their inset.
public func fullscreenScript(_ on: Bool) -> String {
    "document.documentElement.toggleAttribute(\"data-shell-fullscreen\", \(on ? "true" : "false"));"
}
