import QtQuick
import org.kde.kirigami as Kirigami

// Index-based overlay scrollbar for ListViews with variable-height delegates.
//
// Qt's attached ScrollBar maps content geometry (viewport / contentHeight),
// but in a ListView those are *estimates* the view keeps revising as
// delegates materialise: the thumb breathes while scrolling, and during a
// drag the mouse-to-contentY mapping shifts underneath the pointer, which
// reads as stutter. This scrollbar maps rows instead — thumb size and
// position come from visible row indices, which only change when rows
// actually enter or leave the viewport — and while pressed the thumb is
// pointer-bound, so the view chases the thumb and never the reverse.
//
// Written for a BottomToTop ListView: index 0 = newest = visual bottom,
// highest index = oldest = visual top.
Item {
    id: root

    // Row-window inputs, bound by the owner (see MessageView.updateScrollState).
    property int count: 0
    // Row at the visual top of the viewport (highest visible index).
    property int topVisibleIndex: -1
    // Row at the visual bottom of the viewport (lowest visible index).
    property int bottomVisibleIndex: -1
    // Fraction (0..1) of the top row scrolled off above the viewport.
    property real topRowFraction: 0

    // Anchor `index` at the visual top, then hide `fraction` of it above.
    signal dragPositionRequested(int index, real fraction)
    signal jumpToNewestRequested()

    readonly property bool dragging: dragArea.pressed
    readonly property bool hoveredOrActive: hoverHandler.hovered || dragArea.pressed

    readonly property real minThumb: Kirigami.Units.gridUnit
    readonly property real visualWidth: hoveredOrActive ? Kirigami.Units.smallSpacing * 1.5 : 2

    readonly property real visibleSpan: topVisibleIndex >= 0 && bottomVisibleIndex >= 0
                                        ? Math.max(1, topVisibleIndex - bottomVisibleIndex + 1)
                                        : 1
    readonly property bool scrollable: count > 0 && visibleSpan < count
    // Whole rows fully above the viewport plus the fractional part of the top
    // row: sweeps continuously as rows pass the top edge, so the thumb moves
    // smoothly instead of in row-sized steps.
    readonly property real rowsAbove: Math.max(0, (count - 1 - topVisibleIndex) + topRowFraction)
    readonly property real denom: Math.max(1, count - visibleSpan)
    readonly property real posFraction: Math.max(0, Math.min(1, rowsAbove / denom))
    readonly property real thumbHeight: Math.max(minThumb, (visibleSpan / Math.max(1, count)) * height)
    readonly property real travel: Math.max(0, height - thumbHeight)

    // Pointer-bound thumb position while dragging.
    property real dragThumbY: 0
    property real pressOffset: 0

    // Constant hit width for grabbability; only the visual thumb animates.
    width: Kirigami.Units.smallSpacing * 2

    function requestFromThumb() {
        // Qt.callLater coalesces repeated calls into one invocation per
        // event-loop pass — the once-per-frame throttle for repositioning.
        Qt.callLater(flushDrag)
    }

    function flushDrag() {
        if (!dragArea.pressed || !scrollable) {
            return
        }
        const frac = travel > 0 ? dragThumbY / travel : 0
        if (frac <= 0.001) {
            // Track top: oldest row flush with the viewport top.
            dragPositionRequested(count - 1, 0)
            return
        }
        if (frac >= 0.999) {
            jumpToNewestRequested()
            return
        }
        const targetRowsAbove = frac * denom
        const whole = Math.floor(targetRowsAbove)
        const idx = Math.max(0, Math.min(count - 1, count - 1 - whole))
        dragPositionRequested(idx, targetRowsAbove - whole)
    }

    HoverHandler {
        id: hoverHandler
    }

    MouseArea {
        id: dragArea

        anchors.fill: parent
        acceptedButtons: Qt.LeftButton
        enabled: root.scrollable
        // Keep the grab for the whole drag: the message view's full-area
        // drag-eater DragHandler would otherwise take over once the pointer
        // passes the drag threshold, freezing the thumb after a few pixels.
        preventStealing: true

        onPressed: mouse => {
            const within = mouse.y >= thumb.y && mouse.y <= thumb.y + thumb.height
            root.pressOffset = within ? mouse.y - thumb.y : thumb.height / 2
            root.dragThumbY = Math.max(0, Math.min(root.travel, mouse.y - root.pressOffset))
            root.requestFromThumb()
        }
        onPositionChanged: mouse => {
            if (!pressed) {
                return
            }
            root.dragThumbY = Math.max(0, Math.min(root.travel, mouse.y - root.pressOffset))
            root.requestFromThumb()
        }
    }

    Rectangle {
        id: thumb

        visible: root.scrollable
        width: root.visualWidth
        x: parent.width - width - 1
        radius: width / 2
        height: root.thumbHeight
        y: dragArea.pressed ? root.dragThumbY : root.posFraction * root.travel
        color: Qt.alpha(Kirigami.Theme.textColor, root.hoveredOrActive ? 0.42 : 0.22)

        // History pages append 80 rows at once; ease the resulting thumb
        // shrink instead of popping.
        Behavior on height {
            NumberAnimation {
                duration: Kirigami.Units.shortDuration * 2
                easing.type: Easing.OutCubic
            }
        }

        Behavior on width {
            NumberAnimation {
                duration: Kirigami.Units.shortDuration
                easing.type: Easing.OutCubic
            }
        }
    }
}
