import Darwin
import Foundation

/// ProcSample is one reading of one process.
public struct ProcSample: Equatable {
    public var pid: Int32
    public var ppid: Int32
    /// name is the short command name (p_comm, at most 16 bytes).
    public var name: String
    /// path is the executable path, or "" when it cannot be read.
    public var path: String
    /// cpuNanos is the total user plus system CPU time so far.
    public var cpuNanos: UInt64
    /// residentBytes is the resident memory (what `ps -o rss` shows, in bytes).
    public var residentBytes: UInt64
    /// startTime is when the process started (seconds since 1970).
    public var startTime: TimeInterval

    public init(pid: Int32, ppid: Int32, name: String, path: String, cpuNanos: UInt64, residentBytes: UInt64, startTime: TimeInterval) {
        self.pid = pid
        self.ppid = ppid
        self.name = name
        self.path = path
        self.cpuNanos = cpuNanos
        self.residentBytes = residentBytes
        self.startTime = startTime
    }

    /// isClaudeCode reports whether the process looks like the Claude Code CLI.
    public var isClaudeCode: Bool {
        name.lowercased() == "claude" || (path as NSString).lastPathComponent.lowercased() == "claude"
            || path.contains("/claude-code/") || path.contains("/.local/share/claude/")
    }
}

/// ProcessReader reads processes. LibprocReader is the real one; tests use a fake.
public protocol ProcessReader {
    func sample(pid: Int32) -> ProcSample?
    func children(of pid: Int32) -> [Int32]
}

/// LibprocReader reads processes with libproc (proc_pidinfo,
/// proc_listchildpids, proc_pidpath). It needs no privileges for processes of
/// the same user and never spawns `ps`.
public struct LibprocReader: ProcessReader {
    /// The task times of proc_pidinfo are Mach absolute time units on Apple
    /// silicon (and nanoseconds on Intel, where the timebase is 1/1).
    private let timebase: (numer: UInt64, denom: UInt64)

    public init() {
        var info = mach_timebase_info_data_t()
        mach_timebase_info(&info)
        timebase = (UInt64(max(info.numer, 1)), UInt64(max(info.denom, 1)))
    }

    public func sample(pid: Int32) -> ProcSample? {
        var task = proc_taskinfo()
        let taskSize = Int32(MemoryLayout<proc_taskinfo>.size)
        guard proc_pidinfo(pid, PROC_PIDTASKINFO, 0, &task, taskSize) == taskSize else {
            return nil
        }
        var bsd = proc_bsdinfo()
        let bsdSize = Int32(MemoryLayout<proc_bsdinfo>.size)
        guard proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &bsd, bsdSize) == bsdSize else {
            return nil
        }
        let ticks = task.pti_total_user &+ task.pti_total_system
        let nanos = ticks.multipliedReportingOverflow(by: timebase.numer)
        let cpuNanos = nanos.overflow ? ticks / timebase.denom * timebase.numer : nanos.partialValue / timebase.denom
        let start = TimeInterval(bsd.pbi_start_tvsec) + TimeInterval(bsd.pbi_start_tvusec) / 1_000_000
        return ProcSample(
            pid: pid,
            ppid: Int32(bitPattern: bsd.pbi_ppid),
            name: withUnsafeBytes(of: bsd.pbi_comm) { cString($0) },
            path: executablePath(pid: pid),
            cpuNanos: cpuNanos,
            residentBytes: task.pti_resident_size,
            startTime: start
        )
    }

    public func children(of pid: Int32) -> [Int32] {
        // With no buffer the call returns an estimate of all PIDs; ask with a
        // buffer of that size to get the real count.
        let estimate = proc_listchildpids(pid, nil, 0)
        guard estimate > 0 else {
            return []
        }
        var buffer = [Int32](repeating: 0, count: Int(estimate) + 16)
        let count = buffer.withUnsafeMutableBytes { raw in
            proc_listchildpids(pid, raw.baseAddress, Int32(raw.count))
        }
        guard count > 0 else {
            return []
        }
        return Array(buffer.prefix(Int(count))).filter { $0 > 0 && $0 != pid }
    }

    public func executablePath(pid: Int32) -> String {
        var buffer = [UInt8](repeating: 0, count: 4 * Int(MAXPATHLEN))
        let length = proc_pidpath(pid, &buffer, UInt32(buffer.count))
        guard length > 0 else {
            return ""
        }
        return String(decoding: buffer.prefix(Int(length)), as: UTF8.self)
    }
}

