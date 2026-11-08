import Foundation

/// SSEMessage is one server-sent event.
public struct SSEMessage: Equatable {
    public var event: String
    public var id: String?
    public var data: String
}

/// SSEParser turns the lines of a text/event-stream into messages. Comment
/// lines (": ping") are dropped; a blank line ends a message.
public struct SSEParser {
    private var event = ""
    private var id: String?
    private var data: [String] = []

    public init() {}

    /// feed takes one line without its line break and returns the message a
    /// blank line completes.
    public mutating func feed(_ rawLine: String) -> SSEMessage? {
        let line = rawLine.hasSuffix("\r") ? String(rawLine.dropLast()) : rawLine
        if line.isEmpty {
            defer { event = ""; id = nil; data = [] }
            guard !data.isEmpty || !event.isEmpty else { return nil }
            return SSEMessage(event: event.isEmpty ? "message" : event, id: id, data: data.joined(separator: "\n"))
        }
        if line.hasPrefix(":") {
            return nil
        }
        let field: Substring
        var value: Substring
        if let colon = line.firstIndex(of: ":") {
            field = line[..<colon]
            value = line[line.index(after: colon)...]
            if value.hasPrefix(" ") { value = value.dropFirst() }
        } else {
            field = Substring(line)
            value = ""
        }
        switch field {
        case "event": event = String(value)
        case "data": data.append(String(value))
        case "id": id = String(value)
        default: break
        }
        return nil
    }
}

/// SubscriptionStatus is the state of the event subscription, shown in
/// Settings and printed by --dump-status.
public struct SubscriptionStatus: Codable, Equatable, Sendable {
    /// state is "idle", "waiting_for_daemon", "connecting", "subscribed",
    /// "retrying", or "stopped".
    public var state: String
    public var detail: String?
    public var addr: String?
    public var lastEventId: String?
    public var received: Int

    public init(state: String, detail: String? = nil, addr: String? = nil, lastEventId: String? = nil, received: Int = 0) {
        self.state = state
        self.detail = detail
        self.addr = addr
        self.lastEventId = lastEventId
        self.received = received
    }
}

/// localEventsRequest builds GET /v1/local/events (API-NTF-001). after is the
/// last event id seen, so the daemon replays what came after it.
public func localEventsRequest(secret: DaemonSecret, after: String?) -> URLRequest? {
    guard var components = URLComponents(string: "http://" + secret.addr + "/v1/local/events") else {
        return nil
    }
    if let after, !after.isEmpty {
        components.percentEncodedQuery = "after=" + formEncode(after)
    }
    guard let url = components.url else {
        return nil
    }
    // The stream is idle between events; the daemon sends a comment every
    // 20 s, so a 75 s idle timeout only trips on a dead connection.
    var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 75)
    request.setValue(secret.secret, forHTTPHeaderField: localSecretHeader)
    request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
    return request
}

/// retryDelay is the reconnect backoff: 1, 2, 4, 8, 16, then 30 seconds.
public func retryDelay(attempt: Int) -> TimeInterval {
    let capped = min(max(attempt, 0), 5)
    return min(30, pow(2, Double(capped)))
}

/// SeenEvents remembers the last event ids so a replay never posts twice.
public struct SeenEvents {
    private var order: [String] = []
    private var set: Set<String> = []
    private let limit: Int

    public init(limit: Int = 500) {
        self.limit = limit
    }

    /// insert returns false when id was seen before.
    public mutating func insert(_ id: String) -> Bool {
        guard !set.contains(id) else { return false }
        set.insert(id)
        order.append(id)
        if order.count > limit {
            set.remove(order.removeFirst())
        }
        return true
    }
}

/// EventSubscription follows GET /v1/local/events while the daemon runs. It
/// reads daemon.secret before every connect, reconnects with backoff, resumes
/// with ?after=<last id>, and drops repeated ids.
public final class EventSubscription: @unchecked Sendable {
    public typealias EventHandler = @Sendable (LanaEvent) -> Void
    public typealias StatusHandler = @Sendable (SubscriptionStatus) -> Void

    private let home: @Sendable () -> LanaHome
    private let onEvent: EventHandler
    private let onStatus: StatusHandler
    private let lock = NSLock()
    private var task: Task<Void, Never>?
    private var seen = SeenEvents()
    private var lastId: String?
    private var received = 0
    private let session: URLSession

    public init(home: @escaping @Sendable () -> LanaHome, onEvent: @escaping EventHandler, onStatus: @escaping StatusHandler) {
        self.home = home
        self.onEvent = onEvent
        self.onStatus = onStatus
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 75
        config.timeoutIntervalForResource = .infinity
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        session = URLSession(configuration: config)
    }

