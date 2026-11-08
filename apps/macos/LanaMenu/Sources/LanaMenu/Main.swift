import AppKit
import Foundation
import LanaMenuCore
import UserNotifications

/// The entry point handles the command-line flags before SwiftUI starts:
///
///   --dump-status       print the status and usage JSON the menu would show,
///                       with the notification permission and event subscription
///   --simulate-event J  post the notification of one event JSON (spec #42)
///   --watch-events      print the daemon's notification events as JSON lines
///   --write-iconset DIR write the app icon PNGs for iconutil (make macos-app)
///   --snapshot PNG      render the open menu with live data to a PNG
///   --help
@main
enum Main {
    static func main() {
        let args = Array(CommandLine.arguments.dropFirst())
        if args.contains("--help") || args.contains("-h") {
            print("""
            usage: LanaMenu [--dump-status [--samples N]] [--snapshot PNG [--dark] [--seconds N]] [--write-iconset DIR]
                            [--simulate-event JSON] [--watch-events [--seconds N] [--answer allowed_once|rejected]]
              (no flags)       run the menu bar app
              --dump-status    print the status and usage JSON the menu would show, with the
                               notification permission and the event subscription state
              --simulate-event post the notification of one event JSON without the daemon
                               (needs Lana.app); prints the notification plan
              --watch-events   follow GET /v1/local/events and print each event with its
                               notification plan; --answer answers approvals like the
                               Allow once / Reject actions; stops after --seconds (default 60)
              --snapshot       render the open menu with live data to a PNG
              --write-iconset  write the app icon PNGs into DIR for iconutil
            """)
            return
        }
        if let i = args.firstIndex(of: "--write-iconset") {
            guard i + 1 < args.count else {
                fputs("error: --write-iconset needs a directory\n", stderr)
                exit(2)
            }
            do {
                try LanaMark.writeIconset(to: URL(fileURLWithPath: args[i + 1]))
            } catch {
                fputs("error: \(error)\n", stderr)
                exit(1)
            }
            return
        }
        if let i = args.firstIndex(of: "--snapshot") {
            guard i + 1 < args.count else {
                fputs("error: --snapshot needs a file path\n", stderr)
                exit(2)
            }
            var seconds = 2.6
            if let j = args.firstIndex(of: "--seconds"), j + 1 < args.count, let n = Double(args[j + 1]) {
                seconds = n
            }
            exit(Snapshot.run(path: args[i + 1], dark: args.contains("--dark"), seconds: seconds))
        }
        if let i = args.firstIndex(of: "--simulate-event") {
            guard i + 1 < args.count else {
                fputs("error: --simulate-event needs an event JSON\n", stderr)
                exit(2)
            }
            exit(NotifyDebug.simulate(json: args[i + 1]))
        }
        if args.contains("--watch-events") {
            var seconds = 60.0
            if let j = args.firstIndex(of: "--seconds"), j + 1 < args.count, let n = Double(args[j + 1]) {
                seconds = n
            }
            var answer: String?
            if let j = args.firstIndex(of: "--answer"), j + 1 < args.count {
                answer = args[j + 1]
            }
            exit(NotifyDebug.watch(seconds: seconds, answer: answer))
        }
        if args.contains("--dump-status") {
            var samples = 2
            if let i = args.firstIndex(of: "--samples"), i + 1 < args.count, let n = Int(args[i + 1]) {
                samples = max(1, n)
            }
            exit(DumpStatus.run(samples: samples))
        }
        LanaMenuApp.main()
    }
}

/// DumpStatus prints what the menu would show, as JSON, for scripts and tests.
enum DumpStatus {
    struct Output: Encodable {
        var status: DaemonStatus
        var usage: Usage?
        var usageText: UsageText?
        var binary: BinaryResolution
        var links: [String: String]
        var notifications: NotifyDebug.State
    }

    struct UsageText: Encodable {
        var cpu: String
        var ram: String
        var processes: String
    }

    static func run(samples: Int) -> Int32 {
        let settings = AppSettings()
        let home = settings.home
        let versions = VersionCache()
        let status = readStatus(home: home, versions: versions)
        var usage: Usage?
        if status.running, let pid = status.pid {
            let reader = LibprocReader()
            let meter = UsageMeter()
            for index in 0 ..< samples {
                if index > 0 {
                    Thread.sleep(forTimeInterval: 2)
                }
                usage = meter.update(samples: collectTree(root: pid, reader: reader), nowNanos: monotonicNanos())
            }
        }
        let shell = probeLoginShell()
        let binary = resolveBinary(setting: settings.binaryOverride, loginShellBinary: shell.lanaBinary, userHome: NSHomeDirectory())
        var links: [String: String] = [:]
        for item in DashboardPage.allCases {
            links[item.rawValue] = status.dashboardURL(hash: item.hash)?.absoluteString
        }
        let text = usage.map {
            UsageText(
                cpu: $0.cpuPercent.map(formatPercent) ?? "–",
                ram: formatBytes($0.residentBytes) + " · " + formatPercent($0.memoryPercent) + " of " + formatBytes($0.physicalMemory),
                processes: describeProcesses($0)
            )
        }
        let out = Output(status: status, usage: usage, usageText: text, binary: binary, links: links, notifications: NotifyDebug.state(home: home))
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
        guard let data = try? encoder.encode(out) else {
            return 1
        }
        print(String(decoding: data, as: UTF8.self))
        return 0
    }
}

