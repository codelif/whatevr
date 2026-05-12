import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami

Frame {
    id: root

    Layout.fillWidth: true
    Layout.fillHeight: true
    padding: 0

    background: Rectangle {
        color: Kirigami.Theme.alternateBackgroundColor
    }

    contentItem: ColumnLayout {
        spacing: 0

        ToolBar {
            Layout.fillWidth: true

            contentItem: RowLayout {
                anchors.fill: parent
                anchors.leftMargin: Kirigami.Units.largeSpacing
                anchors.rightMargin: Kirigami.Units.smallSpacing
                spacing: Kirigami.Units.largeSpacing

                AvatarImage {
                    Layout.preferredWidth: Kirigami.Units.gridUnit * 2.1
                    Layout.preferredHeight: Layout.preferredWidth
                    visible: AppController.hasSelectedChat
                    avatarLocalPath: AppController.selectedChatAvatarLocalPath
                    initials: AppController.selectedChatName.length > 0
                              ? AppController.selectedChatName.slice(0, 1).toUpperCase()
                              : "?"
                    backgroundColor: Qt.alpha(foregroundColor, 0.16)
                }

                Label {
                    Layout.fillWidth: true
                    text: AppController.hasSelectedChat
                          ? AppController.selectedChatName
                          : i18nc("@title", "Select a chat")
                    font.weight: Font.DemiBold
                    elide: Text.ElideRight
                }

                ToolButton {
                    icon.name: "documentinfo-symbolic"
                    text: i18nc("@action:button", "Information")
                    display: AbstractButton.IconOnly
                    enabled: false
                    visible: AppController.hasSelectedChat
                }
            }
        }

        Rectangle {
            Layout.fillWidth: true
            implicitHeight: 1
            color: Qt.alpha(Kirigami.Theme.textColor, 0.10)
        }

        Item {
            id: timelineArea

            Layout.fillWidth: true
            Layout.fillHeight: true

            MessageView {
                id: messageView

                anchors.fill: parent
                anchors.margins: Kirigami.Units.smallSpacing
                visible: AppController.hasSelectedChat
                         && AppController.messageErrorText.length === 0
                         && !AppController.messagesEmpty
                model: AppController.messageListModel
                loadingOlderMessages: AppController.olderMessagesLoading
                canLoadOlderMessages: AppController.canLoadOlderMessages
                onLoadOlderMessagesRequested: AppController.loadOlderMessages()
            }

            BusyIndicator {
                anchors.centerIn: parent
                running: AppController.messagesLoading && AppController.messagesEmpty
                visible: running
            }

            Kirigami.Action {
                id: retryMessagesAction

                text: i18nc("@action:button", "Retry")
                icon.name: "view-refresh-symbolic"
                onTriggered: AppController.retryMessages()
            }

            Kirigami.PlaceholderMessage {
                anchors.centerIn: parent
                width: Math.min(parent.width - Kirigami.Units.largeSpacing * 4,
                                Kirigami.Units.gridUnit * 22)
                visible: !messageView.visible && !(AppController.messagesLoading && AppController.messagesEmpty)
                text: !AppController.hasSelectedChat
                      ? i18nc("@info", "Select a chat")
                      : (AppController.messageErrorText.length > 0
                         ? i18nc("@info", "Messages could not be loaded")
                         : i18nc("@info", "No messages yet"))
                explanation: !AppController.hasSelectedChat
                             ? i18nc("@info", "Choose a conversation from the chat list to open it here.")
                             : (AppController.messageErrorText.length > 0
                                ? AppController.messageErrorText
                                : i18nc("@info", "Messages you send and receive will appear here."))

                helpfulAction: AppController.hasSelectedChat && AppController.messageErrorText.length > 0
                               ? retryMessagesAction
                               : null
            }
        }

        MessageComposer {
            Layout.fillWidth: true
            visible: AppController.hasSelectedChat
            enabledForChat: AppController.composerEnabled
            sending: AppController.sendInFlight
            errorText: AppController.composerErrorText
            onSendTextRequested: text => AppController.sendText(text)
            onSendImageRequested: (fileUrl, caption) => AppController.sendImage(fileUrl, caption)
        }
    }
}
