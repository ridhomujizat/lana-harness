import AppKit
import Foundation
import LanaMenuCore

/// The entry point handles the command-line flags before SwiftUI starts:
///
///   --dump-status       print the status and usage JSON the menu would show
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
              (no flags)       run the menu bar app
              --dump-status    print the status and usage JSON the menu would show
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
        let out = Output(status: status, usage: usage, usageText: text, binary: binary, links: links)
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
        guard let data = try? encoder.encode(out) else {
            return 1
        }
        print(String(decoding: data, as: UTF8.self))
        return 0
    }
}
