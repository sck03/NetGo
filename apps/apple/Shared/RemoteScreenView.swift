import SwiftUI
#if os(macOS)
import AppKit
#else
import UIKit
#endif

#if os(macOS)
struct ScreenView: NSViewRepresentable {
    let viewer: RemoteViewer
    func makeNSView(context: Context) -> Screen { let value = Screen(); value.viewer = viewer; value.wantsLayer = true; value.layer?.addSublayer(viewer.media.layer); return value }
    func updateNSView(_ view: Screen,context: Context) { view.viewer = viewer }
    final class Screen: NSView {
        var viewer: RemoteViewer?
        override var acceptsFirstResponder: Bool { true }
        override func layout() { super.layout(); viewer?.media.layer.frame = bounds }
        override func mouseDown(with event: NSEvent) { window?.makeFirstResponder(self); send(event,down:true) }
        override func mouseUp(with event: NSEvent) { send(event,down:false) }
        override func mouseDragged(with event: NSEvent) { send(event,down:nil) }
        override func rightMouseDown(with event: NSEvent) { send(event,down:true,button:2) }
        override func rightMouseUp(with event: NSEvent) { send(event,down:false,button:2) }
        private func send(_ event: NSEvent,down: Bool?,button: Int = 1) {
            let p = convert(event.locationInWindow,from:nil)
            viewer?.pointer(x:p.x,y:bounds.height-p.y,viewWidth:bounds.width,viewHeight:bounds.height,down:down,button:button)
        }
        private var heldModifiers = Set<UInt16>()
        override func keyDown(with event: NSEvent) {
            if let code = RemoteKeys.mac[event.keyCode] { viewer?.input(RemoteKeys.event(code,down:true)) }
            else if let text = event.characters, !text.isEmpty { viewer?.input(["type":"text","text":text]) }
        }
        override func keyUp(with event: NSEvent) {
            if let code = RemoteKeys.mac[event.keyCode] { viewer?.input(RemoteKeys.event(code,down:false)) }
        }
        override func flagsChanged(with event: NSEvent) {
            guard let code = RemoteKeys.mac[event.keyCode], let flag = RemoteKeys.modifiers[event.keyCode] else { return }
            if event.keyCode == 57 {
                viewer?.input(RemoteKeys.event(code,down:true))
                viewer?.input(RemoteKeys.event(code,down:false))
                return
            }
            let down = event.modifierFlags.contains(flag) && !heldModifiers.contains(event.keyCode)
            if down { heldModifiers.insert(event.keyCode) } else { heldModifiers.remove(event.keyCode) }
            viewer?.input(RemoteKeys.event(code,down:down))
        }
        override func scrollWheel(with event: NSEvent) {
            for (value,horizontal) in [(event.scrollingDeltaY,false),(event.scrollingDeltaX,true)] where value != 0 {
                let delta = max(-1200,min(1200,Int(value * (event.hasPreciseScrollingDeltas ? 6 : 120))))
                if delta != 0 { viewer?.input(["type":"wheel","delta":delta,"horizontal":horizontal]) }
            }
        }
        override func mouseMoved(with event: NSEvent) { send(event,down:nil) }
        override func rightMouseDragged(with event: NSEvent) { send(event,down:nil) }
        override func otherMouseDown(with event: NSEvent) { send(event,down:true,button:3) }
        override func otherMouseUp(with event: NSEvent) { send(event,down:false,button:3) }
        override func otherMouseDragged(with event: NSEvent) { send(event,down:nil) }
        override func updateTrackingAreas() {
            super.updateTrackingAreas()
            for area in trackingAreas { removeTrackingArea(area) }
            addTrackingArea(NSTrackingArea(rect:bounds,options:[.mouseMoved,.activeInKeyWindow,.inVisibleRect],owner:self,userInfo:nil))
        }
        override func resignFirstResponder() -> Bool { heldModifiers.removeAll(); viewer?.input(["type":"release"]); return super.resignFirstResponder() }
    }
}
#else
struct ScreenView: UIViewRepresentable {
    let viewer: RemoteViewer
    func makeUIView(context: Context) -> Screen { let value = Screen(); value.viewer = viewer; value.layer.addSublayer(viewer.media.layer); return value }
    func updateUIView(_ view: Screen,context: Context) { view.viewer = viewer }
    final class Screen: UIView {
        var viewer: RemoteViewer?
        override func layoutSubviews() { super.layoutSubviews(); viewer?.media.layer.frame = bounds }
        override var canBecomeFirstResponder: Bool { true }
        override func touchesBegan(_ touches: Set<UITouch>,with event: UIEvent?) { becomeFirstResponder(); send(touches,down:true) }
        override func pressesBegan(_ presses: Set<UIPress>,with event: UIPressesEvent?) { keys(presses,down:true) }
        override func pressesEnded(_ presses: Set<UIPress>,with event: UIPressesEvent?) { keys(presses,down:false) }
        override func pressesCancelled(_ presses: Set<UIPress>,with event: UIPressesEvent?) { viewer?.input(["type":"release"]) }
        override func resignFirstResponder() -> Bool { viewer?.input(["type":"release"]); return super.resignFirstResponder() }
        private func keys(_ presses: Set<UIPress>,down: Bool) {
            for press in presses {
                guard let key=press.key else { continue }
                if let code=RemoteKeys.hid(Int(key.keyCode.rawValue)) { viewer?.input(RemoteKeys.event(code,down:down)) }
                else if down && !key.characters.isEmpty { viewer?.input(["type":"text","text":key.characters]) }
            }
        }
        override func touchesMoved(_ touches: Set<UITouch>,with event: UIEvent?) { send(touches,down:nil) }
        override func touchesEnded(_ touches: Set<UITouch>,with event: UIEvent?) { send(touches,down:false) }
        override func touchesCancelled(_ touches: Set<UITouch>,with event: UIEvent?) { viewer?.input(["type":"release"]) }
        private func send(_ touches: Set<UITouch>,down: Bool?) {
            guard let touch = touches.first else { return }; let point = touch.location(in:self)
            viewer?.pointer(x:point.x,y:point.y,viewWidth:bounds.width,viewHeight:bounds.height,down:down)
        }
    }
}
#endif
