// swift-tools-version:5.9
// LanaMenu is the macOS menu bar app for lana (spec #41). It is a separate
// Swift package so the lana Go binary stays CGO-free (ADR 0013); the app talks
// to the daemon only through $LANA_HOME files and the loopback HTTP API.
import PackageDescription

let package = Package(
    name: "LanaMenu",
    platforms: [.macOS(.v13)],
    products: [
        .executable(name: "LanaMenu", targets: ["LanaMenu"]),
    ],
    targets: [
        .target(name: "LanaMenuCore"),
        .executableTarget(name: "LanaMenu", dependencies: ["LanaMenuCore"]),
        .testTarget(name: "LanaMenuCoreTests", dependencies: ["LanaMenuCore"]),
    ]
)
