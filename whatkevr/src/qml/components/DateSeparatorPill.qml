import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami

// Capsule "squircle" pill used both as the inline day separator and as the
// floating date indicator while scrolling, so the two always look identical.
Rectangle {
    id: root

    required property string text

    implicitWidth: label.implicitWidth + Kirigami.Units.largeSpacing * 2
    implicitHeight: label.implicitHeight + Kirigami.Units.smallSpacing * 1.5
    radius: height / 2
    color: Qt.alpha(Kirigami.Theme.backgroundColor, 0.9)
    border.color: Qt.alpha(Kirigami.Theme.textColor, 0.12)

    Label {
        id: label

        anchors.centerIn: parent
        text: root.text
        font.pointSize: Kirigami.Theme.smallFont.pointSize
        font.weight: Font.Medium
        color: Kirigami.Theme.disabledTextColor
    }
}
