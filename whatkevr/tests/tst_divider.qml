// Standalone check for DN14, deliberately NOT wired into ctest: it verifies the
// placement arithmetic against numbers recorded from a real session, it does not
// instantiate MessageView, so it guards the reasoning rather than the code.
//
//   QT_QPA_PLATFORM=offscreen qmltestrunner -input whatkevr/tests/tst_divider.qml -o -,txt
//
import QtQuick
import QtTest

// Reproduces the three real opens from Harsh's WHATKEVR_PERF trace: a 758px
// viewport with anchor rows of 473px (open 1, which looked right) and 1021px /
// 1032px (opens 2 and 3, where the divider was off the top). Checks where the
// top of the anchor row — which is where the unread divider is drawn — lands
// under the old row-centring rule and the new divider-centring rule.
TestCase {
    id: testCase

    name: "UnreadDividerPlacement"
    when: windowShown
    width: 400
    height: 758

    readonly property real listHeight: 758

    function dividerOffsetOnScreen(itemY, itemH, contentY) {
        return itemY - contentY
    }

    function test_placement_data() {
        return [
            { tag: "open1 short row",  itemY: 3460, itemH: 473 },
            { tag: "open2 tall row",   itemY: 3992, itemH: 1021 },
            { tag: "open3 tall row",   itemY: 5238, itemH: 1032 },
        ]
    }

    function test_placement(row) {
        const centreRow = row.itemY + (row.itemH - testCase.listHeight) / 2
        const centreDivider = row.itemY - testCase.listHeight / 2

        const oldOnScreen = dividerOffsetOnScreen(row.itemY, row.itemH, centreRow)
        const newOnScreen = dividerOffsetOnScreen(row.itemY, row.itemH, centreDivider)

        console.log(row.tag
                    + " | itemH=" + row.itemH
                    + " | OLD contentY=" + centreRow.toFixed(0)
                    + " divider at y=" + oldOnScreen.toFixed(0)
                    + (oldOnScreen < 0 ? "  <-- OFF SCREEN ABOVE" : "  (visible)")
                    + " | NEW contentY=" + centreDivider.toFixed(0)
                    + " divider at y=" + newOnScreen.toFixed(0)
                    + (newOnScreen < 0 ? "  <-- OFF SCREEN ABOVE" : "  (visible)"))

        // The whole point: the divider must be on screen, and centred.
        verify(newOnScreen >= 0, "divider above the viewport top")
        verify(newOnScreen <= testCase.listHeight, "divider below the viewport bottom")
        compare(newOnScreen, testCase.listHeight / 2, "divider not centred")
    }
}
