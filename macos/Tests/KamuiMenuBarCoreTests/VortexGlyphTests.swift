import CoreGraphics
import XCTest
@testable import KamuiMenuBarCore

final class VortexGlyphTests: XCTestCase {
    func testThreeClosedArmsStayInsideTheBox() {
        let arms = VortexGlyph.arms(steps: 70)
        XCTAssertEqual(arms.count, 3)
        for arm in arms {
            XCTAssertEqual(arm.count, 142, "outer and inner edges with 71 samples each")
            for point in arm {
                XCTAssertTrue((0...VortexGlyph.size).contains(point.x) && (0...VortexGlyph.size).contains(point.y), "\(point)")
            }
        }
    }

    func testArmsAreRotationallySymmetric() {
        let arms = VortexGlyph.arms(steps: 70)
        let angle = 120.0 * .pi / 180
        for (first, second) in zip(arms[0], arms[1]) {
            let x = first.x - 50, y = first.y - 50
            let rotated = CGPoint(x: 50 + x * cos(angle) - y * sin(angle), y: 50 + x * sin(angle) + y * cos(angle))
            XCTAssertEqual(rotated.x, second.x, accuracy: 0.001)
            XCTAssertEqual(rotated.y, second.y, accuracy: 0.001)
        }
    }

    func testArmTapersToAPointAtTheRim() {
        let arm = VortexGlyph.arms(steps: 70)[0]
        let outerStart = arm[0], innerStart = arm[arm.count - 1]
        XCTAssertEqual(outerStart.x, innerStart.x, accuracy: 0.001)
        XCTAssertEqual(outerStart.y, innerStart.y, accuracy: 0.001)
    }
}
