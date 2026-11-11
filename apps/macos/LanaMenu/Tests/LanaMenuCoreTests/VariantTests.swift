import CoreGraphics
import Foundation
@testable import LanaMenuCore
import XCTest

final class VariantTests: XCTestCase {
    func testVariantFromInfo() {
        XCTAssertEqual(AppVariant.from(info: ["LanaVariant": "dev"]), .dev)
        XCTAssertEqual(AppVariant.from(info: ["LanaVariant": "DEV"]), .dev)
        XCTAssertEqual(AppVariant.from(info: ["LanaVariant": "prod"]), .prod)
        XCTAssertEqual(AppVariant.from(info: ["LanaVariant": ""]), .prod)
        XCTAssertEqual(AppVariant.from(info: [:]), .prod)
        XCTAssertEqual(AppVariant.from(info: nil), .prod)
        XCTAssertEqual(AppVariant.dev.displayName, "lana dev")
        XCTAssertEqual(AppVariant.prod.displayName, "lana")
    }

    func testBundleHome() {
        XCTAssertEqual(bundleHome(info: ["LanaHome": "~/.lana-dev"], userHome: "/Users/u"), "/Users/u/.lana-dev")
        XCTAssertEqual(bundleHome(info: ["LanaHome": "/srv/lana"], userHome: "/Users/u"), "/srv/lana")
        XCTAssertNil(bundleHome(info: ["LanaHome": "  "], userHome: "/Users/u"))
        XCTAssertNil(bundleHome(info: ["LanaHome": ""], userHome: "/Users/u"))
        XCTAssertNil(bundleHome(info: [:], userHome: "/Users/u"))
    }

    /// The dev app's home wins over LANA_HOME from the environment, so it never
    /// talks to the production daemon; the settings override still wins.
    func testResolveWithBundleHome() {
        let env = ["LANA_HOME": "/Users/u/.lana"]
        XCTAssertEqual(LanaHome.resolve(override: nil, bundleHome: "~/.lana-dev", environment: env, userHome: "/Users/u").path, "/Users/u/.lana-dev")
        XCTAssertEqual(LanaHome.resolve(override: nil, bundleHome: "/Users/u/.lana-dev", environment: [:], userHome: "/Users/u").path, "/Users/u/.lana-dev")
        XCTAssertEqual(LanaHome.resolve(override: "~/scratch", bundleHome: "~/.lana-dev", environment: env, userHome: "/Users/u").path, "/Users/u/scratch")
        // The production app has no bundle home: unchanged behaviour.
        XCTAssertEqual(LanaHome.resolve(override: nil, bundleHome: nil, environment: env, userHome: "/Users/u").path, "/Users/u/.lana")
        XCTAssertEqual(LanaHome.resolve(override: nil, bundleHome: "", environment: [:], userHome: "/Users/u").path, "/Users/u/.lana")
    }

    /// The dev home reads its own config, secret, and lock files.
    func testDevHomeFiles() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("lana-dev-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        try "[web]\naddr = \"127.0.0.1:18399\"\n".write(to: dir.appendingPathComponent("config.toml"), atomically: true, encoding: .utf8)
        let home = LanaHome.resolve(override: nil, bundleHome: dir.path, environment: ["LANA_HOME": "/nonexistent/.lana"], userHome: "/Users/u")
        XCTAssertEqual(home.path, dir.path)
        XCTAssertEqual(readWebSettings(home).addr, "127.0.0.1:18399")
        XCTAssertEqual(home.secretURL.deletingLastPathComponent().path, dir.path)
        XCTAssertEqual(home.daemonLogURL.deletingLastPathComponent().path, dir.path)
    }

    func testDevIconIsYellow() throws {
        let image = try XCTUnwrap(LanaMark.renderAppIcon(pixels: 64, variant: .dev))
        let corner = try pixel(image, x: 32, y: 14) // inside the tile, above the mark
        XCTAssertGreaterThan(corner.r, 0.9)
        XCTAssertGreaterThan(corner.g, 0.7)
        XCTAssertLessThan(corner.b, 0.15)
        let prod = try pixel(try XCTUnwrap(LanaMark.renderAppIcon(pixels: 64)), x: 32, y: 14)
        XCTAssertLessThan(prod.r, 0.1)
    }

    private func pixel(_ image: CGImage, x: Int, y: Int) throws -> (r: Double, g: Double, b: Double) {
        var data = [UInt8](repeating: 0, count: 4)
        let ctx = try XCTUnwrap(CGContext(
            data: &data, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4,
            space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
        ))
        ctx.draw(image, in: CGRect(x: -x, y: -(image.height - 1 - y), width: image.width, height: image.height))
        return (Double(data[0]) / 255, Double(data[1]) / 255, Double(data[2]) / 255)
    }
}
