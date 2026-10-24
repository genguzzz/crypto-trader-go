import Foundation
import UIKit
import CryptoKit
import Capacitor

/// Over-the-air web bundles for the ctg app.
///
/// The JS side (web/src/native.ts) asks the server for the current bundle manifest and calls
/// download(); files land in Library/NoCloud/ionic_built_snapshots/<version>, the folder
/// Capacitor itself loads a persisted `serverBasePath` from, so the next launch serves the new
/// bundle (a new binary resets it). A staged bundle is also switched to as soon as the app goes
/// to the background. A bundle that doesn't call ready() within `readyTimeout` seconds of
/// being active is rolled back to the last confirmed one and never offered again.
@objc(LiveUpdatePlugin)
public class LiveUpdatePlugin: CAPPlugin, CAPBridgedPlugin {
    public let identifier = "LiveUpdatePlugin"
    public let jsName = "LiveUpdate"
    public let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "getState", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "download", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "ready", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "reset", returnType: CAPPluginReturnPromise)
    ]

    private static let pathKey = "serverBasePath" // Capacitor's own key, read at launch
    private static let confirmedKey = "ctgConfirmedBundle"
    private static let failedKey = "ctgFailedBundles"
    private let readyTimeout: TimeInterval = 15
    private let store = KeyValueStore.standard
    private var watchdog: DispatchWorkItem?
    private var downloading = false

    private lazy var snapshots: URL = FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0]
        .appendingPathComponent("NoCloud", isDirectory: true)
        .appendingPathComponent("ionic_built_snapshots", isDirectory: true)

    override public func load() {
        let nc = NotificationCenter.default
        nc.addObserver(self, selector: #selector(didEnterBackground), name: UIApplication.didEnterBackgroundNotification, object: nil)
        nc.addObserver(self, selector: #selector(didBecomeActive), name: UIApplication.didBecomeActiveNotification, object: nil)
        DispatchQueue.main.async {
            if UIApplication.shared.applicationState == .active { self.armWatchdog() }
        }
    }

    // MARK: - State

    private var viewController: CAPBridgeViewController? { bridge?.viewController as? CAPBridgeViewController }

    private var builtinPath: String? { Bundle.main.url(forResource: "public", withExtension: nil)?.path }

    /// Bundle the web view serves now; nil = the one shipped in the binary.
    private var running: String? {
        guard let path = viewController?.getServerBasePath(), !path.isEmpty else { return nil }
        let url = URL(fileURLWithPath: path)
        return url.deletingLastPathComponent().lastPathComponent == "ionic_built_snapshots" ? url.lastPathComponent : nil
    }

    /// Bundle the next launch loads.
    private var staged: String? {
        guard let path = store[Self.pathKey, as: String.self], !path.isEmpty else { return nil }
        return URL(fileURLWithPath: path).lastPathComponent
    }

    private var confirmed: String? {
        get { store[Self.confirmedKey, as: String.self] }
        set { store[Self.confirmedKey] = newValue }
    }

    private var failed: [String] {
        get { store[Self.failedKey, as: [String].self] ?? [] }
        set { store[Self.failedKey] = newValue }
    }

    // MARK: - JS API

    @objc func getState(_ call: CAPPluginCall) {
        DispatchQueue.main.async {
            let info = Bundle.main.infoDictionary ?? [:]
            call.resolve([
                "runtime": info["CTGRuntime"] as? String ?? "",
                "binary": info["CFBundleShortVersionString"] as? String ?? "",
                "build": info["CFBundleVersion"] as? String ?? "",
                "running": self.running ?? "",
                "staged": self.staged ?? "",
                "downloading": self.downloading,
                "failed": self.failed.map { $0 as JSValue }
            ])
        }
    }

    /// files: [{ path, sha256 }] relative to baseUrl; must include index.html.
    @objc func download(_ call: CAPPluginCall) {
        guard let version = call.getString("version"),
              version.range(of: "^[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}$", options: .regularExpression) != nil,
              let base = call.getString("baseUrl").flatMap({ URL(string: $0) }),
              let raw = call.getArray("files") else {
            call.reject("bad arguments")
            return
        }
        var files: [(path: String, sha256: String)] = []
        for item in raw {
            guard let entry = item as? JSObject, let path = entry["path"] as? String, let hash = entry["sha256"] as? String,
                  !path.isEmpty, !path.hasPrefix("/"),
                  !path.split(separator: "/").contains(where: { $0 == ".." || $0 == "." }) else {
                call.reject("bad file entry")
                return
            }
            files.append((path, hash.lowercased()))
        }
        guard files.contains(where: { $0.path == "index.html" }) else {
            call.reject("bundle has no index.html")
            return
        }
        DispatchQueue.main.async {
            if self.downloading {
                call.reject("download in progress")
                return
            }
            if self.failed.contains(version) {
                call.reject("bundle \(version) failed before")
                return
            }
            if version == self.running || version == self.staged {
                call.resolve(["version": version])
                return
            }
            self.downloading = true
            let reuseFrom = self.viewController?.getServerBasePath()
            Task.detached {
                do {
                    let dir = try await self.fetch(version: version, base: base, files: files, reuseFrom: reuseFrom)
                    await MainActor.run {
                        self.downloading = false
                        self.store[Self.pathKey] = dir.path
                        CAPLog.print("LiveUpdate: staged bundle \(version)")
                        call.resolve(["version": version])
                    }
                } catch {
                    await MainActor.run { self.downloading = false }
                    call.reject("download failed: \(error.localizedDescription)")
                }
            }
        }
    }

    /// Called by every web bundle once it has started; confirms it as good.
    @objc func ready(_ call: CAPPluginCall) {
        DispatchQueue.main.async {
            self.watchdog?.cancel()
            self.watchdog = nil
            let current = self.running
            self.confirmed = current
            if !self.downloading { self.cleanup(keep: Set([current, self.staged].compactMap { $0 })) }
            call.resolve(["running": current ?? ""])
        }
    }

    /// Drops every downloaded bundle and reloads the one shipped in the binary.
    @objc func reset(_ call: CAPPluginCall) {
        DispatchQueue.main.async {
            self.watchdog?.cancel()
            self.watchdog = nil
            self.store[Self.pathKey] = nil as String?
            self.confirmed = nil
            self.failed = []
            if let path = self.builtinPath { self.viewController?.setServerBasePath(path: path) }
            call.resolve()
        }
    }

    // MARK: - Lifecycle

    @objc private func didEnterBackground() {
        watchdog?.cancel()
        watchdog = nil
        guard !downloading, let next = staged, next != running, !failed.contains(next) else { return }
        let dir = snapshots.appendingPathComponent(next, isDirectory: true)
        guard FileManager.default.fileExists(atPath: dir.appendingPathComponent("index.html").path) else { return }
        CAPLog.print("LiveUpdate: switching to bundle \(next)")
        viewController?.setServerBasePath(path: dir.path)
    }

    @objc private func didBecomeActive() {
        armWatchdog()
    }

    private func armWatchdog() {
        guard watchdog == nil, let current = running, current != confirmed else { return }
        let item = DispatchWorkItem { [weak self] in
            self?.watchdog = nil
            self?.rollback(current)
        }
        watchdog = item
        DispatchQueue.main.asyncAfter(deadline: .now() + readyTimeout, execute: item)
    }

    private func rollback(_ bad: String) {
        guard running == bad, bad != confirmed else { return }
        CAPLog.print("LiveUpdate: bundle \(bad) never reported ready, rolling back")
        failed = Array((failed.filter { $0 != bad } + [bad]).suffix(20))
        var good: URL?
        if let name = confirmed {
            let dir = snapshots.appendingPathComponent(name, isDirectory: true)
            if FileManager.default.fileExists(atPath: dir.path) { good = dir }
        }
        store[Self.pathKey] = good?.path
        if let path = good?.path ?? builtinPath { viewController?.setServerBasePath(path: path) }
    }

    // MARK: - Files

    private func cleanup(keep: Set<String>) {
        let fm = FileManager.default
        guard let names = try? fm.contentsOfDirectory(atPath: snapshots.path) else { return }
        for name in names where !keep.contains(name) {
            try? fm.removeItem(at: snapshots.appendingPathComponent(name))
        }
    }

    private func fetch(version: String, base: URL, files: [(path: String, sha256: String)], reuseFrom: String?) async throws -> URL {
        let fm = FileManager.default
        try fm.createDirectory(at: snapshots, withIntermediateDirectories: true)
        let tmp = snapshots.appendingPathComponent(".\(version).part", isDirectory: true)
        let dest = snapshots.appendingPathComponent(version, isDirectory: true)
        try? fm.removeItem(at: tmp)
        try fm.createDirectory(at: tmp, withIntermediateDirectories: true)
        let session = URLSession(configuration: .ephemeral)
        defer { session.finishTasksAndInvalidate() }
        do {
            for file in files {
                let target = tmp.appendingPathComponent(file.path)
                try fm.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
                // Hashed asset names rarely change: copy unchanged files from the bundle in use.
                if let reuseFrom,
                   let data = fm.contents(atPath: URL(fileURLWithPath: reuseFrom).appendingPathComponent(file.path).path),
                   Self.sha256(data) == file.sha256 {
                    try data.write(to: target)
                    continue
                }
                var request = URLRequest(url: base.appendingPathComponent(file.path))
                request.cachePolicy = .reloadIgnoringLocalCacheData
                request.timeoutInterval = 60
                let (data, response) = try await session.data(for: request)
                let status = (response as? HTTPURLResponse)?.statusCode ?? 0
                guard status == 200 else { throw LiveUpdateError("HTTP \(status) for \(file.path)") }
                guard Self.sha256(data) == file.sha256 else { throw LiveUpdateError("checksum mismatch for \(file.path)") }
                try data.write(to: target)
            }
            try? fm.removeItem(at: dest)
            try fm.moveItem(at: tmp, to: dest)
            return dest
        } catch {
            try? fm.removeItem(at: tmp)
            throw error
        }
    }

    private static func sha256(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }
}

private struct LiveUpdateError: LocalizedError {
    let errorDescription: String?
    init(_ message: String) { errorDescription = message }
}
