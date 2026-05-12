import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami

Frame {
    id: root

    Layout.fillHeight: true
    Layout.minimumWidth: Kirigami.Units.gridUnit * 17
    Layout.preferredWidth: Kirigami.Units.gridUnit * 20
    Layout.maximumWidth: Kirigami.Units.gridUnit * 24
    padding: 0

    background: Rectangle {
        color: Kirigami.Theme.backgroundColor
        border.color: Qt.alpha(Kirigami.Theme.textColor, 0.10)
    }

    contentItem: ColumnLayout {
        spacing: 0

        ToolBar {
            Layout.fillWidth: true

            contentItem: RowLayout {
                anchors.fill: parent
                anchors.leftMargin: Kirigami.Units.largeSpacing
                anchors.rightMargin: Kirigami.Units.smallSpacing
                spacing: Kirigami.Units.smallSpacing

                Kirigami.Heading {
                    Layout.fillWidth: true
                    level: 2
                    text: i18nc("@title", "Chats")
                    elide: Text.ElideRight
                }

                ToolButton {
                    icon.name: "view-refresh-symbolic"
                    text: i18nc("@action:button", "Refresh")
                    display: AbstractButton.IconOnly
                    visible: AppController.bannerText.length > 0
                    enabled: AppController.primaryActionEnabled
                    onClicked: AppController.triggerPrimaryAction()
                }

                ToolButton {
                    icon.name: "system-log-out-symbolic"
                    text: i18nc("@action:button", "Log out")
                    display: AbstractButton.IconOnly
                    onClicked: AppController.logout()
                }
            }
        }

        Rectangle {
            Layout.fillWidth: true
            implicitHeight: 1
            color: Qt.alpha(Kirigami.Theme.textColor, 0.10)
        }

        HistorySyncStrip {
            Layout.margins: Kirigami.Units.largeSpacing
            Layout.bottomMargin: AppController.historySyncVisible ? Kirigami.Units.smallSpacing : 0
        }

        Kirigami.InlineMessage {
            Layout.fillWidth: true
            Layout.leftMargin: Kirigami.Units.largeSpacing
            Layout.rightMargin: Kirigami.Units.largeSpacing
            visible: AppController.bannerText.length > 0
            type: Kirigami.MessageType.Warning
            showCloseButton: false
            text: AppController.bannerText
        }

        Item {
            id: chatListViewport

            Layout.fillWidth: true
            Layout.fillHeight: true

            ListView {
                id: chatList

                anchors.fill: parent
                clip: true
                model: AppController.chatListModel
                currentIndex: -1
                boundsBehavior: Flickable.StopAtBounds
                flickableDirection: Flickable.VerticalFlick
                reuseItems: true
                spacing: 0
                cacheBuffer: Math.max(0, height)
                ScrollBar.vertical: ScrollBar {
                    policy: ScrollBar.AlwaysOn
                }

                delegate: ChatListDelegate {
                    chatId: String(model.chatId || "")
                    name: String(model.name || "")
                    lastMessage: String(model.lastMessage || "")
                    lastMessageDirection: Number(model.lastMessageDirection || 0)
                    lastMessageStatus: Number(model.lastMessageStatus || 0)
                    avatarLocalPath: String(model.avatarLocalPath || "")
                    initials: String(model.initials || "?")
                    unreadCount: Number(model.unreadCount || 0)
                    current: AppController.selectedChatId === chatId
                    onSelected: id => AppController.selectChat(id)
                }

                BusyIndicator {
                    anchors.centerIn: parent
                    running: AppController.chatsLoading && AppController.chatsEmpty
                    visible: running
                }

                Kirigami.PlaceholderMessage {
                    anchors.centerIn: parent
                    width: Math.min(parent.width - Kirigami.Units.largeSpacing * 4,
                                    Kirigami.Units.gridUnit * 16)
                    visible: !AppController.chatsLoading && AppController.chatsEmpty
                    text: i18nc("@info", "No chats yet")
                    explanation: i18nc("@info", "Chats will appear here as history sync stores them locally.")
                }
            }

            KineticWheelScroller {
                anchors.fill: chatList
                target: chatList
                wheelStep: Kirigami.Units.gridUnit * 4
            }
        }
    }
}
