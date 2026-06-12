import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Full-width "N unread messages" divider shown above the first unread message
// when a chat is opened. Styled to pair with DateSeparatorPill: a centred
// capsule flanked by hairlines.
Item {
    id: root

    required property int count

    implicitHeight: pill.implicitHeight

    Rectangle {
        anchors.left: parent.left
        anchors.right: pill.left
        anchors.rightMargin: Kirigami.Units.largeSpacing
        anchors.verticalCenter: parent.verticalCenter
        height: 1
        color: Qt.alpha(Kirigami.Theme.highlightColor, 0.45)
    }

    Rectangle {
        id: pill

        anchors.horizontalCenter: parent.horizontalCenter
        implicitWidth: label.implicitWidth + Kirigami.Units.largeSpacing * 2
        implicitHeight: label.implicitHeight + Kirigami.Units.smallSpacing * 1.5
        radius: height / 2
        color: Qt.alpha(Kirigami.Theme.highlightColor, 0.14)
        border.color: Qt.alpha(Kirigami.Theme.highlightColor, 0.35)

        Label {
            id: label

            anchors.centerIn: parent
            text: Whatevr.I18n.i18ncp("@info divider above the first unread message", "%1 unread message", "%1 unread messages", root.count)
            font.pointSize: Kirigami.Theme.smallFont.pointSize
            font.weight: Font.DemiBold
            color: Kirigami.Theme.textColor
        }
    }

    Rectangle {
        anchors.left: pill.right
        anchors.leftMargin: Kirigami.Units.largeSpacing
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        height: 1
        color: Qt.alpha(Kirigami.Theme.highlightColor, 0.45)
    }
}
