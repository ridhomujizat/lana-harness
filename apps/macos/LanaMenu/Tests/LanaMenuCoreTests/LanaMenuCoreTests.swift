import Foundation
@testable import LanaMenuCore
import XCTest

final class WebSettingsTests: XCTestCase {
    func testDefaultWithoutWebTable() {
        let settings = parseWebSettings(toml: "[providers.openai]\naddr = \"x\"\n")
        XCTAssertEqual(settings, WebSettings(addr: "127.0.0.1:18300", enabled: true, addrFromConfig: false))
    }

    func testReadsWebAddrAndEnabled() {
        let toml = """
        # lana config
        addr = "top-level is not web"

        [web]
        enabled = false   # off for now
        addr = "127.0.0.1:18399" # the demo port

        [providers.claude]
        addr = "127.0.0.1:1"
        """
        let settings = parseWebSettings(toml: toml)
        XCTAssertEqual(settings.addr, "127.0.0.1:18399")
        XCTAssertFalse(settings.enabled)
        XCTAssertTrue(settings.addrFromConfig)
    }

    func testLiteralStringHashInsideAndSpacedTable() {
        let settings = parseWebSettings(toml: "[ web ]\naddr='127.0.0.1:1#2'\n")
        XCTAssertEqual(settings.addr, "127.0.0.1:1#2")
    }

    func testDottedTopLevelKeyAndQuotedKey() {
        XCTAssertEqual(parseWebSettings(toml: "web.addr = \"127.0.0.1:9\"").addr, "127.0.0.1:9")
        XCTAssertEqual(parseWebSettings(toml: "[web]\n\"addr\" = \"127.0.0.1:8\"").addr, "127.0.0.1:8")
    }

    func testArrayTableNamedWebIsIgnored() {
        XCTAssertEqual(parseWebSettings(toml: "[[web]]\naddr = \"127.0.0.1:7\"").addr, defaultWebAddr)
    }

    func testEmptyOrUnquotedAddrKeepsDefault() {
        XCTAssertEqual(parseWebSettings(toml: "[web]\naddr = \"\"").addr, defaultWebAddr)
        XCTAssertEqual(parseWebSettings(toml: "[web]\naddr = 18300").addr, defaultWebAddr)
    }

    func testReadsFilesFromHome() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let home = LanaHome(path: dir.path)
        try "[web]\naddr = \"127.0.0.1:18301\"\n".write(to: home.configURL, atomically: true, encoding: .utf8)
        try "{\"addr\":\"127.0.0.1:18302\",\"secret\":\"s3\"}\n".write(to: home.secretURL, atomically: true, encoding: .utf8)
        try "4242\n".write(to: home.lockURL, atomically: true, encoding: .utf8)
        XCTAssertEqual(readWebSettings(home).addr, "127.0.0.1:18301")
        XCTAssertEqual(readDaemonSecret(home), DaemonSecret(addr: "127.0.0.1:18302", secret: "s3"))
        XCTAssertEqual(readDaemonLock(home)?.pid, 4242)
        // A lock with a PID that is not running is not a running daemon.
        try "999999\n".write(to: home.lockURL, atomically: true, encoding: .utf8)
        let status = readStatus(home: home)
        XCTAssertFalse(status.running)
        XCTAssertEqual(status.addr, "127.0.0.1:18301")
        XCTAssertEqual(status.addrSource, "config.toml")
    }
}

final class HomeAndBinaryTests: XCTestCase {
    func testHomeResolution() {
        XCTAssertEqual(LanaHome.resolve(override: nil, environment: [:], userHome: "/Users/u").path, "/Users/u/.lana")
        XCTAssertEqual(LanaHome.resolve(override: "", environment: ["LANA_HOME": "/tmp/h"], userHome: "/Users/u").path, "/tmp/h")
        XCTAssertEqual(LanaHome.resolve(override: " ", environment: ["LANA_HOME": "  "], userHome: "/Users/u").path, "/Users/u/.lana")
        XCTAssertEqual(LanaHome.resolve(override: "~/lana-test", environment: ["LANA_HOME": "/tmp/h"], userHome: "/Users/u").path, "/Users/u/lana-test")
    }

