import Foundation
@testable import LanaMenuCore
import XCTest

final class NotificationPlanTests: XCTestCase {
    func event(_ kind: String, approval: String? = nil, question: String? = nil, silent: Bool? = nil, timeSensitive: Bool? = nil) -> LanaEvent {
        LanaEvent(id: "b-1", kind: kind, sessionId: "s1", agent: "coder", title: "T", body: "B", url: "#/sessions/s1", approvalId: approval, questionId: question, silent: silent, timeSensitive: timeSensitive, thread: "s1")
    }

    func plan(_ action: NotificationAction, file: StaticString = #filePath, line: UInt = #line) -> NotificationPlan? {
        guard case let .post(plan) = action else {
            XCTFail("expected a post, got \(action)", file: file, line: line)
            return nil
        }
        return plan
    }

    func testDecodesDaemonJSON() throws {
        let json = ##"{"id":"lx1-7","kind":"approval_asked","at":1700000000000,"session_id":"c","agent":"worker","title":"Approval needed · worker","body":"bash: ls","url":"#/sessions/r","approval_id":"AbC_-9","time_sensitive":true,"thread":"r"}"##
        let event = try LanaEvent.decode(json)
        XCTAssertEqual(event.approvalId, "AbC_-9")
        XCTAssertEqual(event.timeSensitive, true)
        XCTAssertNil(event.silent)
        XCTAssertEqual(event.thread, "r")
    }

    func testReply() {
        guard let p = plan(planNotification(event("reply"), settings: .init())) else { return }
        XCTAssertEqual(p.identifier, "event-b-1")
        XCTAssertEqual(p.title, "T")
        XCTAssertEqual(p.body, "B")
        XCTAssertEqual(p.threadIdentifier, "s1")
        XCTAssertEqual(p.category, NotificationCategory.plain)
        XCTAssertTrue(p.sound)
        XCTAssertFalse(p.timeSensitive)
        XCTAssertEqual(p.url, "#/sessions/s1")
        XCTAssertNil(p.approvalId)
    }

    func testReplyWithoutPreview() {
        var settings = NotificationSettings()
        settings.showPreview = false
        XCTAssertEqual(plan(planNotification(event("reply"), settings: settings))?.body, hiddenPreviewBody)
        XCTAssertEqual(plan(planNotification(event("question_asked", question: "q1"), settings: settings))?.body, hiddenPreviewBody)
        // Approvals keep their text: the owner decides from it.
        XCTAssertEqual(plan(planNotification(event("approval_asked", approval: "a1"), settings: settings))?.body, "B")
    }

    func testApprovalHasActions() {
        guard let p = plan(planNotification(event("approval_asked", approval: "a1", timeSensitive: true), settings: .init())) else { return }
        XCTAssertEqual(p.identifier, "approval-a1")
        XCTAssertEqual(p.category, NotificationCategory.approval)
        XCTAssertEqual(p.approvalId, "a1")
        XCTAssertTrue(p.timeSensitive)

        guard let w = plan(planNotification(event("workflow_waiting", approval: "wf:w:d"), settings: .init())) else { return }
        XCTAssertEqual(w.category, NotificationCategory.approval)
        XCTAssertEqual(w.identifier, "approval-wf:w:d")

        // A guardian decision has no approval id and no actions.
        guard let g = plan(planNotification(event("workflow_waiting"), settings: .init())) else { return }
        XCTAssertEqual(g.category, NotificationCategory.plain)
        XCTAssertNil(g.approvalId)
    }

    func testDecisionsRemoveBanners() {
        XCTAssertEqual(planNotification(event("approval_decided", approval: "a1", silent: true), settings: .init()), .remove(["approval-a1"]))
        XCTAssertEqual(planNotification(event("question_answered", question: "q1", silent: true), settings: .init()), .remove(["question-q1"]))
        XCTAssertEqual(plan(planNotification(event("question_asked", question: "q1"), settings: .init()))?.identifier, "question-q1")
    }

    func testWorkflowSounds() {
        XCTAssertEqual(plan(planNotification(event("workflow_started", silent: true), settings: .init()))?.sound, false)
        XCTAssertEqual(plan(planNotification(event("workflow_started"), settings: .init()))?.sound, false)
        XCTAssertEqual(plan(planNotification(event("workflow_failed"), settings: .init()))?.sound, true)
        XCTAssertEqual(plan(planNotification(event("workflow_finished"), settings: .init()))?.sound, true)
    }

