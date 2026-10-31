// swift-tools-version:6.0
import PackageDescription

let package = Package(
    name: "KamuiMenuBar",
    platforms: [.macOS(.v13)],
    products: [
        .executable(name: "KamuiMenuBar", targets: ["KamuiMenuBar"]),
    ],
    targets: [
        .target(name: "KamuiMenuBarCore"),
        .executableTarget(name: "KamuiMenuBar", dependencies: ["KamuiMenuBarCore"]),
        .testTarget(name: "KamuiMenuBarCoreTests", dependencies: ["KamuiMenuBarCore"]),
    ]
)
