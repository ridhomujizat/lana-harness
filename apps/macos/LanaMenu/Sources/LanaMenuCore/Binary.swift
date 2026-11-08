import Darwin
import Foundation

/// BinaryResolution says which lana binary the app would start, and why.
public struct BinaryResolution: Equatable, Codable {
    public var path: String?
    /// source is "setting", "login shell PATH", "fallback", or "not found".
    public var source: String

    public init(path: String?, source: String) {
        self.path = path
        self.source = source
    }
}

/// fallbackBinaryPaths are tried after the setting and the login shell PATH.
/// ~/.local/bin is where install.sh and `make install` put lana.
public func fallbackBinaryPaths(userHome: String) -> [String] {
    [
        (userHome as NSString).appendingPathComponent(".local/bin/lana"),
        (userHome as NSString).appendingPathComponent("go/bin/lana"),
        "/opt/homebrew/bin/lana",
        "/usr/local/bin/lana",
    ]
}

/// resolveBinary picks the lana binary: the path from the app settings, then
/// `lana` on the login shell PATH, then the fallback paths.
public func resolveBinary(
    setting: String?,
    loginShellBinary: String?,
    userHome: String,
    isExecutable: (String) -> Bool = { FileManager.default.isExecutableFile(atPath: $0) }
) -> BinaryResolution {
    if let raw = setting?.trimmingCharacters(in: .whitespacesAndNewlines), !raw.isEmpty {
        let path = expandTilde(raw, userHome: userHome)
        return BinaryResolution(path: isExecutable(path) ? path : nil, source: "setting")
    }
    if let path = loginShellBinary, path.hasPrefix("/"), isExecutable(path) {
        return BinaryResolution(path: path, source: "login shell PATH")
    }
    for path in fallbackBinaryPaths(userHome: userHome) where isExecutable(path) {
        return BinaryResolution(path: path, source: "fallback")
    }
    return BinaryResolution(path: nil, source: "not found")
}

/// LoginShellEnvironment is what the user's login shell knows that a GUI app
/// does not: its PATH and where `lana` is on it.
public struct LoginShellEnvironment: Equatable {
    public var path: String?
    public var lanaBinary: String?
}

/// parseLoginShellOutput reads the marker lines printed by the probe script,
/// ignoring anything else a shell profile prints.
public func parseLoginShellOutput(_ output: String) -> LoginShellEnvironment {
    var env = LoginShellEnvironment()
    for line in output.components(separatedBy: .newlines) {
        if line.hasPrefix("__LANA_PATH=") {
            let value = String(line.dropFirst("__LANA_PATH=".count))
            env.path = value.isEmpty ? nil : value
        } else if line.hasPrefix("__LANA_BIN=") {
            let value = String(line.dropFirst("__LANA_BIN=".count))
            env.lanaBinary = value.hasPrefix("/") ? value : nil
        }
    }
    return env
}

/// probeLoginShell runs the user's login shell once to read PATH and
/// `command -v lana`. It gives up after the timeout so a slow profile never
/// blocks the app.
public func probeLoginShell(timeout: TimeInterval = 4) -> LoginShellEnvironment {
    let shell = ProcessInfo.processInfo.environment["SHELL"].flatMap { $0.hasPrefix("/") ? $0 : nil } ?? "/bin/zsh"
    let script = "printf '\\n__LANA_PATH=%s\\n' \"$PATH\"; printf '__LANA_BIN=%s\\n' \"$(command -v lana 2>/dev/null)\""
    // -i reads .zshrc too, where many people set PATH; stdin is /dev/null so
    // an interactive profile cannot wait for input.
    guard let output = runCapture(shell, ["-l", "-i", "-c", script], timeout: timeout) else {
        return LoginShellEnvironment()
    }
    return parseLoginShellOutput(output)
}

/// runCapture runs a program and returns its stdout, or nil on a timeout or a
/// launch error.
public func runCapture(_ executable: String, _ arguments: [String], environment: [String: String]? = nil, timeout: TimeInterval) -> String? {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: executable)
    process.arguments = arguments
    if let environment {
        process.environment = environment
    }
    let out = Pipe()
    process.standardOutput = out
    process.standardError = FileHandle.nullDevice
    process.standardInput = FileHandle.nullDevice
    let done = DispatchSemaphore(value: 0)
    process.terminationHandler = { _ in done.signal() }
    do {
        try process.run()
    } catch {
        return nil
    }
    var data = Data()
    let reader = DispatchQueue(label: "lana.runCapture")
    let readDone = DispatchSemaphore(value: 0)
    reader.async {
        data = out.fileHandleForReading.readDataToEndOfFile()
        readDone.signal()
    }
    if done.wait(timeout: .now() + timeout) == .timedOut {
        process.terminate()
        _ = done.wait(timeout: .now() + 1)
        return nil
    }
    _ = readDone.wait(timeout: .now() + 1)
    return String(decoding: data, as: UTF8.self)
}

