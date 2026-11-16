import Foundation
@testable import LanaMenuCore
import XCTest

/// SoundPolicyTests cover the debounce the app uses for its own, full-volume
/// notification sound (spec #55).
final class SoundPolicyTests: XCTestCase {
    func testFirstSoundPlays() {
        XCTAssertTrue(SoundPolicy.shouldPlay(kind: EventKind.reply, sound: true, lastPlayed: nil, now: Date()))
    }

    func testDebounce() {
        let now = Date()
        // Within two seconds of the last sound nothing plays.
        XCTAssertFalse(SoundPolicy.shouldPlay(kind: EventKind.reply, sound: true, lastPlayed: now, now: now))
        XCTAssertFalse(SoundPolicy.shouldPlay(kind: EventKind.reply, sound: true, lastPlayed: now, now: now.addingTimeInterval(SoundPolicy.debounce - 0.01)))
        // A gap of exactly two seconds plays.
        XCTAssertTrue(SoundPolicy.shouldPlay(kind: EventKind.reply, sound: true, lastPlayed: now, now: now.addingTimeInterval(SoundPolicy.debounce)))
        XCTAssertTrue(SoundPolicy.shouldPlay(kind: EventKind.reply, sound: true, lastPlayed: now, now: now.addingTimeInterval(SoundPolicy.debounce + 1)))
    }

    func testSilentPlanNeverPlays() {
        XCTAssertFalse(SoundPolicy.shouldPlay(kind: EventKind.reply, sound: false, lastPlayed: nil, now: Date()))
    }

    func testWorkflowStartedNeverPlays() {
        XCTAssertFalse(SoundPolicy.shouldPlay(kind: EventKind.workflowStarted, sound: true, lastPlayed: nil, now: Date()))
    }

    func testAgentKindsPlay() {
        XCTAssertTrue(SoundPolicy.shouldPlay(kind: EventKind.agentFinished, sound: true, lastPlayed: nil, now: Date()))
        XCTAssertTrue(SoundPolicy.shouldPlay(kind: EventKind.agentNeedsYou, sound: true, lastPlayed: nil, now: Date()))
    }
}
