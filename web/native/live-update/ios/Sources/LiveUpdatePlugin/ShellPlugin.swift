import Foundation
import UIKit
import WebKit
import Capacitor

/// Native behaviour of the app shell that Capacitor has no config for: edge swipes navigate the
/// web history like Safari (the Vue router uses pushState, so this is "back to the previous page"),
/// and the page rubber-bands at its ends (Capacitor hard-codes scrollView.bounces = false).
@objc(ShellPlugin)
public class ShellPlugin: CAPPlugin, CAPBridgedPlugin {
    public let identifier = "ShellPlugin"
    public let jsName = "Shell"
    public let pluginMethods: [CAPPluginMethod] = []

    override public func load() {
        DispatchQueue.main.async {
            guard let webView = self.bridge?.webView else { return }
            webView.allowsBackForwardNavigationGestures = true
            webView.scrollView.bounces = true
            webView.scrollView.alwaysBounceVertical = true
            webView.scrollView.contentInsetAdjustmentBehavior = .never
        }
    }
}
