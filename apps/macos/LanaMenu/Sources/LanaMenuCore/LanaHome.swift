import Foundation

/// The default web address of the daemon when config.toml has no [web] addr
/// (core.DefaultWebAddr in the Go code).
public let defaultWebAddr = "127.0.0.1:18300"

/// LanaHome names the files the menu bar app reads under $LANA_HOME. The app
/// never writes there except menubar-daemon.log when it starts the daemon.
public struct LanaHome: Equatable, Sendable {
    public let path: String

    public init(path: String) {
        self.path = path
    }

    public var configURL: URL { file("config.toml") }
    public var lockURL: URL { file("daemon.lock") }
    public var secretURL: URL { file("daemon.secret") }
    public var daemonLogURL: URL { file("menubar-daemon.log") }

    private func file(_ name: String) -> URL {
        URL(fileURLWithPath: path).appendingPathComponent(name)
    }

    /// resolve picks the home: a non-empty override from the app settings,
    /// then the home the bundle is built for (Info.plist LanaHome, the dev
    /// app's ~/.lana-dev, spec #52), then LANA_HOME from the environment the
    /// app was launched with, then ~/.lana. The bundle home comes before the
    /// environment so the dev app never follows a LANA_HOME meant for the
    /// production daemon. It mirrors config.HomeDir in Go; only the override
    /// and the bundle home expand "~".
    public static func resolve(override: String?, bundleHome: String? = nil, environment: [String: String], userHome: String) -> LanaHome {
        if let raw = override?.trimmingCharacters(in: .whitespacesAndNewlines), !raw.isEmpty {
            return LanaHome(path: expandTilde(raw, userHome: userHome))
        }
        if let raw = bundleHome?.trimmingCharacters(in: .whitespacesAndNewlines), !raw.isEmpty {
            return LanaHome(path: expandTilde(raw, userHome: userHome))
        }
        if let env = environment["LANA_HOME"], !env.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return LanaHome(path: env)
        }
        return LanaHome(path: (userHome as NSString).appendingPathComponent(".lana"))
    }
}

/// expandTilde turns a leading "~" or "~/" into the user home.
public func expandTilde(_ path: String, userHome: String) -> String {
    if path == "~" {
        return userHome
    }
    if path.hasPrefix("~/") {
        return (userHome as NSString).appendingPathComponent(String(path.dropFirst(2)))
    }
    return path
}

/// WebSettings is the part of the [web] table the app needs.
public struct WebSettings: Equatable {
    public var addr: String
    public var enabled: Bool
    /// addrFromConfig is true when config.toml named [web] addr.
    public var addrFromConfig: Bool

    public init(addr: String = defaultWebAddr, enabled: Bool = true, addrFromConfig: Bool = false) {
        self.addr = addr
        self.enabled = enabled
        self.addrFromConfig = addrFromConfig
    }
}

/// parseWebSettings reads addr and enabled from the [web] table of a TOML
/// document. It is a deliberately small reader: tables, `key = value` lines,
/// basic and literal strings, booleans, and comments. Anything it cannot read
/// keeps the default, so a config the app does not understand still works.
public func parseWebSettings(toml: String) -> WebSettings {
    var settings = WebSettings()
    var table = ""
    for rawLine in toml.components(separatedBy: .newlines) {
        let line = stripTOMLComment(rawLine).trimmingCharacters(in: .whitespaces)
        if line.isEmpty {
            continue
        }
        if line.hasPrefix("[") {
            // [[array]] tables are never [web]; keep the name so their keys are skipped.
            let inner = line.trimmingCharacters(in: CharacterSet(charactersIn: "[]")).trimmingCharacters(in: .whitespaces)
            table = line.hasPrefix("[[") ? "[[" + inner : inner
            continue
        }
        guard let eq = line.firstIndex(of: "=") else {
            continue
        }
        var key = line[..<eq].trimmingCharacters(in: .whitespaces)
        let value = line[line.index(after: eq)...].trimmingCharacters(in: .whitespaces)
        key = unquoteKey(key)
        var fullKey = table.isEmpty ? key : table + "." + key
        fullKey = fullKey.replacingOccurrences(of: " ", with: "")
        switch fullKey {
        case "web.addr":
            if let addr = parseTOMLString(value), !addr.isEmpty {
                settings.addr = addr
                settings.addrFromConfig = true
            }
        case "web.enabled":
            if value == "true" {
                settings.enabled = true
            } else if value == "false" {
                settings.enabled = false
            }
        default:
            break
        }
    }
    return settings
}

/// stripTOMLComment drops a "#" comment that is outside a string.
func stripTOMLComment(_ line: String) -> String {
    var quote: Character?
    var escaped = false
    var out = ""
    for ch in line {
        if let q = quote {
            out.append(ch)
            if q == "\"" && ch == "\\" && !escaped {
                escaped = true
                continue
            }
            if ch == q && !escaped {
                quote = nil
            }
            escaped = false
            continue
        }
        if ch == "#" {
            break
        }
        if ch == "\"" || ch == "'" {
            quote = ch
        }
        out.append(ch)
    }
    return out
}

func unquoteKey(_ key: String) -> String {
    if key.count >= 2, let first = key.first, first == key.last, first == "\"" || first == "'" {
        return String(key.dropFirst().dropLast())
    }
    return key
}

/// parseTOMLString reads a basic ("…") or literal ('…') TOML string value.
func parseTOMLString(_ value: String) -> String? {
    guard let first = value.first, first == "\"" || first == "'" else {
        return nil
    }
    var out = ""
    var escaped = false
    for ch in value.dropFirst() {
        if first == "\"" && escaped {
            switch ch {
            case "n": out.append("\n")
            case "t": out.append("\t")
            default: out.append(ch)
            }
            escaped = false
            continue
        }
        if first == "\"" && ch == "\\" {
            escaped = true
            continue
        }
        if ch == first {
            return out
        }
        out.append(ch)
    }
    return nil
}

/// DaemonSecret is $LANA_HOME/daemon.secret: the bound web address and the
/// local control secret the daemon writes after its listener is up.
public struct DaemonSecret: Codable, Equatable, Sendable {
    public var addr: String
    public var secret: String
}

public func readDaemonSecret(_ home: LanaHome) -> DaemonSecret? {
    guard let data = try? Data(contentsOf: home.secretURL),
          let stored = try? JSONDecoder().decode(DaemonSecret.self, from: data),
          !stored.addr.isEmpty, !stored.secret.isEmpty
    else {
        return nil
    }
    return stored
}

/// readDaemonLock returns the PID in $LANA_HOME/daemon.lock and the file's
/// modification time (the daemon rewrites the PID on every start).
public func readDaemonLock(_ home: LanaHome) -> (pid: Int32, modified: Date)? {
    guard let text = try? String(contentsOf: home.lockURL, encoding: .utf8),
          let pid = Int32(text.trimmingCharacters(in: .whitespacesAndNewlines)), pid > 1
    else {
        return nil
    }
    let attrs = try? FileManager.default.attributesOfItem(atPath: home.lockURL.path)
    let modified = (attrs?[.modificationDate] as? Date) ?? Date()
    return (pid, modified)
}

public func readWebSettings(_ home: LanaHome) -> WebSettings {
    guard let text = try? String(contentsOf: home.configURL, encoding: .utf8) else {
        return WebSettings()
    }
    return parseWebSettings(toml: text)
}
