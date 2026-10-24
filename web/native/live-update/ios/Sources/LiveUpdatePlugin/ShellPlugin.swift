import Foundation
import UIKit
import WebKit
import Capacitor

/// Native-feeling back swipe for the single-page app.
///
/// WKWebView's own back/forward gesture shows no preview for pushState navigations, so the shell
/// does it like UINavigationController: before each route change the web side calls capture()
/// and the page being left is kept as a snapshot view (cheap, render-server backed). A left-edge
/// pan slides the current page off over the previous page's snapshot (parallax + dim); releasing
/// past a third (or flicking) runs history.back(), and the overlay stays until the web side
/// reports the new route rendered (didNavigate). The sticky header and the tab bar stay in place:
/// the web side reports their heights and only the content between them moves.
@objc(ShellPlugin)
public class ShellPlugin: CAPPlugin, CAPBridgedPlugin, UIGestureRecognizerDelegate {
    public let identifier = "ShellPlugin"
    public let jsName = "Shell"
    public let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "capture", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "didNavigate", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "reset", returnType: CAPPluginReturnPromise)
    ]

    private var snapshots: [Int: UIView] = [:] // history position → snapshot of that page's content
    private var position = 0
    private var fixedTop: CGFloat = 0
    private var fixedBottom: CGFloat = 0
    private var overlay: UIView?
    private var moving: UIView?
    private var below: UIView?
    private var shade: UIView?
    private var finishing = false

    override public func load() {
        DispatchQueue.main.async {
            guard let webView = self.bridge?.webView else { return }
            webView.allowsBackForwardNavigationGestures = false
            webView.scrollView.contentInsetAdjustmentBehavior = .never
            let pan = UIScreenEdgePanGestureRecognizer(target: self, action: #selector(self.handlePan(_:)))
            pan.edges = .left
            pan.delegate = self
            webView.addGestureRecognizer(pan)
            webView.scrollView.panGestureRecognizer.require(toFail: pan)
        }
    }

    // MARK: - JS API

    /// Snapshot the current page before the router leaves it. position = its history position.
    @objc func capture(_ call: CAPPluginCall) {
        DispatchQueue.main.async {
            let pos = call.getInt("position") ?? self.position
            if self.overlay == nil, let snap = self.contentSnapshot() {
                self.snapshots[pos] = snap
                self.snapshots = self.snapshots.filter { abs($0.key - pos) <= 12 }
            }
            call.resolve()
        }
    }

    /// The router finished a navigation and the page has rendered.
    @objc func didNavigate(_ call: CAPPluginCall) {
        DispatchQueue.main.async {
            self.position = call.getInt("position") ?? self.position
            self.fixedTop = CGFloat(call.getDouble("top") ?? 0)
            self.fixedBottom = CGFloat(call.getDouble("bottom") ?? 0)
            if self.finishing { self.finishTransition() }
            call.resolve()
        }
    }

    /// A fresh page load: forget snapshots of the previous document.
    @objc func reset(_ call: CAPPluginCall) {
        DispatchQueue.main.async {
            self.snapshots.removeAll()
            self.position = 0
            self.finishing = false
            self.removeOverlay()
            call.resolve()
        }
    }

    // MARK: - Gesture

    public func gestureRecognizerShouldBegin(_ gestureRecognizer: UIGestureRecognizer) -> Bool {
        overlay == nil && !finishing && snapshots[position - 1] != nil
    }

    private func contentRect(_ bounds: CGRect) -> CGRect {
        CGRect(x: 0, y: fixedTop, width: bounds.width, height: max(0, bounds.height - fixedTop - fixedBottom))
    }

    private func contentSnapshot() -> UIView? {
        guard let webView = bridge?.webView else { return nil }
        let rect = contentRect(webView.bounds)
        guard rect.height > 0 else { return nil }
        return webView.resizableSnapshotView(from: rect, afterScreenUpdates: false, withCapInsets: .zero)
    }

    @objc private func handlePan(_ pan: UIScreenEdgePanGestureRecognizer) {
        guard let webView = bridge?.webView else { return }
        let width = max(webView.bounds.width, 1)
        switch pan.state {
        case .began:
            guard let previous = snapshots[position - 1], let current = contentSnapshot() else { return }
            let container = UIView(frame: contentRect(webView.bounds))
            container.clipsToBounds = true
            container.backgroundColor = .black
            previous.transform = .identity // reused after a cancelled swipe
            previous.frame = container.bounds
            container.addSubview(previous)
            let shade = UIView(frame: container.bounds)
            shade.backgroundColor = UIColor.black.withAlphaComponent(0.3)
            container.addSubview(shade)
            current.frame = container.bounds
            current.layer.shadowColor = UIColor.black.cgColor
            current.layer.shadowOpacity = 0.4
            current.layer.shadowRadius = 10
            current.layer.shadowOffset = CGSize(width: -3, height: 0)
            current.layer.shadowPath = UIBezierPath(rect: current.bounds).cgPath
            current.layer.masksToBounds = false
            container.addSubview(current)
            webView.addSubview(container)
            overlay = container
            moving = current
            below = previous
            self.shade = shade
            layout(progress: 0, width: width)
        case .changed:
            guard overlay != nil else { return }
            layout(progress: max(0, min(1, pan.translation(in: webView).x / width)), width: width)
        case .ended, .cancelled, .failed:
            guard overlay != nil else { return }
            let progress = max(0, min(1, pan.translation(in: webView).x / width))
            let velocity = pan.velocity(in: webView).x
            let commit = pan.state == .ended && velocity > -300 && (progress > 0.33 || velocity > 600)
            let remaining = commit ? 1 - progress : progress
            UIView.animate(withDuration: max(0.12, 0.32 * Double(remaining)), delay: 0, options: [.curveEaseOut, .allowUserInteraction]) {
                self.layout(progress: commit ? 1 : 0, width: width)
            } completion: { _ in
                guard commit else {
                    self.removeOverlay()
                    return
                }
                // Keep showing the previous page's snapshot until the router has rendered it.
                self.finishing = true
                self.snapshots.removeValue(forKey: self.position - 1)
                webView.evaluateJavaScript("history.back()", completionHandler: nil)
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) { self.finishTransition() }
            }
        default:
            break
        }
    }

    private func layout(progress: CGFloat, width: CGFloat) {
        moving?.transform = CGAffineTransform(translationX: width * progress, y: 0)
        below?.transform = CGAffineTransform(translationX: -width * 0.3 * (1 - progress), y: 0)
        shade?.alpha = 1 - progress
    }

    private func finishTransition() {
        guard finishing else { return }
        finishing = false
        guard let overlay else { return }
        UIView.animate(withDuration: 0.12, animations: { overlay.alpha = 0 }, completion: { _ in self.removeOverlay() })
    }

    private func removeOverlay() {
        overlay?.removeFromSuperview()
        overlay = nil
        moving = nil
        below = nil
        shade = nil
    }
}
