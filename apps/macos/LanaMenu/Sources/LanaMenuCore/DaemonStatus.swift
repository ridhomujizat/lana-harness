import Foundation

/// DaemonStatus is what the menu header shows.
public struct DaemonStatus: Codable, Equatable {
    public var home: String
    public var running: Bool
    public var pid: Int32?
    public var executable: String?
    public var version: String?
    /// startedAt is the daemon process start time (seconds since 1970).
    public var startedAt: TimeInterval?
    public var uptimeSeconds: Int?
    /// addr is the dashboard address: the bound address from daemon.secret
    /// while the daemon runs, else [web] addr from config.toml, else the default.
    public var addr: String
    /// addrSource is "daemon.secret", "config.toml", or "default".
    public var addrSource: String
    public var webEnabled: Bool
    /// localControl is true when daemon.secret is present, so the sign-in
    /// link can be requested.
    public var localControl: Bool

    public var dashboardBase: String {
        "http://" + addr + "/"
    }

    public func dashboardURL(hash: String) -> URL? {
        URL(string: dashboardBase + hash)
    }
}

/// readStatus reads the daemon status from the $LANA_HOME files and libproc.
/// A PID from daemon.lock counts as the daemon only while that process is
/// alive and started before the lock file was last written; the daemon writes
/// its PID on every start, so a reused PID started later never matches.
public func readStatus(home: LanaHome, reader: LibprocReader = LibprocReader(), versions: VersionCache? = nil, now: Date = Date()) -> DaemonStatus {
    let web = readWebSettings(home)
    var status = DaemonStatus(
        home: home.path,
        running: false,
        addr: web.addr,
        addrSource: web.addrFromConfig ? "config.toml" : "default",
        webEnabled: web.enabled,
        localControl: false
    )
    guard let lock = readDaemonLock(home), processAlive(lock.pid), let sample = reader.sample(pid: lock.pid) else {
        return status
    }
    if sample.startTime > lock.modified.timeIntervalSince1970 + 5 {
        return status
    }
    status.running = true
    status.pid = lock.pid
    status.executable = sample.path.isEmpty ? nil : sample.path
    status.startedAt = sample.startTime
    status.uptimeSeconds = max(0, Int(now.timeIntervalSince1970 - sample.startTime))
    if let secret = readDaemonSecret(home) {
        status.addr = secret.addr
        status.addrSource = "daemon.secret"
        status.localControl = true
    }
    if let versions, let exe = status.executable {
        status.version = versions.version(of: exe)
    }
    return status
}

/// LocalControlError is a failed sign-in link request.
public enum LocalControlError: LocalizedError {
    case notRunning
    case status(Int, String)
    case invalidAnswer

    public var errorDescription: String? {
        switch self {
        case .notRunning:
            return "lana is not running, or its daemon.secret is missing."
        case let .status(code, body):
            return "The daemon answered \(code): \(body)"
        case .invalidAnswer:
            return "The daemon returned an invalid sign-in link."
        }
    }
}

/// signInLinkRequest builds POST /v1/local/sign-in-link, the daemon-only
/// endpoint `lana web reset-login` uses. next is an optional dashboard hash
/// ("#/calendar") the link opens after the sign-in.
public func signInLinkRequest(secret: DaemonSecret, next: String?) -> URLRequest? {
    guard var components = URLComponents(string: "http://" + secret.addr + "/v1/local/sign-in-link") else {
        return nil
    }
    if let next, !next.isEmpty {
        // Encode next fully: URLComponents.queryItems would leave "/" and "?"
        // as they are.
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

func formEncode(_ value: String) -> String {
    var allowed = CharacterSet.alphanumerics
    allowed.insert(charactersIn: "-_.~")
    return value.addingPercentEncoding(withAllowedCharacters: allowed) ?? ""
}

/// requestSignInLink asks the running daemon for a one-time sign-in link.
public func requestSignInLink(home: LanaHome, next: String?) async throws -> URL {
    guard let secret = readDaemonSecret(home), let request = signInLinkRequest(secret: secret, next: next) else {
        throw LocalControlError.notRunning
    }
    let (data, response) = try await URLSession.shared.data(for: request)
    let code = (response as? HTTPURLResponse)?.statusCode ?? 0
    guard code == 200 else {
        throw LocalControlError.status(code, String(decoding: data.prefix(512), as: UTF8.self))
    }
    struct Answer: Decodable { var link: String }
    guard let answer = try? JSONDecoder().decode(Answer.self, from: data), let url = URL(string: answer.link),
          url.scheme == "http"
    else {
        throw LocalControlError.invalidAnswer
    }
    return url
}

// MARK: - Formatting

/// formatUptime renders seconds as "45s", "12m", "2h 05m", or "3d 4h".
public func formatUptime(_ seconds: Int) -> String {
    let s = max(0, seconds)
    if s < 60 {
        return "\(s)s"
    }
    if s < 3600 {
        return "\(s / 60)m"
    }
    if s < 86400 {
        return String(format: "%dh %02dm", s / 3600, (s % 3600) / 60)
    }
    return "\(s / 86400)d \((s % 86400) / 3600)h"
}

/// formatBytes renders memory the way the menu shows it: "146 MB", "1.24 GB".
public func formatBytes(_ bytes: UInt64) -> String {
    let mb = Double(bytes) / 1_048_576
    if mb < 1024 {
        return String(format: "%.0f MB", mb)
    }
    let gb = mb / 1024
    var text = String(format: gb < 10 ? "%.2f" : "%.1f", gb)
    while text.hasSuffix("0") {
        text.removeLast()
    }
    if text.hasSuffix(".") {
        text.removeLast()
    }
    return text + " GB"
}

/// formatPercent renders a percentage with one decimal under 10 and none above.
public func formatPercent(_ value: Double) -> String {
    if value < 10 {
        return String(format: "%.1f%%", value)
    }
    return String(format: "%.0f%%", value)
}

/// describeProcesses renders the process line under the usage numbers.
public func describeProcesses(_ usage: Usage) -> String {
    let others = usage.descendantCount - usage.claudeCodeCount
    var parts: [String] = []
    if usage.claudeCodeCount > 0 {
        parts.append("\(usage.claudeCodeCount) Claude Code process" + (usage.claudeCodeCount == 1 ? "" : "es"))
    }
    if others > 0 {
        parts.append(usage.claudeCodeCount > 0 ? "\(others) other" + (others == 1 ? "" : "s") : "\(others) other process" + (others == 1 ? "" : "es"))
    }
    if parts.isEmpty {
        return "lana daemon"
    }
    return "lana daemon + " + parts.joined(separator: ", ")
}
