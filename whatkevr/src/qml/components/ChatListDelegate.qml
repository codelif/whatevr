import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami

ItemDelegate {
    id: root

    property string chatId: ""
    property string name: ""
    property string lastMessage: ""
    property string avatarLocalPath: ""
    property string initials: "?"
    property int unreadCount: 0
    property bool current: false

    signal selected(string chatId)

    width: ListView.view ? ListView.view.width : implicitWidth
    implicitHeight: Kirigami.Units.gridUnit * 4.4
    padding: 0
    hoverEnabled: true
    highlighted: current
    onClicked: selected(chatId)

    background: Rectangle {
        anchors.fill: parent
        anchors.leftMargin: Kirigami.Units.smallSpacing
        anchors.rightMargin: Kirigami.Units.smallSpacing
        anchors.topMargin: Kirigami.Units.smallSpacing / 2
        anchors.bottomMargin: Kirigami.Units.smallSpacing / 2
        radius: Kirigami.Units.largeSpacing
        color: root.highlighted
               ? Qt.alpha(Kirigami.Theme.highlightColor, 0.14)
               : (root.hovered ? Qt.alpha(Kirigami.Theme.textColor, 0.05) : "transparent")
    }

    contentItem: RowLayout {
        anchors.fill: parent
        anchors.leftMargin: Kirigami.Units.largeSpacing
        anchors.rightMargin: Kirigami.Units.largeSpacing
        spacing: Kirigami.Units.largeSpacing

        AvatarImage {
            Layout.preferredWidth: Kirigami.Units.gridUnit * 2.45
            Layout.preferredHeight: Layout.preferredWidth
            avatarLocalPath: root.avatarLocalPath
            initials: root.initials
            backgroundColor: Qt.alpha(foregroundColor, root.highlighted ? 0.22 : 0.14)
        }

        ColumnLayout {
            Layout.fillWidth: true
            spacing: Kirigami.Units.smallSpacing / 2

            Label {
                Layout.fillWidth: true
                text: name
                elide: Text.ElideRight
                maximumLineCount: 1
                font.weight: unreadCount > 0 ? Font.DemiBold : Font.Medium
            }

            Label {
                Layout.fillWidth: true
                text: lastMessage.length > 0
                      ? lastMessage.replace(/[\r\n]+/g, " ")
                      : i18nc("@info", "No messages yet")
                elide: Text.ElideRight
                maximumLineCount: 1
                color: unreadCount > 0 ? Kirigami.Theme.textColor : Kirigami.Theme.disabledTextColor
                opacity: unreadCount > 0 ? 0.84 : 1.0
            }
        }

        Rectangle {
            visible: unreadCount > 0
            Layout.preferredWidth: Math.max(unreadLabel.implicitWidth + Kirigami.Units.largeSpacing,
                                            Kirigami.Units.gridUnit * 1.5)
            Layout.preferredHeight: Kirigami.Units.gridUnit * 1.35
            radius: height / 2
            color: Kirigami.Theme.highlightColor

            Label {
                id: unreadLabel
                anchors.centerIn: parent
                text: unreadCount > 99 ? "99+" : String(unreadCount)
                color: Kirigami.Theme.highlightedTextColor
                font.weight: Font.Bold
                font.pointSize: Kirigami.Theme.smallFont.pointSize
            }
        }
    }
}
