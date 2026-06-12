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

    Shape {
        anchors.fill: parent
        preferredRendererType: Shape.CurveRenderer

        ShapePath {
            strokeColor: Qt.alpha(Kirigami.Theme.textColor, 0.18)
            strokeWidth: root.lineWidth
            fillColor: "transparent"
            capStyle: ShapePath.RoundCap

            PathAngleArc {
                centerX: root.width / 2
                centerY: root.height / 2
                radiusX: root.width / 2 - root.lineWidth
                radiusY: root.height / 2 - root.lineWidth
                startAngle: 0
                sweepAngle: 360
            }
        }

        ShapePath {
            strokeColor: Kirigami.Theme.highlightColor
            strokeWidth: root.lineWidth
            fillColor: "transparent"
            capStyle: ShapePath.RoundCap

            PathAngleArc {
                centerX: root.width / 2
                centerY: root.height / 2
                radiusX: root.width / 2 - root.lineWidth
                radiusY: root.height / 2 - root.lineWidth
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
