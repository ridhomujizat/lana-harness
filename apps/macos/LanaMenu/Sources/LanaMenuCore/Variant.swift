import CoreGraphics
import Foundation

/// AppVariant is the build of Lana.app (spec #52): the normal app, or the
/// development app "Lana Dev" with its own bundle id, a yellow icon, and its
/// own LANA_HOME (~/.lana-dev), so it never talks to the production daemon.
/// Info.plist names it with the LanaVariant key; make app VARIANT=dev sets it.
public enum AppVariant: String, Codable, Sendable {
    case prod
    case dev

    /// variantKey and homeKey are the Info.plist keys of the variant.
    public static let variantKey = "LanaVariant"
    public static let homeKey = "LanaHome"

    /// from reads the variant from an Info.plist dictionary; anything else
    /// than "dev" is the normal app.
    public static func from(info: [String: Any]?) -> AppVariant {
        (info?[variantKey] as? String)?.lowercased() == "dev" ? .dev : .prod
    }

    /// current is the variant of the running bundle.
    public static var current: AppVariant {
        from(info: Bundle.main.infoDictionary)
    }

    /// displayName is the app name the menu and the window show.
    public var displayName: String {
        self == .dev ? "lana dev" : "lana"
    }

    /// tileColor is the background of the app icon tile.
    public var tileColor: CGColor {
        switch self {
        case .prod: return CGColor(srgbRed: 0x0B / 255, green: 0x0F / 255, blue: 0x14 / 255, alpha: 1)
        case .dev: return AppVariant.devYellow
        }
    }

    /// markColor is the colour of the mark on the tile.
    public var markColor: CGColor {
        switch self {
        case .prod: return CGColor(gray: 1, alpha: 1)
        case .dev: return CGColor(srgbRed: 0x0B / 255, green: 0x0F / 255, blue: 0x14 / 255, alpha: 1)
        }
    }

    /// devYellow (#F5C400) marks every surface of the development app.
    public static let devYellow = CGColor(srgbRed: 0xF5 / 255, green: 0xC4 / 255, blue: 0x00 / 255, alpha: 1)
    /// devAmber (#C99A00) is the yellow for a light menu bar, where #F5C400
    /// has too little contrast.
    public static let devAmber = CGColor(srgbRed: 0xC9 / 255, green: 0x9A / 255, blue: 0x00 / 255, alpha: 1)
}

/// bundleHome reads the LanaHome key of an Info.plist dictionary: the
/// LANA_HOME the bundle is built for ("~/.lana-dev" for the dev app), with
/// "~" expanded. An empty or missing key is nil (the normal app).
public func bundleHome(info: [String: Any]?, userHome: String) -> String? {
    guard let raw = (info?[AppVariant.homeKey] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines), !raw.isEmpty else {
        return nil
    }
    return expandTilde(raw, userHome: userHome)
}