    func testBinaryOrder() {
        let all: Set<String> = ["/Users/u/.local/bin/lana", "/Users/u/go/bin/lana", "/opt/homebrew/bin/lana", "/usr/local/bin/lana", "/nix/bin/lana", "/custom/lana"]
        let exec: (String) -> Bool = { all.contains($0) }
        XCTAssertEqual(resolveBinary(setting: "/custom/lana", loginShellBinary: "/nix/bin/lana", userHome: "/Users/u", isExecutable: exec),
                       BinaryResolution(path: "/custom/lana", source: "setting"))
        XCTAssertEqual(resolveBinary(setting: nil, loginShellBinary: "/nix/bin/lana", userHome: "/Users/u", isExecutable: exec).path, "/nix/bin/lana")
        XCTAssertEqual(resolveBinary(setting: "", loginShellBinary: nil, userHome: "/Users/u", isExecutable: exec).path, "/Users/u/.local/bin/lana")
        XCTAssertEqual(resolveBinary(setting: nil, loginShellBinary: "lana", userHome: "/Users/u", isExecutable: { $0 == "/opt/homebrew/bin/lana" }).path, "/opt/homebrew/bin/lana")
        // A setting that is not executable is reported, not silently replaced.
        XCTAssertEqual(resolveBinary(setting: "/missing", loginShellBinary: "/nix/bin/lana", userHome: "/Users/u", isExecutable: exec),
                       BinaryResolution(path: nil, source: "setting"))
        XCTAssertNil(resolveBinary(setting: nil, loginShellBinary: nil, userHome: "/Users/u", isExecutable: { _ in false }).path)
    }

    func testLoginShellOutput() {
        let env = parseLoginShellOutput("Welcome!\n\n__LANA_PATH=/a/bin:/usr/bin\n__LANA_BIN=/a/bin/lana\n")
        XCTAssertEqual(env, LoginShellEnvironment(path: "/a/bin:/usr/bin", lanaBinary: "/a/bin/lana"))
        XCTAssertNil(parseLoginShellOutput("__LANA_PATH=/x\n__LANA_BIN=lana: aliased to foo\n").lanaBinary)
    }

    func testVersionParse() {
        XCTAssertEqual(parseVersion("lana 0.1.0-beta.1 (61af960, 2026-11-01T17:36:08Z)\n"), "0.1.0-beta.1")
        XCTAssertEqual(parseVersion("lana dev\n"), "dev")
        XCTAssertNil(parseVersion("something else"))
    }
}

/// FakeReader is a process table for the tree and usage tests.
struct FakeReader: ProcessReader {
    var table: [Int32: ProcSample]

    func sample(pid: Int32) -> ProcSample? { table[pid] }

    func children(of pid: Int32) -> [Int32] {
        table.values.filter { $0.ppid == pid && $0.pid != pid }.map(\.pid).sorted()
    }
}

final class UsageTests: XCTestCase {
    private func proc(_ pid: Int32, _ ppid: Int32, _ name: String, cpu: UInt64, rss: UInt64, start: TimeInterval = 100) -> ProcSample {
        ProcSample(pid: pid, ppid: ppid, name: name, path: "/bin/" + name, cpuNanos: cpu, residentBytes: rss, startTime: start)
    }

    func testTreeIncludesDescendantsOnly() {
        let reader = FakeReader(table: [
            10: proc(10, 1, "lana", cpu: 0, rss: 100),
            11: proc(11, 10, "claude", cpu: 0, rss: 50),
            12: proc(12, 11, "node", cpu: 0, rss: 20),
            20: proc(20, 1, "other", cpu: 0, rss: 999),
        ])
        let tree = collectTree(root: 10, reader: reader)
        XCTAssertEqual(tree.map(\.pid), [10, 11, 12])
        XCTAssertTrue(collectTree(root: 99, reader: reader).isEmpty)
    }

    func testCPUPercentFromDeltas() {
        let meter = UsageMeter(physicalMemory: 1000)
        let first = meter.update(samples: [proc(10, 1, "lana", cpu: 1_000_000_000, rss: 100), proc(11, 10, "claude", cpu: 0, rss: 50)], nowNanos: 10_000_000_000)
        XCTAssertNil(first.cpuPercent)
        XCTAssertEqual(first.residentBytes, 150)
        XCTAssertEqual(first.memoryPercent, 15, accuracy: 0.0001)
        XCTAssertEqual(first.descendantCount, 1)
        XCTAssertEqual(first.claudeCodeCount, 1)

        // 2 s later: lana used 0.1 s and claude 0.5 s of CPU, so 30 %.
        let second = meter.update(samples: [proc(10, 1, "lana", cpu: 1_100_000_000, rss: 100), proc(11, 10, "claude", cpu: 500_000_000, rss: 60)], nowNanos: 12_000_000_000)
        XCTAssertEqual(second.cpuPercent ?? -1, 30, accuracy: 0.0001)
        XCTAssertEqual(second.residentBytes, 160)

        // A new child counts from its next sample; a reused PID with a new start
        // time never inherits the old CPU time.
        let third = meter.update(samples: [
            proc(10, 1, "lana", cpu: 1_300_000_000, rss: 100),
            proc(11, 10, "zsh", cpu: 9_000_000_000, rss: 10, start: 200),
            proc(13, 10, "git", cpu: 5_000_000_000, rss: 10),
        ], nowNanos: 14_000_000_000)
        XCTAssertEqual(third.cpuPercent ?? -1, 10, accuracy: 0.0001)
        XCTAssertEqual(third.claudeCodeCount, 0)
        XCTAssertEqual(describeProcesses(third), "lana daemon + 2 other processes")

        meter.reset()
        XCTAssertNil(meter.update(samples: [proc(10, 1, "lana", cpu: 2_000_000_000, rss: 1)], nowNanos: 16_000_000_000).cpuPercent)
    }

