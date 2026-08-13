import QtQuick
import QtQuick.Controls
import QtQuick.Shapes
import org.kde.kirigami as Kirigami

// Determinate circular progress ring with a centred percentage, used for
// media downloads (the indeterminate counterpart stays a BusyIndicator).
Item {
    id: root

    // 0..1
    property real progress: 0
    property bool showLabel: true
    property real lineWidth: Math.max(2, Math.round(width / 14))
    property color trackColor: Qt.alpha(Kirigami.Theme.textColor, 0.18)
    property color fillColor: Kirigami.Theme.highlightColor
    // Half the stroke sits outside the arc radius, so by default the ring is
    // fully inside the item. A video note insets its picture instead and wants
    // the ring on the very edge.
    property real inset: lineWidth

    Shape {
        anchors.fill: parent
        preferredRendererType: Shape.CurveRenderer

        ShapePath {
            strokeColor: root.trackColor
            strokeWidth: root.lineWidth
            fillColor: "transparent"
            capStyle: ShapePath.RoundCap

            PathAngleArc {
                centerX: root.width / 2
                centerY: root.height / 2
                radiusX: root.width / 2 - root.inset
                radiusY: root.height / 2 - root.inset
                startAngle: 0
                sweepAngle: 360
            }
        }

        ShapePath {
            strokeColor: root.fillColor
            strokeWidth: root.lineWidth
            fillColor: "transparent"
            capStyle: ShapePath.RoundCap

            PathAngleArc {
                centerX: root.width / 2
                centerY: root.height / 2
                radiusX: root.width / 2 - root.inset
                radiusY: root.height / 2 - root.inset
                startAngle: -90
                sweepAngle: 360 * Math.max(0, Math.min(1, root.progress))

                Behavior on sweepAngle {
                    NumberAnimation {
                        duration: Kirigami.Units.shortDuration
                        easing.type: Easing.OutCubic
                    }
                }
            }
        }
    }

    Label {
        anchors.centerIn: parent
        visible: root.showLabel
        text: Math.round(Math.max(0, Math.min(1, root.progress)) * 100) + "%"
        font.pointSize: Kirigami.Theme.smallFont.pointSize * 0.85
        font.weight: Font.DemiBold
        color: Kirigami.Theme.textColor
    }
}
