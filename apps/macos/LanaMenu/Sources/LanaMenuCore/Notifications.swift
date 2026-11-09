import Foundation

// Spec #42: the daemon publishes notification events on GET /v1/local/events;
// the app maps each one to a macOS notification. Everything here is plain data
// so the mapping is tested without UNUserNotificationCenter, which needs the
// app bundle.

/// LanaEvent is one notification event of the daemon (API-NTF-001).
public struct LanaEvent: Codable, Equatable, Sendable {
    public var id: String
    public var kind: String
    public var at: Int64
    public var sessionId: String?
    public var agent: String?
    public var title: String
    public var body: String
    public var url: String
    public var approvalId: String?
    public var questionId: String?
    public var workflowId: String?
    public var workflowRunId: String?
    public var outcome: String?
    public var silent: Bool?
    public var timeSensitive: Bool?
    public var thread: String

    enum CodingKeys: String, CodingKey {
        case id, kind, at, agent, title, body, url, outcome, silent, thread
        case sessionId = "session_id"
        case approvalId = "approval_id"
        case questionId = "question_id"
        case workflowId = "workflow_id"
        case workflowRunId = "workflow_run_id"
        case timeSensitive = "time_sensitive"
    }

    public init(id: String, kind: String, at: Int64 = 0, sessionId: String? = nil, agent: String? = nil, title: String, body: String, url: String, approvalId: String? = nil, questionId: String? = nil, workflowId: String? = nil, workflowRunId: String? = nil, outcome: String? = nil, silent: Bool? = nil, timeSensitive: Bool? = nil, thread: String = "") {
        self.id = id
        self.kind = kind
        self.at = at
        self.sessionId = sessionId
        self.agent = agent
        self.title = title
        self.body = body
        self.url = url
        self.approvalId = approvalId
        self.questionId = questionId
        self.workflowId = workflowId
        self.workflowRunId = workflowRunId
        self.outcome = outcome
        self.silent = silent
        self.timeSensitive = timeSensitive
        self.thread = thread
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        kind = try c.decode(String.self, forKey: .kind)
        at = try c.decodeIfPresent(Int64.self, forKey: .at) ?? 0
        sessionId = try c.decodeIfPresent(String.self, forKey: .sessionId)
        agent = try c.decodeIfPresent(String.self, forKey: .agent)
        title = try c.decodeIfPresent(String.self, forKey: .title) ?? ""
        body = try c.decodeIfPresent(String.self, forKey: .body) ?? ""
        url = try c.decodeIfPresent(String.self, forKey: .url) ?? ""
        approvalId = try c.decodeIfPresent(String.self, forKey: .approvalId)
        questionId = try c.decodeIfPresent(String.self, forKey: .questionId)
        workflowId = try c.decodeIfPresent(String.self, forKey: .workflowId)
        workflowRunId = try c.decodeIfPresent(String.self, forKey: .workflowRunId)
        outcome = try c.decodeIfPresent(String.self, forKey: .outcome)
        silent = try c.decodeIfPresent(Bool.self, forKey: .silent)
        timeSensitive = try c.decodeIfPresent(Bool.self, forKey: .timeSensitive)
        thread = try c.decodeIfPresent(String.self, forKey: .thread) ?? ""
    }

    public static func decode(_ json: String) throws -> LanaEvent {
        try JSONDecoder().decode(LanaEvent.self, from: Data(json.utf8))
    }
}

/// EventKind names the event kinds the app knows.
public enum EventKind {
    public static let reply = "reply"
    public static let approvalAsked = "approval_asked"
    public static let approvalDecided = "approval_decided"
    public static let questionAsked = "question_asked"
    public static let questionAnswered = "question_answered"
    public static let workflowStarted = "workflow_started"
    public static let workflowFinished = "workflow_finished"
    public static let workflowFailed = "workflow_failed"
    public static let workflowWaiting = "workflow_waiting"
}

