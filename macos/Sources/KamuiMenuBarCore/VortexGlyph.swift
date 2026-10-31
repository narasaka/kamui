import CoreGraphics
import Foundation

/// Geometry of Kamui's three-armed vortex mark in a 100 × 100 box with the
/// origin at the top left. The menu bar icon draws it; the app icon in
/// Resources/AppIcon.icns uses the same spiral.
public enum VortexGlyph {
    public static let size: CGFloat = 100
    static let center = CGPoint(x: 50, y: 50)

    /// The three arms, each a closed polygon: outer edge from rim to core,
    /// then the inner edge back out.
    public static func arms(steps: Int = 70) -> [[CGPoint]] {
        [0.0, 120.0, 240.0].map { arm(rotation: $0, steps: steps) }
    }

    /// One logarithmic spiral band from the rim to the core. It tapers to a
    /// point at the rim and thins toward the core.
    static func arm(
        rotation: Double, turns: Double = 1.15, outerRadius: Double = 44, innerRadius: Double = 6,
        outerWidth: Double = 13, innerWidth: Double = 2.5, steps: Int
    ) -> [CGPoint] {
        var outer: [CGPoint] = []
        var inner: [CGPoint] = []
        for step in 0...steps {
            let f = Double(step) / Double(steps)
            let radius = outerRadius * pow(innerRadius / outerRadius, f)
            let width = (outerWidth * (1 - f) + innerWidth * f) * smoothstep(f / 0.14)
            let angle = (rotation + f * turns * 360 - 90) * .pi / 180
            outer.append(point(radius: radius + width / 2, angle: angle))
            inner.append(point(radius: max(radius - width / 2, 0.2), angle: angle))
        }
        return outer + inner.reversed()
    }

    private static func point(radius: Double, angle: Double) -> CGPoint {
        CGPoint(x: center.x + radius * cos(angle), y: center.y + radius * sin(angle))
    }

    private static func smoothstep(_ x: Double) -> Double {
        let t = min(max(x, 0), 1)
        return t * t * (3 - 2 * t)
    }
}