    func testSettingsFilters() {
        func skipped(_ kind: String, _ settings: NotificationSettings, approval: String? = nil) -> Bool {
            if case .skip = planNotification(event(kind, approval: approval), settings: settings) { return true }
            return false
        }
        XCTAssertTrue(skipped("reply", NotificationSettings(messages: false)))
        XCTAssertTrue(skipped("approval_asked", NotificationSettings(approvals: false), approval: "a"))
        XCTAssertTrue(skipped("workflow_waiting", NotificationSettings(approvals: false), approval: "a"))
        XCTAssertTrue(skipped("question_asked", NotificationSettings(questions: false)))
        XCTAssertTrue(skipped("workflow_started", NotificationSettings(workflowStarted: false)))
        XCTAssertTrue(skipped("workflow_finished", NotificationSettings(workflowFinished: false)))
        XCTAssertTrue(skipped("workflow_failed", NotificationSettings(workflowFinished: false)))
        XCTAssertFalse(skipped("reply", NotificationSettings(approvals: false, questions: false, workflowStarted: false, workflowFinished: false)))
        XCTAssertTrue(skipped("something_new", NotificationSettings()))
        // Removing a banner is never filtered.
        XCTAssertEqual(planNotification(event("approval_decided", approval: "a"), settings: NotificationSettings(approvals: false)), .remove(["approval-a"]))
    }

    func testSettingsDefaultsAndPersistence() throws {
        let suite = "lana-menu-tests-" + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        XCTAssertEqual(NotificationSettings.load(defaults), NotificationSettings())
        XCTAssertTrue(NotificationSettings.load(defaults).workflowStarted)
        var settings = NotificationSettings()
        settings.messages = false
        settings.showPreview = false
        settings.save(defaults)
        XCTAssertEqual(NotificationSettings.load(defaults), settings)
    }

    func testDashboardURL() {
        XCTAssertEqual(dashboardURL(addr: "127.0.0.1:18300", hash: "#/sessions/abc")?.absoluteString, "http://127.0.0.1:18300/#/sessions/abc")
        XCTAssertEqual(dashboardURL(addr: "127.0.0.1:1", hash: "#/workflows/w/runs/3")?.absoluteString, "http://127.0.0.1:1/#/workflows/w/runs/3")
    }

    func testLocalApprovalRequest() throws {
        let secret = DaemonSecret(addr: "127.0.0.1:18431", secret: "s3cret")
        let request = try XCTUnwrap(localApprovalRequest(secret: secret, approvalId: "wf:w1:d1", outcome: "rejected"))
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.url?.absoluteString, "http://127.0.0.1:18431/v1/local/approvals/wf:w1:d1")
        XCTAssertEqual(request.value(forHTTPHeaderField: "X-Lana-Local-Secret"), "s3cret")
        let body = try JSONSerialization.jsonObject(with: XCTUnwrap(request.httpBody)) as? [String: String]
        XCTAssertEqual(body, ["outcome": "rejected"])

        let events = try XCTUnwrap(localEventsRequest(secret: secret, after: "lx1-9"))
        XCTAssertEqual(events.url?.absoluteString, "http://127.0.0.1:18431/v1/local/events?after=lx1-9")
        XCTAssertNil(try XCTUnwrap(localEventsRequest(secret: secret, after: nil)).url?.query)
    }
}

final class EventStreamTests: XCTestCase {
    func testSSEParser() {
        var parser = SSEParser()
        let lines = [
            "event: ready", "data: {\"last_id\":\"\"}", "",
            ": ping", "",
            "id: b-1", "event: notify", "data: {\"a\":1}", "",
            "data: one", "data: two\r", "",
        ]
        var messages: [SSEMessage] = []
        for line in lines {
            if let message = parser.feed(line) {
                messages.append(message)
            }
        }
        XCTAssertEqual(messages, [
            SSEMessage(event: "ready", id: nil, data: "{\"last_id\":\"\"}"),
            SSEMessage(event: "notify", id: "b-1", data: "{\"a\":1}"),
            SSEMessage(event: "message", id: nil, data: "one\ntwo"),
        ])
    }

    func testRetryDelay() {
        XCTAssertEqual((0 ... 7).map { retryDelay(attempt: $0) }, [1, 2, 4, 8, 16, 30, 30, 30])
    }

    func testSeenEvents() {
        var seen = SeenEvents(limit: 2)
        XCTAssertTrue(seen.insert("a"))
        XCTAssertFalse(seen.insert("a"))
        XCTAssertTrue(seen.insert("b"))
        XCTAssertTrue(seen.insert("c"))
        XCTAssertTrue(seen.insert("a"), "the oldest id is forgotten past the limit")
    }
}
