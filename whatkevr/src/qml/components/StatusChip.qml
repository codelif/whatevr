import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami

Rectangle {
    id: root

    required property string text
    property color foregroundColor: Kirigami.Theme.highlightColor
    property color backgroundColor: Qt.alpha(foregroundColor, 0.14)

    radius: Kirigami.Units.largeSpacing
    implicitWidth: label.implicitWidth + Kirigami.Units.largeSpacing * 2
    implicitHeight: label.implicitHeight + Kirigami.Units.smallSpacing * 1.5
    color: root.backgroundColor

    Label {
        id: label

        anchors.centerIn: parent
        text: root.text
        color: root.foregroundColor
        font.weight: Font.Medium
    }
}
