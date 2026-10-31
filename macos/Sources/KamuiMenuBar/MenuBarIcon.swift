import AppKit
import KamuiMenuBarCore

/// Draws the vortex as an 18 pt template image, so macOS tints it for light
/// and dark menu bars. Idle and connecting states are dimmed; a session that
/// needs attention gets a badge.
enum MenuBarIcon {
    static let pointSize: CGFloat = 18

    static func image(for indicator: Indicator) -> NSImage {
        let image = NSImage(size: NSSize(width: pointSize, height: pointSize), flipped: true) { rect in
            let scale = rect.width / VortexGlyph.size
            NSColor.black.withAlphaComponent(opacity(for: indicator)).setFill()
            for arm in VortexGlyph.arms() {
                let path = NSBezierPath()
                path.move(to: scaled(arm[0], scale))
                for point in arm.dropFirst() {
                    path.line(to: scaled(point, scale))
                }
                path.close()
                path.fill()
            }
            if indicator == .attention {
                NSGraphicsContext.current?.compositingOperation = .clear
                NSBezierPath(ovalIn: circle(x: 84, y: 84, radius: 20, scale: scale)).fill()
                NSGraphicsContext.current?.compositingOperation = .sourceOver
                NSColor.black.setFill()
                NSBezierPath(ovalIn: circle(x: 84, y: 84, radius: 13, scale: scale)).fill()
            }
            return true
        }
        image.isTemplate = true
        image.accessibilityDescription = "Kamui"
        return image
    }

    static func opacity(for indicator: Indicator) -> CGFloat {
        switch indicator {
        case .idle: 0.45
        case .connecting: 0.7
        case .connected, .attention: 1
        }
    }

    private static func scaled(_ point: CGPoint, _ scale: CGFloat) -> CGPoint {
        CGPoint(x: point.x * scale, y: point.y * scale)
    }

    private static func circle(x: CGFloat, y: CGFloat, radius: CGFloat, scale: CGFloat) -> NSRect {
        NSRect(x: (x - radius) * scale, y: (y - radius) * scale, width: radius * 2 * scale, height: radius * 2 * scale)
    }
}
