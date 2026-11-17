// Renders the library cover for the scriptlet: logo above the title, on a white 660x1000 canvas.
// swift tools/cover.swift <logo.png> <out.jpg>   (logo.png: kindle/logo.svg rendered, e.g. qlmanage -t -s 600)
import AppKit

let args = CommandLine.arguments
let logo = NSImage(contentsOfFile: args[1])!
let out = args[2]
let W = 660, H = 1000

let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: W, pixelsHigh: H, bitsPerSample: 8,
                           samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                           colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
NSColor.white.setFill()
NSRect(x: 0, y: 0, width: W, height: H).fill()

let side: CGFloat = 540
logo.draw(in: NSRect(x: (CGFloat(W) - side) / 2, y: CGFloat(H) - 70 - side, width: side, height: side))

let style = NSMutableParagraphStyle()
style.alignment = .center
let attrs: [NSAttributedString.Key: Any] = [
    .font: NSFont.boldSystemFont(ofSize: 78),
    .foregroundColor: NSColor.black,
    .paragraphStyle: style,
]
("Audiobookshelf\nSync" as NSString).draw(in: NSRect(x: 0, y: 90, width: CGFloat(W), height: 220), withAttributes: attrs)
NSGraphicsContext.restoreGraphicsState()

let jpg = rep.representation(using: .jpeg, properties: [.compressionFactor: 0.85])!
try! jpg.write(to: URL(fileURLWithPath: out))