/// NotificationSettings are the notification toggles of the Settings window.
/// Every kind is on by default; workflow-started banners are silent.
public struct NotificationSettings: Codable, Equatable, Sendable {
    public var messages = true
    public var approvals = true
    public var questions = true
    public var workflowStarted = true
    public var workflowFinished = true
    /// showPreview off replaces the reply and question text with a placeholder.
    public var showPreview = true

    public init() {}

    public init(messages: Bool = true, approvals: Bool = true, questions: Bool = true, workflowStarted: Bool = true, workflowFinished: Bool = true, showPreview: Bool = true) {
        self.messages = messages
        self.approvals = approvals
        self.questions = questions
        self.workflowStarted = workflowStarted
        self.workflowFinished = workflowFinished
        self.showPreview = showPreview
    }

    static let keys = (
        messages: "notify.messages",
        approvals: "notify.approvals",
        questions: "notify.questions",
        workflowStarted: "notify.workflowStarted",
        workflowFinished: "notify.workflowFinished",
        showPreview: "notify.showPreview"
    )

    /// load reads the toggles; a missing key keeps its default.
    public static func load(_ defaults: UserDefaults = .standard) -> NotificationSettings {
        func flag(_ key: String) -> Bool {
            defaults.object(forKey: key) == nil ? true : defaults.bool(forKey: key)
        }
        return NotificationSettings(
            messages: flag(keys.messages),
            approvals: flag(keys.approvals),
            questions: flag(keys.questions),
            workflowStarted: flag(keys.workflowStarted),
            workflowFinished: flag(keys.workflowFinished),
            showPreview: flag(keys.showPreview)
        )
    }

    public func save(_ defaults: UserDefaults = .standard) {
        defaults.set(messages, forKey: Self.keys.messages)
        defaults.set(approvals, forKey: Self.keys.approvals)
        defaults.set(questions, forKey: Self.keys.questions)
        defaults.set(workflowStarted, forKey: Self.keys.workflowStarted)
        defaults.set(workflowFinished, forKey: Self.keys.workflowFinished)
        defaults.set(showPreview, forKey: Self.keys.showPreview)
    }
}

/// NotificationCategory names the registered categories and their actions.
public enum NotificationCategory {
    /// approval has the Allow once and Reject actions.
    public static let approval = "LANA_APPROVAL"
    public static let plain = "LANA_PLAIN"
    public static let allowAction = "LANA_ALLOW_ONCE"
    public static let rejectAction = "LANA_REJECT"
}

/// NotificationPlan is one banner to post.
public struct NotificationPlan: Codable, Equatable, Sendable {
    public var identifier: String
    public var title: String
    public var body: String
    public var threadIdentifier: String
    public var category: String
    /// sound false posts the banner without a sound.
    public var sound: Bool
    public var timeSensitive: Bool
    /// url is the dashboard hash a click opens.
    public var url: String
    /// approvalId is what the Allow once and Reject actions answer.
    public var approvalId: String?
}

/// NotificationAction is what the app does with one event.
public enum NotificationAction: Equatable, Sendable {
    case post(NotificationPlan)
    /// remove takes delivered banners away, by identifier.
    case remove([String])
    /// skip drops the event: its toggle is off or the kind is unknown.
    case skip(String)
}

/// The placeholder body of a reply or question when previews are off.
public let hiddenPreviewBody = "New message"

/// approvalIdentifier is the banner identifier of an approval, so a decision
/// made elsewhere can remove it.
public func approvalIdentifier(_ approvalId: String) -> String { "approval-" + approvalId }

/// questionIdentifier is the banner identifier of a question.
public func questionIdentifier(_ questionId: String) -> String { "question-" + questionId }

