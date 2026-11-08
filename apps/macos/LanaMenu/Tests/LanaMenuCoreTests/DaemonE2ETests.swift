import Foundation
@testable import LanaMenuCore
import XCTest

/// DaemonE2ETests start a real `lana daemon` in a temporary home with the
/// app's own start code, ask it for a sign-in link with next, and stop it
/// again. They run only when LANA_MENU_E2E_BINARY names a lana binary:
///
///   LANA_MENU_E2E_BINARY=$PWD/lana swift test --filter DaemonE2ETests
final class DaemonE2ETests: XCTestCase {
    func testStartSignInLinkStop() async throws {
        guard let binary = ProcessInfo.processInfo.environment["LANA_MENU_E2E_BINARY"], !binary.isEmpty else {
            throw XCTSkip("set LANA_MENU_E2E_BINARY to run the daemon end-to-end test")
        }
        let port = ProcessInfo.processInfo.environment["LANA_MENU_E2E_PORT"] ?? "18397"
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("lana-menu-e2e-" + UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let home = LanaHome(path: dir.path)
        try """
        [web]
        enabled = true
        addr = "127.0.0.1:\(port)"
        open_browser = false
        """.write(to: home.configURL, atomically: true, encoding: .utf8)

        let pid = try startDaemon(binary: binary, home: home, path: ProcessInfo.processInfo.environment["PATH"], baseEnvironment: ProcessInfo.processInfo.environment)
        var status = readStatus(home: home)
        for _ in 0 ..< 100 where !(status.running && status.localControl) {
            try await Task.sleep(nanoseconds: 100_000_000)
            status = readStatus(home: home, versions: VersionCache())
        }
        let log = (try? String(contentsOf: home.daemonLogURL, encoding: .utf8)) ?? ""
        XCTAssertTrue(status.running, "daemon did not start; log:\n\(log)")
        XCTAssertEqual(status.pid, pid)
        XCTAssertEqual(status.addr, "127.0.0.1:\(port)")
        XCTAssertEqual(status.addrSource, "daemon.secret")
        XCTAssertNotNil(status.version)

        let link = try await requestSignInLink(home: home, next: "#/calendar")
        XCTAssertTrue(link.absoluteString.hasPrefix("http://127.0.0.1:\(port)/#/welcome?code="), link.absoluteString)
        XCTAssertTrue(link.absoluteString.hasSuffix("&next=%23%2Fcalendar"), link.absoluteString)
        let plain = try await requestSignInLink(home: home, next: "https://evil.example/")
        XCTAssertFalse(plain.absoluteString.contains("next="), plain.absoluteString)

        try stopDaemon(pid: pid)
        for _ in 0 ..< 100 where readStatus(home: home).running {
            try await Task.sleep(nanoseconds: 100_000_000)
        }
        XCTAssertFalse(readStatus(home: home).running)
        XCTAssertFalse(processAlive(pid))
    }
}
