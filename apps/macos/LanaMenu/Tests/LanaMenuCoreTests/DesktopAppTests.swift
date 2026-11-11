import Foundation
@testable import LanaMenuCore
import XCTest

final class DesktopAppTests: XCTestCase {
    func testAppLinkRequest() throws {
        let secret = DaemonSecret(addr: "127.0.0.1:18300", secret: "s3cret")
        let request = try XCTUnwrap(appLinkRequest(secret: secret, next: "#/sessions/abc?x=1"))
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.value(forHTTPHeaderField: "X-Lana-Local-Secret"), "s3cret")
        XCTAssertEqual(request.url?.absoluteString, "http://127.0.0.1:18300/v1/local/app-link?next=%23%2Fsessions%2Fabc%3Fx%3D1")
        let plain = try XCTUnwrap(appLinkRequest(secret: secret, next: nil))
        XCTAssertEqual(plain.url?.absoluteString, "http://127.0.0.1:18300/v1/local/app-link")
    }

    func testClassifyNavigation() throws {
        let addr = "127.0.0.1:18300"
        func target(_ s: String) throws -> NavigationTarget {
            classifyNavigation(try XCTUnwrap(URL(string: s)), dashboardAddr: addr)
        }
        XCTAssertEqual(try target("http://127.0.0.1:18300/#/chats"), .inApp)
        XCTAssertEqual(try target("http://127.0.0.1:18300/v1/auth/app?code=x"), .inApp)
        XCTAssertEqual(try target("http://127.0.0.1:18399/"), .external)
        XCTAssertEqual(try target("http://localhost:18300/"), .external)
        XCTAssertEqual(try target("https://127.0.0.1:18300/"), .external)
        XCTAssertEqual(try target("https://github.com/ridhomujizat"), .external)
        XCTAssertEqual(try target("mailto:me@example.com"), .external)
        XCTAssertEqual(try target("about:blank"), .allow)
        XCTAssertEqual(try target("blob:http://127.0.0.1:18300/1234"), .allow)
        XCTAssertEqual(classifyNavigation(try XCTUnwrap(URL(string: "http://example.com/")), dashboardAddr: "example.com:80"), .inApp)
    }

    func testDashboardHash() throws {
        XCTAssertEqual(dashboardHashOf(try XCTUnwrap(URL(string: "http://127.0.0.1:18300/#/sessions/abc"))), "#/sessions/abc")
        XCTAssertNil(dashboardHashOf(try XCTUnwrap(URL(string: "http://127.0.0.1:18300/"))))
        XCTAssertNil(dashboardHashOf(try XCTUnwrap(URL(string: "http://127.0.0.1:18300/#top"))))
    }

    func testBundledBinary() {
        XCTAssertEqual(bundledBinary(bundleURL: URL(fileURLWithPath: "/Applications/Lana.app")), "/Applications/Lana.app/Contents/MacOS/lana")
        XCTAssertNil(bundledBinary(bundleURL: URL(fileURLWithPath: "/usr/local/bin")))
    }

    func testResolveBinaryPrefersBundleAfterSetting() {
        let bundled = "/Applications/Lana.app/Contents/MacOS/lana"
        let exec: (String) -> Bool = { _ in true }
        XCTAssertEqual(resolveBinary(setting: "/custom/lana", loginShellBinary: "/nix/bin/lana", userHome: "/Users/u", bundled: bundled, isExecutable: exec),
                       BinaryResolution(path: "/custom/lana", source: "setting"))
        XCTAssertEqual(resolveBinary(setting: nil, loginShellBinary: "/nix/bin/lana", userHome: "/Users/u", bundled: bundled, isExecutable: exec),
                       BinaryResolution(path: bundled, source: "app bundle"))
        let missing: (String) -> Bool = { $0 != bundled }
        XCTAssertEqual(resolveBinary(setting: nil, loginShellBinary: "/nix/bin/lana", userHome: "/Users/u", bundled: bundled, isExecutable: missing).source, "login shell PATH")
    }

    func testPlaceholderEscapes() {
        let html = placeholderHTML(title: "<b>lana</b>", detail: "a & b", canStart: true)
        XCTAssertTrue(html.contains("&lt;b&gt;lana&lt;/b&gt;"))
        XCTAssertTrue(html.contains("a &amp; b"))
        XCTAssertTrue(html.contains("messageHandlers.lana"))
        XCTAssertFalse(placeholderHTML(title: "t", detail: "d", canStart: false).contains("<button"))
    }
}
