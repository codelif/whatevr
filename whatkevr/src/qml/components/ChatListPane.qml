import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami

Kirigami.Page {
    id: root

    signal chatSelected(string chatId)

    Layout.fillHeight: true
    Layout.minimumWidth: 0
    Layout.preferredWidth: Kirigami.Units.gridUnit * 20
    Layout.maximumWidth: Kirigami.Units.gridUnit * 22
    Kirigami.ColumnView.minimumWidth: 0
    Kirigami.ColumnView.preferredWidth: Kirigami.Units.gridUnit * 20
    Kirigami.ColumnView.maximumWidth: Kirigami.Units.gridUnit * 22
    title: i18nc("@title", "Chats")
    padding: 0
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    actions: [
        Kirigami.Action {
            icon.name: "view-refresh-symbolic"
            text: i18nc("@action:button", "Refresh")
            visible: AppController.bannerText.length > 0
            enabled: AppController.primaryActionEnabled
            onTriggered: AppController.triggerPrimaryAction()
        },
        Kirigami.Action {
            icon.name: "system-log-out-symbolic"
            text: i18nc("@action:button", "Log out")
            onTriggered: AppController.logout()
        }
    ]

    ColumnLayout {
        anchors.fill: parent
        spacing: 0

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
                ScrollBar.vertical: DiscreetScrollBar {}

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
                    onSelected: id => {
                        AppController.selectChat(id)
                        root.chatSelected(id)
                    }
                }

                BusyIndicator {
                    anchors.centerIn: parent
                    running: AppController.chatsLoading && AppController.chatsEmpty
                    visible: running
                }

                Kirigami.PlaceholderMessage {
                    anchors.centerIn: parent
                    width: Math.min(Math.max(0, parent.width - Kirigami.Units.largeSpacing * 4),
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