/// NotifyDebug holds the notification flags of spec #42.
enum NotifyDebug {
    struct State: Encodable {
        /// permission is the macOS notification permission, or "unavailable"
        /// outside Lana.app.
        var permission: String
        var subscription: SubscriptionStatus
        var settings: NotificationSettings
    }

    static func wait<T>(_ work: @escaping @Sendable () async -> T) -> T {
        let box = ResultBox<T>()
        let done = DispatchSemaphore(value: 0)
        Task.detached {
            box.value = await work()
            done.signal()
        }
        done.wait()
        return box.value!
    }

    final class ResultBox<T>: @unchecked Sendable {
        var value: T?
    }

    static func permission() -> String {
        guard Notifier.available else {
            return "unavailable"
        }
        return wait {
            await withCheckedContinuation { continuation in
                UNUserNotificationCenter.current().getNotificationSettings { settings in
                    continuation.resume(returning: permissionText(settings.authorizationStatus))
                }
            }
        }
    }

    static func state(home: LanaHome) -> State {
        State(permission: permission(), subscription: wait { await probeSubscription(home: home) }, settings: .load())
    }

    static func encode<T: Encodable>(_ value: T) -> String {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return (try? encoder.encode(value)).map { String(decoding: $0, as: UTF8.self) } ?? "{}"
    }

    struct ActionLine: Encodable {
        var event: LanaEvent
        var action: String
        var plan: NotificationPlan?
        var remove: [String]?
        var reason: String?
    }

    static func line(_ event: LanaEvent, _ action: NotificationAction) -> ActionLine {
        switch action {
        case let .post(plan): return ActionLine(event: event, action: "post", plan: plan)
        case let .remove(ids): return ActionLine(event: event, action: "remove", remove: ids)
        case let .skip(reason): return ActionLine(event: event, action: "skip", reason: reason)
        }
    }

    /// simulate posts the notification of one event without the daemon.
    static func simulate(json: String) -> Int32 {
        let event: LanaEvent
        do {
            event = try LanaEvent.decode(json)
        } catch {
            fputs("error: event JSON: \(error)\n", stderr)
            return 2
        }
        let action = planNotification(event, settings: .load())
        print(encode(line(event, action)))
        guard case let .post(plan) = action else {
            return 0
        }
        guard Notifier.available else {
            fputs("error: posting needs the app bundle; run dist/Lana.app/Contents/MacOS/LanaMenu\n", stderr)
            return 3
        }
        // The flag never asks for permission, so it cannot hang on the
        // system prompt; the menu bar app asks on its first launch.
        let allowed = permission()
        guard allowed == "authorized" || allowed == "provisional" else {
            fputs("error: notifications are \(allowed) for lana; start Lana.app once and allow them\n", stderr)
            return 4
        }
        let error: String? = wait {
            await withCheckedContinuation { continuation in
                UNUserNotificationCenter.current().setNotificationCategories(Notifier.categories)
                Notifier.post(plan) { error in
                    continuation.resume(returning: error?.localizedDescription)
                }
            }
        }
        if let error {
            fputs("error: post: \(error)\n", stderr)
            return 1
        }
        print("{\"posted\":\"\(plan.identifier)\",\"permission\":\"\(permission())\"}")
        return 0
    }

    /// watch follows the event stream and prints each event as one JSON line.
    static func watch(seconds: Double, answer: String?) -> Int32 {
        let home = AppSettings().home
        let settings = NotificationSettings.load()
        let out = DispatchQueue(label: "watch-events")
        let subscription = EventSubscription(
            home: { home },
            onEvent: { event in
                let action = planNotification(event, settings: settings)
                out.sync {
                    print(encode(line(event, action)))
                    fflush(stdout)
                }
                guard let answer, case let .post(plan) = action, let id = plan.approvalId else {
                    return
                }
                Task.detached {
                    var result = ["answered": id, "outcome": answer]
                    do {
                        try await answerApproval(home: home, approvalId: id, outcome: answer)
                    } catch {
                        result["error"] = error.localizedDescription
                    }
                    out.sync {
                        print(encode(result))
                        fflush(stdout)
                    }
                }
            },
            onStatus: { status in
                out.sync {
                    print(encode(["status": status]))
                    fflush(stdout)
                }
            }
        )
        subscription.start()
        Thread.sleep(forTimeInterval: max(1, seconds))
        subscription.stop()
        Thread.sleep(forTimeInterval: 0.2)
        return 0
    }
}
