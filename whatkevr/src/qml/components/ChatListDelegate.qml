import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

ItemDelegate {
    id: root

    property string chatId: ""
    property string name: ""
    property string lastMessage: ""
    property int lastMessageDirection: 0
    property int lastMessageStatus: 0
    property string avatarLocalPath: ""
    property string initials: "?"
    property int unreadCount: 0
    property bool current: false
    readonly property bool hasLastMessage: lastMessage.length > 0
    readonly property bool lastMessageIsOutgoing: lastMessageDirection === 2
    readonly property bool showDeliveryStatus: hasLastMessage && lastMessageIsOutgoing
    readonly property real deliveryIconSize: Kirigami.Units.iconSizes.small
    readonly property real deliveryDoubleTickOffset: deliveryIconSize * 0.4
    readonly property bool deliveryStatusIsDoubleTick: lastMessageStatus === 3 || lastMessageStatus === 4
    readonly property bool deliveryStatusIsRead: lastMessageStatus === 4
    readonly property string deliveryStatusSingleIcon: {
        switch (lastMessageStatus) {
        case 1: return "clock"
        case 2: return "checkmark"
        case 5: return "dialog-error-symbolic"
        default: return ""
        }
    }


    signal selected(string chatId)

    width: ListView.view ? ListView.view.width : implicitWidth
    implicitHeight: Kirigami.Units.gridUnit * 4.4
    padding: 0
    hoverEnabled: true
    highlighted: current
    onClicked: selected(chatId)

    SystemPalette {
        id: activePalette
        colorGroup: SystemPalette.Active
    }

    background: Rectangle {
        anchors.fill: parent
        anchors.leftMargin: Kirigami.Units.smallSpacing
        anchors.rightMargin: Kirigami.Units.smallSpacing
        anchors.topMargin: Kirigami.Units.smallSpacing / 2
        anchors.bottomMargin: Kirigami.Units.smallSpacing / 2
        radius: Kirigami.Units.cornerRadius
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
                text: root.name
                elide: Text.ElideRight
                maximumLineCount: 1
                font.weight: root.unreadCount > 0 ? Font.DemiBold : Font.Medium
            }

            RowLayout {
                Layout.fillWidth: true
                Layout.minimumWidth: 0
                spacing: Kirigami.Units.smallSpacing

                Item {
                    visible: root.showDeliveryStatus
                    Layout.preferredWidth: root.deliveryStatusIsDoubleTick
                                           ? root.deliveryIconSize * 1.4
                                           : root.deliveryIconSize
                    Layout.preferredHeight: root.deliveryIconSize
                    Layout.alignment: Qt.AlignVCenter

                    Kirigami.Icon {
                        id: singleDeliveryIcon

                        anchors.centerIn: parent
                        visible: !root.deliveryStatusIsDoubleTick && root.deliveryStatusSingleIcon.length > 0
                        source: root.deliveryStatusSingleIcon
                        implicitWidth: root.deliveryIconSize
                        implicitHeight: root.deliveryIconSize
                        color: root.lastMessageStatus === 5
                               ? Kirigami.Theme.negativeTextColor
                               : Kirigami.Theme.disabledTextColor
                    }
                    Kirigami.Icon {
                        anchors.centerIn: parent
                        anchors.horizontalCenterOffset: 0.75
                        visible: singleDeliveryIcon.visible && root.deliveryStatusSingleIcon === "checkmark"
                        source: singleDeliveryIcon.source
                        implicitWidth: singleDeliveryIcon.implicitWidth
                        implicitHeight: singleDeliveryIcon.implicitHeight
                        color: singleDeliveryIcon.color
                    }

                    Item {
                        anchors.centerIn: parent
                        visible: root.deliveryStatusIsDoubleTick
                        width: root.deliveryIconSize * 1.4
                        height: root.deliveryIconSize

                        Kirigami.Icon {
                            id: firstDeliveryTick

                            x: 0
                            anchors.verticalCenter: parent.verticalCenter
                            source: "checkmark"
                            implicitWidth: root.deliveryIconSize
                            implicitHeight: root.deliveryIconSize
                            color: root.deliveryStatusIsRead
                                   ? activePalette.highlight
                                   : Kirigami.Theme.disabledTextColor
                        }
                        Kirigami.Icon {
                            x: firstDeliveryTick.x + 0.75
                            anchors.verticalCenter: parent.verticalCenter
                            source: firstDeliveryTick.source
                            implicitWidth: firstDeliveryTick.implicitWidth
                            implicitHeight: firstDeliveryTick.implicitHeight
                            color: firstDeliveryTick.color
                        }

                        Kirigami.Icon {
                            id: secondDeliveryTick

                            x: root.deliveryDoubleTickOffset
                            anchors.verticalCenter: parent.verticalCenter
                            source: "checkmark"
                            implicitWidth: root.deliveryIconSize
                            implicitHeight: root.deliveryIconSize
                            color: root.deliveryStatusIsRead
                                   ? activePalette.highlight
                                   : Kirigami.Theme.disabledTextColor
                        }
                        Kirigami.Icon {
                            x: secondDeliveryTick.x + 0.75
                            anchors.verticalCenter: parent.verticalCenter
                            source: secondDeliveryTick.source
                            implicitWidth: secondDeliveryTick.implicitWidth
                            implicitHeight: secondDeliveryTick.implicitHeight
                            color: secondDeliveryTick.color
                        }
                    }
                }

                Label {
                    Layout.fillWidth: true
                    Layout.minimumWidth: 0
                    text: root.hasLastMessage
                          ? root.lastMessage.replace(/[\r\n]+/g, " ")
                          : Whatevr.I18n.i18nc("@info", "No messages yet")
                    elide: Text.ElideRight
                    maximumLineCount: 1
                    color: root.unreadCount > 0 ? Kirigami.Theme.textColor : Kirigami.Theme.disabledTextColor
                    opacity: root.unreadCount > 0 ? 0.84 : 1.0
                }
            }
        }

        Rectangle {
            visible: root.unreadCount > 0
            Layout.preferredWidth: Math.max(unreadLabel.implicitWidth + Kirigami.Units.largeSpacing,
                                            Kirigami.Units.gridUnit * 1.5)
            Layout.preferredHeight: Kirigami.Units.gridUnit * 1.35
            radius: height / 2
            color: Kirigami.Theme.highlightColor

            Label {
                id: unreadLabel
                anchors.centerIn: parent
                text: root.unreadCount > 99 ? "99+" : String(root.unreadCount)
                color: Kirigami.Theme.highlightedTextColor
                font.weight: Font.Bold
                font.pointSize: Kirigami.Theme.smallFont.pointSize
            }
        }
    }
}
