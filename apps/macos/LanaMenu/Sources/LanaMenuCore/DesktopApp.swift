import Foundation

// The desktop window of spec #52: the dashboard in a WKWebView inside Lana.app.
// This file holds the parts that need no AppKit, so the tests cover them.

/// appLinkRequest builds POST /v1/local/app-link (API-DSK-001). next is an
/// optional dashboard hash ("#/calendar") the link opens after the sign-in.
public func appLinkRequest(secret: DaemonSecret, next: String?) -> URLRequest? {
    guard var components = URLComponents(string: "http://" + secret.addr + "/v1/local/app-link") else {
        return nil
    }
    if let next, !next.isEmpty {
        components.percentEncodedQuery = "next=" + formEncode(next)
    }
    guard let url = components.url else {
        return nil
    }
    var request = URLRequest(url: url, timeoutInterval: 5)
    request.httpMethod = "POST"
    request.setValue(secret.secret, forHTTPHeaderField: "X-Lana-Local-Secret")
    return request
}

/// requestAppLink asks the running daemon for a one-time app link. The window
/// loads it, so its own cookie jar gets a dashboard session without a
/// password. A daemon older than spec #52 answers 404 (LocalControlError.status).
public func requestAppLink(home: LanaHome, next: String?) async throws -> URL {
    guard let secret = readDaemonSecret(home), let request = appLinkRequest(secret: secret, next: next) else {
        throw LocalControlError.notRunning
    }
    let (data, response) = try await URLSession.shared.data(for: request)
    let code = (response as? HTTPURLResponse)?.statusCode ?? 0
    guard code == 200 else {
        throw LocalControlError.status(code, String(decoding: data.prefix(512), as: UTF8.self))
    }
    struct Answer: Decodable { var link: String }
    guard let answer = try? JSONDecoder().decode(Answer.self, from: data), let url = URL(string: answer.link),
          url.scheme == "http", hostPort(of: url) == secret.addr.lowercased()
    else {
        throw LocalControlError.invalidAnswer
    }
    return url
}

/// NavigationTarget says where the window sends a link.
public enum NavigationTarget: Equatable {
    /// inApp stays in the Lana window: the dashboard origin.
    case inApp
    /// external opens in the default browser or app (http, https, mailto …).
    case external
    /// allow lets the web view handle it (about:, blob:, data:).
    case allow
}

/// hostPort returns "host:port" of an http URL in lower case, with the default
/// port 80 filled in.
public func hostPort(of url: URL) -> String? {
    guard let host = url.host?.lowercased() else {
        return nil
    }
    let port = url.port ?? (url.scheme?.lowercased() == "https" ? 443 : 80)
    return host + ":" + String(port)
}

/// classifyNavigation decides where a link goes: the dashboard origin
/// (http://<addr>) stays in the window, about/blob/data stay with the web
/// view, and everything else leaves for the default browser or app.
public func classifyNavigation(_ url: URL, dashboardAddr: String) -> NavigationTarget {
    switch url.scheme?.lowercased() {
    case "about", "blob", "data":
        return .allow
    case "http":
        return hostPort(of: url) == dashboardAddr.lowercased() ? .inApp : .external
    default:
        return .external
    }
}

/// bundledBinary is the lana binary inside the app bundle
/// (Lana.app/Contents/MacOS/lana), or nil outside a bundle.
public func bundledBinary(bundleURL: URL) -> String? {
    guard bundleURL.pathExtension == "app" else {
        return nil
    }
    return bundleURL.appendingPathComponent("Contents/MacOS/lana").path
}

/// dashboardHashOf returns the "#/…" part of a dashboard URL, or nil.
public func dashboardHashOf(_ url: URL) -> String? {
    guard let fragment = url.fragment, fragment.hasPrefix("/") else {
        return nil
    }
    return "#" + fragment
}

/// placeholderHTML is the page the window shows while the dashboard cannot
/// load. canStart shows the Start lana button.
public func placeholderHTML(title: String, detail: String, canStart: Bool) -> String {
    func escape(_ s: String) -> String {
        s.replacingOccurrences(of: "&", with: "&amp;")
            .replacingOccurrences(of: "<", with: "&lt;")
            .replacingOccurrences(of: ">", with: "&gt;")
            .replacingOccurrences(of: "\"", with: "&quot;")
    }
    let button = canStart
        ? "<button onclick=\"window.webkit.messageHandlers.lana.postMessage('start')\">Start lana</button>"
        : ""
    return """
    <!doctype html><html><head><meta charset="utf-8"><title>lana</title><style>
    :root { color-scheme: light dark; --bg: #f7f7f8; --fg: #16171a; --muted: #6b6f76; --btn: #16171a; --btnfg: #fff; }
    @media (prefers-color-scheme: dark) { :root { --bg: #0d0f12; --fg: #eceef1; --muted: #9aa0a8; --btn: #eceef1; --btnfg: #0d0f12; } }
    html, body { height: 100%; margin: 0; background: var(--bg); color: var(--fg);
      font: 14px -apple-system, BlinkMacSystemFont, "Helvetica Neue", sans-serif; }
    main { height: 100%; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 10px; padding: 0 16px; text-align: center; }
    h1 { font-size: 18px; font-weight: 600; margin: 0; }
    p { margin: 0; color: var(--muted); max-width: 420px; line-height: 1.5; }
    button { margin-top: 8px; border: 0; border-radius: 8px; padding: 8px 16px; font: inherit; font-weight: 600;
      background: var(--btn); color: var(--btnfg); cursor: pointer; }
    </style></head><body><main data-drag-region>
    <h1>\(escape(title))</h1><p>\(escape(detail))</p>\(button)
    </main></body></html>
    """
}
