import Foundation

/// SoundPolicy decides when the app plays the notification sound itself
/// (spec #55). UNNotificationSound.default plays at the macOS alert volume,
/// which is often too low, so the app plays the sound through NSSound at full
/// volume instead and uses this policy to keep a burst of events from stacking
/// up sound on sound. It is pure so it is tested without AppKit or a clock.
public enum SoundPolicy {
    /// debounce is the shortest gap between two played sounds; an event within
    /// it still posts, but silently.
    public static let debounce: TimeInterval = 2

    /// shouldPlay is true when the app plays a sound for one event: the plan
    /// asks for one, the kind is not the silent workflow-started, and no sound
    /// played within the last debounce seconds. A gap of exactly debounce plays.
    public static func shouldPlay(kind: String, sound: Bool, lastPlayed: Date?, now: Date) -> Bool {
        guard sound, kind != EventKind.workflowStarted else {
            return false
        }
        if let lastPlayed, now.timeIntervalSince(lastPlayed) < debounce {
            return false
        }
        return true
    }
}
