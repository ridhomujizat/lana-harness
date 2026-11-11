import Foundation
@testable import LanaMenuCore
import XCTest

final class WindowShellTests: XCTestCase {
    func testLayout() {
        XCTAssertEqual(ShellLayout.buttonY(height: 14), 16)
        XCTAssertEqual(ShellLayout.buttonY(height: 16), 15)
        XCTAssertEqual(ShellLayout.lightsWidth(zoomMaxX: 74), 84)
        XCTAssertEqual(ShellLayout.lightsWidth(zoomMaxX: 73.5), 84)
    }

    func testMessages() {
        XCTAssertEqual(ShellMessage.parse("drag"), .drag)
        XCTAssertEqual(ShellMessage.parse("dblclick"), .doubleClick)
        XCTAssertEqual(ShellMessage.parse("theme:dark"), .theme(dark: true))
        XCTAssertEqual(ShellMessage.parse("theme:light"), .theme(dark: false))
        XCTAssertNil(ShellMessage.parse("start"))
        XCTAssertNil(ShellMessage.parse(42))
    }

    func testDoubleClickSetting() {
        XCTAssertEqual(DoubleClickAction.from(setting: nil), .zoom)
        XCTAssertEqual(DoubleClickAction.from(setting: "Maximize"), .zoom)
        XCTAssertEqual(DoubleClickAction.from(setting: "Minimize"), .minimize)
        XCTAssertEqual(DoubleClickAction.from(setting: "None"), .none)
    }

    func testBootstrapScript() {
        let prod = shellBootstrapScript(variant: .prod, lightsWidth: 84)
        XCTAssertTrue(prod.contains("d.dataset.shell = \"macos-app\""))
        XCTAssertTrue(prod.contains("\"--shell-lights-w\", \"84px\""))
        XCTAssertTrue(prod.contains("[data-drag-region]"))
        XCTAssertTrue(prod.contains("messageHandlers.lanaShell"))
        XCTAssertFalse(prod.contains("shellVariant"))
        XCTAssertTrue(shellBootstrapScript(variant: .dev, lightsWidth: 84).contains("d.dataset.shellVariant = \"dev\""))
        XCTAssertEqual(fullscreenScript(true), "document.documentElement.toggleAttribute(\"data-shell-fullscreen\", true);")
    }

    func testPlaceholderIsDraggable() {
        XCTAssertTrue(placeholderHTML(title: "t", detail: "d", canStart: false).contains("<main data-drag-region>"))
    }
}
