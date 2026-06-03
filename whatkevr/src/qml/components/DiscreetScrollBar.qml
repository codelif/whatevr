import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami

ScrollBar {
    id: root

    policy: ScrollBar.AlwaysOn
    hoverEnabled: true
    interactive: true
    implicitWidth: hovered || pressed || active ? Kirigami.Units.smallSpacing * 1.5 : 2
    width: implicitWidth

    // Smallest the visible thumb is allowed to get; keeps it grabbable/visible
    // even when the proportional size would shrink to a couple of pixels.
    readonly property real minThumb: Kirigami.Units.gridUnit
    readonly property real thumbHeight: Math.max(root.size * root.height, minThumb)

    Behavior on implicitWidth {
        NumberAnimation {
            duration: Kirigami.Units.shortDuration
            easing.type: Easing.OutCubic
        }
    }

    // Control-managed drag/hit target; not used for visuals.
    background: Item {}
    contentItem: Item {}

    // Visible thumb, positioned by reactive bindings (not the control's
    // imperative resizeContent) so it refreshes immediately on window resize.
    Rectangle {
        id: thumb

        // size >= 1 means content fully fits -> nothing to scroll.
        visible: root.size > 0 && root.size < 1
        width: root.implicitWidth
        x: (root.width - width) / 2
        height: root.thumbHeight
        radius: width / 2

        // Remap position (0..1-size) onto the reduced travel of a min-clamped
        // thumb, then clamp into the track so it never floats off-screen.
        y: {
            const travel = root.height - height
            if (travel <= 0)
                return 0
            const denom = 1 - root.size
            const frac = denom > 0 ? root.position / denom : 0
            return Math.max(0, Math.min(travel, frac * travel))
        }

        color: Qt.alpha(Kirigami.Theme.textColor,
                        root.hovered || root.pressed || root.active ? 0.42 : 0.22)

        Behavior on width {
            NumberAnimation {
                duration: Kirigami.Units.shortDuration
                easing.type: Easing.OutCubic
            }
        }
    }
}
