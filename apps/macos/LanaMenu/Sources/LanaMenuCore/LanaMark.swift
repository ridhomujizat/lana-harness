import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

/// The lana mark from web/ui/public/icons/logo-mark.svg: two rounded bars that
/// meet at the top like a "Λ". The paths are the SVG paths in SVG coordinates
/// (y grows down), so the app draws the same shape as the dashboard.
public enum LanaMark {
    /// bounds is the box around both bars in SVG coordinates.
    public static var bounds: CGRect {
        leftBar.boundingBoxOfPath.union(rightBar.boundingBoxOfPath)
    }

    /// leftBar is the "/" bar (drawn at 72 % opacity in the colour logo).
    public static var leftBar: CGPath {
        let p = CGMutablePath()
        let t = CGAffineTransform(translationX: 1.264, y: 4.75)
        p.move(to: CGPoint(x: 0.39, y: 18.028), transform: t)
        p.addLine(to: CGPoint(x: 9.681, y: 1.528), transform: t)
        p.addCurve(to: CGPoint(x: 12.295, y: 0), control1: CGPoint(x: 10.213, y: 0.584), control2: CGPoint(x: 11.212, y: 0), transform: t)
        p.addLine(to: CGPoint(x: 15.843, y: 0), transform: t)
        p.addCurve(to: CGPoint(x: 18.457, y: 4.472), control1: CGPoint(x: 18.138, y: 0), control2: CGPoint(x: 19.583, y: 2.472), transform: t)
        p.addLine(to: CGPoint(x: 9.166, y: 20.972), transform: t)
        p.addCurve(to: CGPoint(x: 6.552, y: 22.5), control1: CGPoint(x: 8.635, y: 21.916), control2: CGPoint(x: 7.636, y: 22.5), transform: t)
        p.addLine(to: CGPoint(x: 3.004, y: 22.5), transform: t)
        p.addCurve(to: CGPoint(x: 0.39, y: 18.028), control1: CGPoint(x: 0.709, y: 22.5), control2: CGPoint(x: -0.736, y: 20.028), transform: t)
        p.closeSubpath()
        return p
    }

    /// rightBar is the "\" bar, drawn on top of the left one.
    public static var rightBar: CGPath {
        let p = CGMutablePath()
        let t = CGAffineTransform(translationX: 11.889, y: 4.75)
        p.move(to: CGPoint(x: 3.004, y: 0), transform: t)
        p.addLine(to: CGPoint(x: 6.552, y: 0), transform: t)
        p.addCurve(to: CGPoint(x: 9.166, y: 1.528), control1: CGPoint(x: 7.636, y: 0), control2: CGPoint(x: 8.635, y: 0.584), transform: t)
        p.addLine(to: CGPoint(x: 18.457, y: 18.028), transform: t)
        p.addCurve(to: CGPoint(x: 15.843, y: 22.5), control1: CGPoint(x: 19.583, y: 20.028), control2: CGPoint(x: 18.138, y: 22.5), transform: t)
        p.addLine(to: CGPoint(x: 12.295, y: 22.5), transform: t)
        p.addCurve(to: CGPoint(x: 9.681, y: 20.972), control1: CGPoint(x: 11.212, y: 22.5), control2: CGPoint(x: 10.213, y: 21.916), transform: t)
        p.addLine(to: CGPoint(x: 0.39, y: 4.472), transform: t)
        p.addCurve(to: CGPoint(x: 3.004, y: 0), control1: CGPoint(x: -0.736, y: 2.472), control2: CGPoint(x: 0.709, y: 0), transform: t)
        p.closeSubpath()
        return p
    }

    /// Style picks how the bars are painted.
    public enum Style {
        /// template is one opaque colour with a thin gap cut around the front
        /// bar, so the two bars read apart in a monochrome menu bar icon.
        case template
        /// color paints the logo colours: the back bar at 72 % opacity.
        case color(CGColor)
    }