/// parseVersion turns `lana --version` output ("lana 0.1.0 (abc, date)") into
/// the version ("0.1.0").
public func parseVersion(_ output: String) -> String? {
    let line = output.split(whereSeparator: \.isNewline).first.map(String.init) ?? ""
    let fields = line.split(separator: " ")
    guard fields.count >= 2, fields[0] == "lana" else {
        return nil
    }
    return String(fields[1])
}

/// VersionCache runs `<exe> --version` once per executable and modification
/// time, so the menu shows the version of the binary the daemon runs.
public final class VersionCache: @unchecked Sendable {
    private var cache: [String: String] = [:]
    private let lock = NSLock()

    public init() {}

    public func version(of executable: String) -> String? {
        let attrs = try? FileManager.default.attributesOfItem(atPath: executable)
        let modified = (attrs?[.modificationDate] as? Date)?.timeIntervalSince1970 ?? 0
        let key = "\(executable)|\(modified)"
        lock.lock()
        if let hit = cache[key] {
            lock.unlock()
            return hit
        }
        lock.unlock()
        guard let output = runCapture(executable, ["--version"], timeout: 3), let version = parseVersion(output) else {
            return nil
        }
        lock.lock()
        cache[key] = version
        lock.unlock()
        return version
    }
}

/// startDaemon runs `<binary> daemon` in its own session with LANA_HOME set,
/// stdin from /dev/null and stdout and stderr appended to logPath. It returns
/// the child PID. The child keeps running when the app quits.
public func startDaemon(binary: String, home: LanaHome, path: String?, baseEnvironment: [String: String]) throws -> Int32 {
    try FileManager.default.createDirectory(atPath: home.path, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
    var env = baseEnvironment
    env["LANA_HOME"] = home.path
    if let path, !path.isEmpty {
        // A GUI app starts with a minimal PATH; the daemon needs the user's
        // PATH to find claude, git, node, and the other tools it runs.
        env["PATH"] = path
    }
    let envp = env.map { "\($0.key)=\($0.value)" }

    var actions: posix_spawn_file_actions_t?
    posix_spawn_file_actions_init(&actions)
    defer { posix_spawn_file_actions_destroy(&actions) }
    posix_spawn_file_actions_addopen(&actions, 0, "/dev/null", O_RDONLY, 0)
    posix_spawn_file_actions_addopen(&actions, 1, home.daemonLogURL.path, O_WRONLY | O_CREAT | O_APPEND, 0o600)
    posix_spawn_file_actions_adddup2(&actions, 1, 2)
    posix_spawn_file_actions_addchdir_np(&actions, home.path)

    var attrs: posix_spawnattr_t?
    posix_spawnattr_init(&attrs)
    defer { posix_spawnattr_destroy(&attrs) }
    posix_spawnattr_setflags(&attrs, Int16(POSIX_SPAWN_SETSID | POSIX_SPAWN_CLOEXEC_DEFAULT))

    var pid: pid_t = 0
    let argv = [binary, "daemon"]
    let status = withCStrings(argv) { argvPtr in
        withCStrings(envp) { envPtr in
            posix_spawn(&pid, binary, &actions, &attrs, argvPtr, envPtr)
        }
    }
    if status != 0 {
        throw NSError(domain: NSPOSIXErrorDomain, code: Int(status), userInfo: [
            NSLocalizedDescriptionKey: "Could not start \(binary): \(String(cString: strerror(status)))",
        ])
    }
    // Reap the child when it exits so it never stays a zombie while the app runs.
    let child = pid
    DispatchQueue.global(qos: .utility).async {
        var stat: Int32 = 0
        _ = waitpid(child, &stat, 0)
    }
    return pid
}

private func withCStrings<R>(_ strings: [String], _ body: ([UnsafeMutablePointer<CChar>?]) -> R) -> R {
    var pointers: [UnsafeMutablePointer<CChar>?] = strings.map { strdup($0) }
    pointers.append(nil)
    defer { pointers.forEach { free($0) } }
    return body(pointers)
}

/// stopDaemon sends SIGTERM, the signal `lana daemon` handles with a graceful
/// shutdown.
public func stopDaemon(pid: Int32) throws {
    if kill(pid, SIGTERM) != 0 {
        let code = errno
        throw NSError(domain: NSPOSIXErrorDomain, code: Int(code), userInfo: [
            NSLocalizedDescriptionKey: "Could not stop lana (pid \(pid)): \(String(cString: strerror(code)))",
        ])
    }
}
