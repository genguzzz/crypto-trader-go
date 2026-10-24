// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "CtgLiveUpdate",
    platforms: [.iOS(.v15)],
    products: [
        .library(name: "CtgLiveUpdate", targets: ["LiveUpdatePlugin"])
    ],
    dependencies: [
        .package(url: "https://github.com/ionic-team/capacitor-swift-pm.git", from: "8.0.0")
    ],
    targets: [
        .target(
            name: "LiveUpdatePlugin",
            dependencies: [.product(name: "Capacitor", package: "capacitor-swift-pm")],
            path: "ios/Sources/LiveUpdatePlugin")
    ]
)