    /// draw paints the mark centred in rect of a y-up context (the normal
    /// CoreGraphics and AppKit orientation).
    public static func draw(in ctx: CGContext, rect: CGRect, style: Style) {
        let b = bounds
        let scale = min(rect.width / b.width, rect.height / b.height)
        let w = b.width * scale
        let h = b.height * scale
        ctx.saveGState()
        defer { ctx.restoreGState() }
        // SVG y grows down: flip, then map the bounds into the centred box.
        ctx.translateBy(x: rect.midX - w / 2, y: rect.midY + h / 2)
        ctx.scaleBy(x: scale, y: -scale)
        ctx.translateBy(x: -b.minX, y: -b.minY)

        switch style {
        case .template:
            ctx.setFillColor(CGColor(gray: 0, alpha: 1))
            ctx.addPath(leftBar)
            ctx.fillPath()
            // Cut a gap of about 1.1 pt (in output points) around the front bar.
            ctx.setBlendMode(.clear)
            ctx.setLineWidth(2.2 / scale)
            ctx.setLineJoin(.round)
            ctx.addPath(rightBar)
            ctx.strokePath()
            ctx.setBlendMode(.normal)
            ctx.addPath(rightBar)
            ctx.fillPath()
        case let .color(color):
            ctx.setFillColor(color.copy(alpha: 0.72) ?? color)
            ctx.addPath(leftBar)
            ctx.fillPath()
            ctx.setFillColor(color)
            ctx.addPath(rightBar)
            ctx.fillPath()
        }
    }

    /// renderAppIcon draws the app icon (the dark rounded tile of
    /// app-icon.svg with the white mark) at pixels × pixels.
    public static func renderAppIcon(pixels: Int) -> CGImage? {
        guard let ctx = CGContext(
            data: nil, width: pixels, height: pixels, bitsPerComponent: 8, bytesPerRow: 0,
            space: CGColorSpace(name: CGColorSpace.sRGB)!,
            bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
        ) else {
            return nil
        }
        let size = CGFloat(pixels)
        // macOS icon grid: an 824/1024 tile centred on the canvas.
        let tileSide = size * 824 / 1024
        let tile = CGRect(x: (size - tileSide) / 2, y: (size - tileSide) / 2 + size * 0.01, width: tileSide, height: tileSide)
        let radius = tileSide * 0.225
        ctx.setShouldAntialias(true)
        if pixels >= 64 {
            ctx.setShadow(offset: CGSize(width: 0, height: -size * 0.01), blur: size * 0.02, color: CGColor(gray: 0, alpha: 0.3))
        }
        ctx.addPath(CGPath(roundedRect: tile, cornerWidth: radius, cornerHeight: radius, transform: nil))
        ctx.setFillColor(CGColor(srgbRed: 0x0B / 255, green: 0x0F / 255, blue: 0x14 / 255, alpha: 1))
        ctx.fillPath()
        ctx.setShadow(offset: .zero, blur: 0, color: nil)
        let markRect = tile.insetBy(dx: tileSide * 0.2, dy: tileSide * 0.2)
        draw(in: ctx, rect: markRect, style: .color(CGColor(gray: 1, alpha: 1)))
        return ctx.makeImage()
    }

    /// writeIconset writes the PNGs `iconutil -c icns` expects into dir.
    public static func writeIconset(to dir: URL) throws {
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        for base in [16, 32, 128, 256, 512] {
            for scale in [1, 2] {
                let name = scale == 1 ? "icon_\(base)x\(base).png" : "icon_\(base)x\(base)@2x.png"
                guard let image = renderAppIcon(pixels: base * scale) else {
                    throw CocoaError(.fileWriteUnknown)
                }
                let url = dir.appendingPathComponent(name)
                guard let dest = CGImageDestinationCreateWithURL(url as CFURL, UTType.png.identifier as CFString, 1, nil) else {
                    throw CocoaError(.fileWriteUnknown)
                }
                CGImageDestinationAddImage(dest, image, nil)
                guard CGImageDestinationFinalize(dest) else {
                    throw CocoaError(.fileWriteUnknown)
                }
            }
        }
    }
}
