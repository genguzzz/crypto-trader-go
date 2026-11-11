// Drives the watch app on the simulator with demo data: turns the pages and scrolls each one, leaving a
// screenshot per step as an attachment (xcresult). Run: xcodebuild test -scheme OKXWatchApp -destination '…watchOS Simulator…'
import XCTest

final class ScrollTests: XCTestCase {
    func testPagesAndScrolling() {
        let app = XCUIApplication()
        app.launchEnvironment["OKX_DEMO"] = "1"
        app.launch()
        shot("1-proposals")
        app.swipeUp()
        shot("2-proposals-scrolled")
        app.swipeLeft()
        sleep(1)
        shot("3-positions")
        app.swipeUp()
        sleep(1)
        shot("4-positions-scrolled")
        app.swipeDown()
        sleep(1)
        shot("5-positions-back")
        app.swipeLeft()
        sleep(1)
        shot("6-desk")
        app.swipeUp()
        sleep(1)
        shot("7-desk-scrolled")
        app.swipeRight()
        app.swipeRight()
        sleep(1)
        app.cells.firstMatch.tap()
        sleep(1)
        shot("8-detail")
        app.swipeUp()
        app.swipeUp()
        sleep(1)
        shot("9-detail-bottom")
    }

    private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
    }
}