private func cString(_ raw: UnsafeRawBufferPointer) -> String {
    let bytes = raw.prefix { $0 != 0 }
    return String(decoding: bytes, as: UTF8.self)
}

/// processAlive reports whether pid exists (kill 0 succeeds or is refused).
public func processAlive(_ pid: Int32) -> Bool {
    if pid <= 1 {
        return false
    }
    if kill(pid, 0) == 0 {
        return true
    }
    return errno == EPERM
}

/// collectTree returns the root process and all its descendants, root first.
/// Processes that exit between the listing and the read are skipped.
public func collectTree(root: Int32, reader: ProcessReader, limit: Int = 512) -> [ProcSample] {
    guard let rootSample = reader.sample(pid: root) else {
        return []
    }
    var out = [rootSample]
    var seen: Set<Int32> = [root]
    var queue = [root]
    while !queue.isEmpty && out.count < limit {
        let parent = queue.removeFirst()
        for child in reader.children(of: parent) where !seen.contains(child) {
            seen.insert(child)
            guard let sample = reader.sample(pid: child), sample.ppid == parent else {
                continue
            }
            out.append(sample)
            queue.append(child)
            if out.count >= limit {
                break
            }
        }
    }
    return out
}

/// Usage is what the menu shows under "Usage on this Mac".
public struct Usage: Codable, Equatable {
    /// cpuPercent is the CPU use of the whole tree since the previous sample,
    /// where 100 is one full core (as Activity Monitor counts). nil on the
    /// first sample.
    public var cpuPercent: Double?
    public var residentBytes: UInt64
    public var physicalMemory: UInt64
    /// processCount counts the daemon and its descendants.
    public var processCount: Int
    public var descendantCount: Int
    public var claudeCodeCount: Int
    public var daemonResidentBytes: UInt64

    public var memoryPercent: Double {
        physicalMemory == 0 ? 0 : Double(residentBytes) / Double(physicalMemory) * 100
    }

    public var residentMB: Double {
        Double(residentBytes) / 1_048_576
    }
}

/// UsageMeter turns process tree samples into Usage. CPU % is the sum of each
/// process's CPU time delta over the wall-clock delta between two samples; a
/// process is matched by PID and start time so a reused PID never counts.
public final class UsageMeter {
    private struct Key: Hashable {
        var pid: Int32
        var start: TimeInterval
    }

    private var previous: [Key: UInt64] = [:]
    private var previousAt: UInt64?
    public let physicalMemory: UInt64

    public init(physicalMemory: UInt64 = ProcessInfo.processInfo.physicalMemory) {
        self.physicalMemory = physicalMemory
    }

    /// reset forgets the previous sample, so the next CPU % starts fresh.
    public func reset() {
        previous = [:]
        previousAt = nil
    }

    /// update takes the tree samples read at monotonic time nowNanos.
    public func update(samples: [ProcSample], nowNanos: UInt64) -> Usage {
        var current: [Key: UInt64] = [:]
        var cpuDelta: UInt64 = 0
        for sample in samples {
            let key = Key(pid: sample.pid, start: sample.startTime)
            current[key] = sample.cpuNanos
            if let before = previous[key], sample.cpuNanos >= before {
                cpuDelta += sample.cpuNanos - before
            }
        }
        var cpu: Double?
        if let before = previousAt, nowNanos > before, !previous.isEmpty {
            cpu = Double(cpuDelta) / Double(nowNanos - before) * 100
        }
        previous = current
        previousAt = nowNanos
        let descendants = Array(samples.dropFirst())
        return Usage(
            cpuPercent: cpu,
            residentBytes: samples.reduce(0) { $0 + $1.residentBytes },
            physicalMemory: physicalMemory,
            processCount: samples.count,
            descendantCount: descendants.count,
            claudeCodeCount: descendants.filter(\.isClaudeCode).count,
            daemonResidentBytes: samples.first?.residentBytes ?? 0
        )
    }
}

/// monotonicNanos is a clock for CPU % deltas that never jumps.
public func monotonicNanos() -> UInt64 {
    clock_gettime_nsec_np(CLOCK_UPTIME_RAW)
}