    func testDescribeProcesses() {
        var usage = Usage(cpuPercent: nil, residentBytes: 0, physicalMemory: 1, processCount: 1, descendantCount: 0, claudeCodeCount: 0, daemonResidentBytes: 0)
        XCTAssertEqual(describeProcesses(usage), "lana daemon")
        usage.descendantCount = 1
        usage.claudeCodeCount = 1
        XCTAssertEqual(describeProcesses(usage), "lana daemon + 1 Claude Code process")
        usage.descendantCount = 4
        usage.claudeCodeCount = 2
        XCTAssertEqual(describeProcesses(usage), "lana daemon + 2 Claude Code processes, 2 others")
    }

    func testClaudeDetection() {
        XCTAssertTrue(ProcSample(pid: 1, ppid: 0, name: "claude", path: "", cpuNanos: 0, residentBytes: 0, startTime: 0).isClaudeCode)
        XCTAssertTrue(ProcSample(pid: 1, ppid: 0, name: "2.1.3", path: "/Users/u/.local/share/claude/versions/2.1.3", cpuNanos: 0, residentBytes: 0, startTime: 0).isClaudeCode)
        XCTAssertFalse(ProcSample(pid: 1, ppid: 0, name: "node", path: "/usr/bin/node", cpuNanos: 0, residentBytes: 0, startTime: 0).isClaudeCode)
    }

    func testLibprocReadsThisProcess() {
        let reader = LibprocReader()
        let me = getpid()
        guard let sample = reader.sample(pid: me) else {
            return XCTFail("no sample for self")
        }
        XCTAssertEqual(sample.pid, me)
        XCTAssertGreaterThan(sample.residentBytes, 1_000_000)
        XCTAssertGreaterThan(sample.cpuNanos, 0)
        XCTAssertFalse(sample.path.isEmpty)
        XCTAssertLessThan(sample.startTime, Date().timeIntervalSince1970 + 1)
    }

    func testFormatting() {
        XCTAssertEqual(formatUptime(45), "45s")
        XCTAssertEqual(formatUptime(12 * 60 + 5), "12m")
        XCTAssertEqual(formatUptime(2 * 3600 + 5 * 60), "2h 05m")
        XCTAssertEqual(formatUptime(3 * 86400 + 4 * 3600 + 59), "3d 4h")
        XCTAssertEqual(formatBytes(146 * 1_048_576), "146 MB")
        XCTAssertEqual(formatBytes(1_331_439_861), "1.24 GB")
        XCTAssertEqual(formatBytes(25_769_803_776), "24 GB")
        XCTAssertEqual(formatBytes(17_716_740_096), "16.5 GB")
        XCTAssertEqual(formatPercent(3.24), "3.2%")
        XCTAssertEqual(formatPercent(123.4), "123%")
    }
}

final class LocalControlTests: XCTestCase {
    func testSignInRequestEncodesNext() throws {
        let secret = DaemonSecret(addr: "127.0.0.1:18399", secret: "abc")
        let request = try XCTUnwrap(signInLinkRequest(secret: secret, next: "#/calendar"))
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.url?.absoluteString, "http://127.0.0.1:18399/v1/local/sign-in-link?next=%23%2Fcalendar")
        XCTAssertEqual(request.value(forHTTPHeaderField: "X-Lana-Local-Secret"), "abc")
        let plain = try XCTUnwrap(signInLinkRequest(secret: secret, next: nil))
        XCTAssertEqual(plain.url?.absoluteString, "http://127.0.0.1:18399/v1/local/sign-in-link")
    }

    func testDashboardURL() {
        let status = DaemonStatus(home: "/h", running: true, addr: "127.0.0.1:18300", addrSource: "default", webEnabled: true, localControl: false)
        XCTAssertEqual(status.dashboardURL(hash: "#/workflows")?.absoluteString, "http://127.0.0.1:18300/#/workflows")
    }
}

final class MarkTests: XCTestCase {
    func testIconRenders() throws {
        let image = try XCTUnwrap(LanaMark.renderAppIcon(pixels: 64))
        XCTAssertEqual(image.width, 64)
        let bounds = LanaMark.bounds
        XCTAssertEqual(bounds.minY, 4.75, accuracy: 0.01)
        XCTAssertEqual(bounds.height, 22.5, accuracy: 0.01)
    }
}