/// planNotification maps one daemon event to a notification under the
/// settings (spec #42 table).
public func planNotification(_ event: LanaEvent, settings: NotificationSettings) -> NotificationAction {
    let approvable = (event.approvalId ?? "").isEmpty == false
    var plan = NotificationPlan(
        identifier: "event-" + event.id,
        title: event.title,
        body: event.body,
        threadIdentifier: event.thread.isEmpty ? (event.sessionId ?? event.id) : event.thread,
        category: NotificationCategory.plain,
        sound: !(event.silent ?? false),
        timeSensitive: event.timeSensitive ?? false,
        url: event.url,
        approvalId: nil
    )
    switch event.kind {
    case EventKind.reply:
        guard settings.messages else { return .skip("messages off") }
        if !settings.showPreview { plan.body = hiddenPreviewBody }
    case EventKind.questionAsked:
        guard settings.questions else { return .skip("questions off") }
        if let q = event.questionId, !q.isEmpty { plan.identifier = questionIdentifier(q) }
        if !settings.showPreview { plan.body = hiddenPreviewBody }
    case EventKind.approvalAsked, EventKind.workflowWaiting:
        guard settings.approvals else { return .skip("approvals off") }
        if approvable, let id = event.approvalId {
            plan.identifier = approvalIdentifier(id)
            plan.category = NotificationCategory.approval
            plan.approvalId = id
        }
    case EventKind.workflowStarted:
        guard settings.workflowStarted else { return .skip("workflow started off") }
        plan.sound = false
    case EventKind.workflowFinished, EventKind.workflowFailed:
        guard settings.workflowFinished else { return .skip("workflow finished off") }
        if event.kind == EventKind.workflowFailed { plan.sound = true }
    case EventKind.approvalDecided:
        guard let id = event.approvalId, !id.isEmpty else { return .skip("no approval id") }
        return .remove([approvalIdentifier(id)])
    case EventKind.questionAnswered:
        guard let id = event.questionId, !id.isEmpty else { return .skip("no question id") }
        return .remove([questionIdentifier(id)])
    default:
        return .skip("unknown kind " + event.kind)
    }
    return .post(plan)
}

/// dashboardURL joins the dashboard address and an event url ("#/…").
public func dashboardURL(addr: String, hash: String) -> URL? {
    var tail = hash
    if tail.hasPrefix("/") { tail.removeFirst() }
    return URL(string: "http://" + addr + "/" + tail)
}

// MARK: - Local approval answer

/// localApprovalRequest builds POST /v1/local/approvals/{id} (API-NTF-002).
/// outcome is "allowed_once" or "rejected".
public func localApprovalRequest(secret: DaemonSecret, approvalId: String, outcome: String) -> URLRequest? {
    var allowed = CharacterSet.alphanumerics
    allowed.insert(charactersIn: "-_.~:")
    guard let escaped = approvalId.addingPercentEncoding(withAllowedCharacters: allowed),
          let url = URL(string: "http://" + secret.addr + "/v1/local/approvals/" + escaped)
    else {
        return nil
    }
    var request = URLRequest(url: url, timeoutInterval: 10)
    request.httpMethod = "POST"
    request.setValue(secret.secret, forHTTPHeaderField: localSecretHeader)
    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
    request.httpBody = try? JSONSerialization.data(withJSONObject: ["outcome": outcome])
    return request
}

/// answerApproval answers one approval through the running daemon.
public func answerApproval(home: LanaHome, approvalId: String, outcome: String) async throws {
    guard let secret = readDaemonSecret(home),
          let request = localApprovalRequest(secret: secret, approvalId: approvalId, outcome: outcome)
    else {
        throw LocalControlError.notRunning
    }
    let (data, response) = try await URLSession.shared.data(for: request)
    let code = (response as? HTTPURLResponse)?.statusCode ?? 0
    guard code == 200 else {
        throw LocalControlError.status(code, String(decoding: data.prefix(512), as: UTF8.self))
    }
}

/// The header that carries daemon.secret.
public let localSecretHeader = "X-Lana-Local-Secret"
