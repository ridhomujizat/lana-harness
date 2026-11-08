import AppKit
import Foundation
import LanaMenuCore
import UserNotifications

/// Notifier posts the macOS notifications of spec #42. It follows the daemon's
/// GET /v1/local/events, maps each event with planNotification, answers the
/// Allow once and Reject actions through POST /v1/local/approvals/{id}, and
/// opens the dashboard on a click. The daemon never posts notifications.
@MainActor
final class Notifier: NSObject, ObservableObject {
    /// permission is "authorized", "provisional", "denied", "not_determined",
    /// or "unavailable" outside Lana.app.
    @Published private(set) var permission = "unknown"
    @Published private(set) var subscription = SubscriptionStatus(state: "idle")
    @Published var settings = NotificationSettings.load() {
        didSet { settings.save() }
    }

    @Published var lastError: String?

    private let home: () -> LanaHome
    private var stream: EventSubscription?
    private var started = false

    init(home: @escaping () -> LanaHome) {
        self.home = home
    }

    /// available is true when the process runs from an app bundle:
    /// UNUserNotificationCenter needs one.
    nonisolated static var available: Bool {
        Bundle.main.bundleIdentifier != nil && Bundle.main.bundleURL.pathExtension == "app"
    }

    /// start asks for permission on the first launch, registers the approval
    /// actions, and subscribes to the daemon events.
    func start() {
        guard !started else {
            return
        }
        started = true
        if Self.available {
            let center = UNUserNotificationCenter.current()
            center.delegate = self
            center.setNotificationCategories(Self.categories)
            center.requestAuthorization(options: [.alert, .sound, .badge]) { _, _ in
                Task { @MainActor in self.refreshPermission() }
            }
        } else {
            permission = "unavailable"
        }
        subscribe()
    }

    /// restart follows a new LANA_HOME.
    func restart() {
        subscribe()
    }

    private func subscribe() {
        stream?.stop()
        let homeNow = home()
        let stream = EventSubscription(
            home: { homeNow },
            onEvent: { event in
                Task { @MainActor in self.deliver(event) }
            },
            onStatus: { status in
                Task { @MainActor in self.subscription = status }
            }
        )
        self.stream = stream
        stream.start()
    }

    nonisolated static var categories: Set<UNNotificationCategory> {
        let allow = UNNotificationAction(identifier: NotificationCategory.allowAction, title: "Allow once", options: [])
        let reject = UNNotificationAction(identifier: NotificationCategory.rejectAction, title: "Reject", options: [.destructive])
        return [
            UNNotificationCategory(identifier: NotificationCategory.approval, actions: [allow, reject], intentIdentifiers: [], options: []),
            UNNotificationCategory(identifier: NotificationCategory.plain, actions: [], intentIdentifiers: [], options: []),
        ]
    }

    func refreshPermission() {
        guard Self.available else {
            permission = "unavailable"
            return
        }
        UNUserNotificationCenter.current().getNotificationSettings { settings in
            let text = permissionText(settings.authorizationStatus)
            Task { @MainActor in self.permission = text }
        }
    }

    /// openSystemSettings opens System Settings → Notifications at this app.
    func openSystemSettings() {
        let id = Bundle.main.bundleIdentifier ?? "dev.lana.menubar"
        if let url = URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension?id=" + id) {
            NSWorkspace.shared.open(url)
        }
    }

    /// deliver posts, removes, or skips the banner of one event.
    func deliver(_ event: LanaEvent) {
        switch planNotification(event, settings: settings) {
        case let .post(plan):
            Self.post(plan) { error in
                if let error {
                    Task { @MainActor in self.lastError = error.localizedDescription }
                }
            }
        case let .remove(ids):
            guard Self.available else { return }
            let center = UNUserNotificationCenter.current()
            center.removeDeliveredNotifications(withIdentifiers: ids)
            center.removePendingNotificationRequests(withIdentifiers: ids)
        case .skip:
            break
        }
    }

    /// post adds one banner. It is static so --simulate-event can use it.
    nonisolated static func post(_ plan: NotificationPlan, completion: @escaping @Sendable (Error?) -> Void) {
        guard available else {
            completion(NotifierError.unavailable)
            return
        }
        let content = UNMutableNotificationContent()
        content.title = plan.title
        content.body = plan.body
        content.threadIdentifier = plan.threadIdentifier
        content.categoryIdentifier = plan.category
        content.sound = plan.sound ? .default : nil
        content.interruptionLevel = plan.timeSensitive ? .timeSensitive : .active
        var info: [String: String] = ["url": plan.url]
        if let id = plan.approvalId {
            info["approval_id"] = id
        }
        content.userInfo = info
        let request = UNNotificationRequest(identifier: plan.identifier, content: content, trigger: nil)
        UNUserNotificationCenter.current().add(request, withCompletionHandler: completion)
    }

    /// open shows a dashboard hash in the browser.
    func open(hash: String) {
        let status = readStatus(home: home())
        guard let url = dashboardURL(addr: status.addr, hash: hash) else {
            return
        }
        NSWorkspace.shared.open(url)
    }

    func answer(approvalId: String, outcome: String, identifier: String) {
        let home = home()
        Task {
            do {
                try await answerApproval(home: home, approvalId: approvalId, outcome: outcome)
                UNUserNotificationCenter.current().removeDeliveredNotifications(withIdentifiers: [identifier])
            } catch {
                lastError = "Approval: " + error.localizedDescription
            }
        }
    }
}

enum NotifierError: LocalizedError {
    case unavailable

    var errorDescription: String? {
        "Notifications need the app bundle: run dist/Lana.app (make macos-app)."
    }
}

func permissionText(_ status: UNAuthorizationStatus) -> String {
    switch status {
    case .authorized: return "authorized"
    case .provisional: return "provisional"
    case .denied: return "denied"
    case .notDetermined: return "not_determined"
    case .ephemeral: return "ephemeral"
    @unknown default: return "unknown"
    }
}

extension Notifier: UNUserNotificationCenterDelegate {
    /// The menu bar app is always active, so banners show in front too.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification, withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .list, .sound])
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse, withCompletionHandler completionHandler: @escaping () -> Void) {
        let info = response.notification.request.content.userInfo
        let url = info["url"] as? String ?? ""
        let approvalId = info["approval_id"] as? String
        let identifier = response.notification.request.identifier
        let action = response.actionIdentifier
        Task { @MainActor in
            switch action {
            case NotificationCategory.allowAction:
                if let approvalId { self.answer(approvalId: approvalId, outcome: "allowed_once", identifier: identifier) }
            case NotificationCategory.rejectAction:
                if let approvalId { self.answer(approvalId: approvalId, outcome: "rejected", identifier: identifier) }
            case UNNotificationDefaultActionIdentifier:
                if !url.isEmpty { self.open(hash: url) }
            default:
                break
            }
            completionHandler()
        }
    }
}
