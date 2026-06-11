import QtQuick
import QtQuick.Controls
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
    property bool isPinned: false
    property bool current: false
    readonly property bool hasLastMessage: lastMessage.length > 0
    readonly property bool lastMessageIsOutgoing: lastMessageDirection === 2
    readonly property bool showDeliveryStatus: hasLastMessage && lastMessageIsOutgoing
    readonly property real deliveryIconSize: Kirigami.Units.iconSizes.small
    readonly property real deliveryDoubleTickOffset: deliveryIconSize * 0.4
    readonly property bool deliveryStatusIsDoubleTick: lastMessageStatus === 3 || lastMessageStatus === 4
    readonly property bool deliveryStatusIsRead: lastMessageStatus === 4
    readonly property url tickSource: "qrc:/data/icons/checkmark-bold.svg"
    readonly property string deliveryStatusSingleIcon: {
        switch (lastMessageStatus) {
        case 1: return "clock"
        case 2: return root.tickSource
        case 5: return "dialog-error-symbolic"
        default: return ""
        }
    }

    signal selected(string chatId)
    signal pinToggled(string chatId, bool pinned)
    signal contextMenuRequested(string chatId, bool pinned, real x, real y)

    width: ListView.view ? ListView.view.width : implicitWidth
    implicitHeight: Kirigami.Units.gridUnit * 4.4
    padding: 0
    hoverEnabled: true
    highlighted: current
    onClicked: selected(chatId)

    MouseArea {
        anchors.fill: parent
        acceptedButtons: Qt.RightButton
        hoverEnabled: false
        z: 1

        onPressed: mouse => {
            root.contextMenuRequested(root.chatId, root.isPinned, mouse.x, mouse.y)
            mouse.accepted = true
        }
    }

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

    // Anchor-based layout (no QtQuick Layouts): the constraint solver is the
    // dominant per-frame cost when the full-width rows are resized in
    // single-column mode, so the row is positioned with plain anchors instead.
    contentItem: Item {
        id: content

        implicitHeight: Kirigami.Units.gridUnit * 4.4

        AvatarImage {
            id: avatar

            anchors.left: parent.left
            anchors.leftMargin: Kirigami.Units.largeSpacing
            anchors.verticalCenter: parent.verticalCenter
            width: Kirigami.Units.gridUnit * 2.45
            height: width
            avatarLocalPath: root.avatarLocalPath
            initials: root.initials
            backgroundColor: Qt.alpha(foregroundColor, root.highlighted ? 0.22 : 0.14)
        }

        Column {
            id: trailing

            visible: root.unreadCount > 0 || root.isPinned
            anchors.right: parent.right
            anchors.rightMargin: Kirigami.Units.largeSpacing
            anchors.verticalCenter: parent.verticalCenter
            spacing: Kirigami.Units.smallSpacing / 2

            Kirigami.Icon {
                visible: root.isPinned
                anchors.horizontalCenter: parent.horizontalCenter
                width: Kirigami.Units.iconSizes.small
                height: width
                source: "window-pin"
                color: Kirigami.Theme.disabledTextColor
            }

            Rectangle {
                visible: root.unreadCount > 0
                anchors.horizontalCenter: parent.horizontalCenter
                width: Math.max(unreadLabel.implicitWidth + Kirigami.Units.largeSpacing,
                                Kirigami.Units.gridUnit * 1.5)
                height: Kirigami.Units.gridUnit * 1.35
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

        Item {
            id: middle

            anchors.left: avatar.right
            anchors.leftMargin: Kirigami.Units.largeSpacing
            anchors.right: trailing.visible ? trailing.left : parent.right
            anchors.rightMargin: Kirigami.Units.largeSpacing
            anchors.verticalCenter: parent.verticalCenter
            height: nameLabel.implicitHeight + Kirigami.Units.smallSpacing / 2 + lastRow.height

            Label {
                id: nameLabel

                anchors.left: parent.left
                anchors.right: parent.right
                anchors.top: parent.top
                text: root.name
                elide: Text.ElideRight
                maximumLineCount: 1
                font.weight: root.unreadCount > 0 ? Font.DemiBold : Font.Medium
            }

            Item {
                id: lastRow

                anchors.left: parent.left
                anchors.right: parent.right
                anchors.top: nameLabel.bottom
                anchors.topMargin: Kirigami.Units.smallSpacing / 2
                height: Math.max(lastMessageLabel.implicitHeight,
                                 root.showDeliveryStatus ? root.deliveryIconSize : 0)

                Item {
                    id: deliveryStatus

                    visible: root.showDeliveryStatus
                    anchors.left: parent.left
                    anchors.verticalCenter: parent.verticalCenter
                    width: visible
                           ? (root.deliveryStatusIsDoubleTick ? root.deliveryIconSize * 1.4 : root.deliveryIconSize)
                           : 0
                    height: root.deliveryIconSize

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
                        isMask: true
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
                            source: root.tickSource
                            implicitWidth: root.deliveryIconSize
                            implicitHeight: root.deliveryIconSize
                            color: root.deliveryStatusIsRead
                                   ? activePalette.highlight
                                   : Kirigami.Theme.disabledTextColor
                            isMask: true
                        }

                        Kirigami.Icon {
                            id: secondDeliveryTick

                            x: root.deliveryDoubleTickOffset
                            anchors.verticalCenter: parent.verticalCenter
                            source: root.tickSource
                            implicitWidth: root.deliveryIconSize
                            implicitHeight: root.deliveryIconSize
                            color: root.deliveryStatusIsRead
                                   ? activePalette.highlight
                                   : Kirigami.Theme.disabledTextColor
                            isMask: true
                        }
                    }
                }

                Label {
                    id: lastMessageLabel

                    anchors.left: deliveryStatus.visible ? deliveryStatus.right : parent.left
                    anchors.leftMargin: deliveryStatus.visible ? Kirigami.Units.smallSpacing : 0
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
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
    }
}