    /// start begins the subscription loop; a running loop is restarted, so a
    /// new LANA_HOME takes effect.
    public func start() {
        lock.lock()
        task?.cancel()
        task = Task.detached(priority: .utility) { [weak self] in
            await self?.run()
        }
        lock.unlock()
    }

    public func stop() {
        lock.lock()
        task?.cancel()
        task = nil
        lock.unlock()
        report(SubscriptionStatus(state: "stopped"))
    }

    private func report(_ status: SubscriptionStatus) {
        var status = status
        lock.lock()
        status.lastEventId = lastId
        status.received = received
        lock.unlock()
        onStatus(status)
    }

    private func run() async {
        var attempt = 0
        while !Task.isCancelled {
            guard let secret = readDaemonSecret(home()) else {
                report(SubscriptionStatus(state: "waiting_for_daemon", detail: "daemon.secret is missing"))
                try? await Task.sleep(nanoseconds: 3_000_000_000)
                continue
            }
            let after = lock.withLock { lastId }
            guard let request = localEventsRequest(secret: secret, after: after) else {
                report(SubscriptionStatus(state: "retrying", detail: "bad daemon address", addr: secret.addr))
                try? await Task.sleep(nanoseconds: 5_000_000_000)
                continue
            }
            report(SubscriptionStatus(state: "connecting", addr: secret.addr))
            var connected = false
            var failure = "stream ended"
            do {
                let (bytes, response) = try await session.bytes(for: request)
                let code = (response as? HTTPURLResponse)?.statusCode ?? 0
                if code != 200 {
                    failure = "daemon answered \(code)"
                } else {
                    var parser = SSEParser()
                    var line = Data()
                    for try await byte in bytes {
                        if byte != 0x0A {
                            line.append(byte)
                            continue
                        }
                        let text = String(decoding: line, as: UTF8.self)
                        line.removeAll(keepingCapacity: true)
                        guard let message = parser.feed(text) else { continue }
                        switch message.event {
                        case "ready":
                            connected = true
                            attempt = 0
                            report(SubscriptionStatus(state: "subscribed", addr: secret.addr))
                        case "notify":
                            handle(message)
                        case "shutdown":
                            failure = "daemon is stopping"
                        default:
                            break
                        }
                    }
                }
            } catch {
                if Task.isCancelled { return }
                failure = error.localizedDescription
            }
            if Task.isCancelled { return }
            let delay = connected ? 1 : retryDelay(attempt: attempt)
            attempt += 1
            report(SubscriptionStatus(state: "retrying", detail: failure + "; retry in \(Int(delay)) s", addr: secret.addr))
            try? await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000))
        }
    }

    private func handle(_ message: SSEMessage) {
        guard let event = try? LanaEvent.decode(message.data) else { return }
        lock.lock()
        let fresh = seen.insert(event.id)
        lastId = event.id
        if fresh { received += 1 }
        lock.unlock()
        guard fresh else { return }
        onEvent(event)
    }
}

/// probeSubscription connects to the event stream once and waits for its
/// ready event, for --dump-status.
public func probeSubscription(home: LanaHome, timeout: TimeInterval = 3) async -> SubscriptionStatus {
    guard let secret = readDaemonSecret(home) else {
        return SubscriptionStatus(state: "waiting_for_daemon", detail: "daemon.secret is missing")
    }
    guard var request = localEventsRequest(secret: secret, after: nil) else {
        return SubscriptionStatus(state: "retrying", detail: "bad daemon address", addr: secret.addr)
    }
    request.timeoutInterval = timeout
    let probe = Task { () -> SubscriptionStatus in
        do {
            let (bytes, response) = try await URLSession.shared.bytes(for: request)
            let code = (response as? HTTPURLResponse)?.statusCode ?? 0
            guard code == 200 else {
                return SubscriptionStatus(state: "retrying", detail: "daemon answered \(code)", addr: secret.addr)
            }
            var parser = SSEParser()
            var line = Data()
            for try await byte in bytes {
                if byte != 0x0A {
                    line.append(byte)
                    continue
                }
                let text = String(decoding: line, as: UTF8.self)
                line.removeAll()
                if let message = parser.feed(text), message.event == "ready" {
                    struct Ready: Decodable { var last_id: String }
                    let last = (try? JSONDecoder().decode(Ready.self, from: Data(message.data.utf8)))?.last_id
                    return SubscriptionStatus(state: "subscribed", addr: secret.addr, lastEventId: (last?.isEmpty ?? true) ? nil : last)
                }
            }
            return SubscriptionStatus(state: "retrying", detail: "stream ended before ready", addr: secret.addr)
        } catch {
            return SubscriptionStatus(state: "retrying", detail: error.localizedDescription, addr: secret.addr)
        }
    }
    let timer = Task {
        try? await Task.sleep(nanoseconds: UInt64(timeout * 1_000_000_000))
        probe.cancel()
    }
    let result = await probe.value
    timer.cancel()
    return result
}
