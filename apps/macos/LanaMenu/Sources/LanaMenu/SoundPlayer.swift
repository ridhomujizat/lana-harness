import AppKit
import Foundation
import LanaMenuCore

/// SoundPlayer plays the notification sound the app owns (spec #55):
/// UNNotificationSound.default follows the macOS alert volume, which is often
/// too low, so the banner posts silently and the app plays NSSound at full
/// volume instead. One shared player holds the debounce state, so the running
/// app and --simulate-event take the same path. It runs on the main thread
/// (NSSound needs it), which is where the Notifier and the flags run.
final class SoundPlayer {
    /// shared is the one player of the process.
    static let shared = SoundPlayer()

    /// playing holds the sound until it ends: NSSound stops when it deallocates.
    private var playing: NSSound?
    private var lastPlayed: Date?

    /// play plays the sound of one event unless SoundPolicy debounces it, and
    /// returns whether it played. now is passed in so the caller owns the clock.
    @discardableResult
    func play(kind: String, sound: Bool, now: Date = Date()) -> Bool {
        guard SoundPolicy.shouldPlay(kind: kind, sound: sound, lastPlayed: lastPlayed, now: now) else {
            return false
        }
        lastPlayed = now
        if let named = NSSound(named: "Glass") ?? NSSound(named: "Ping") {
            named.volume = 1
            playing = named
            named.play()
        } else {
            NSSound.beep()
        }
        return true
    }
}
