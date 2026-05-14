import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami

Kirigami.Page {
    id: root

    Layout.fillWidth: true
    Layout.fillHeight: true
    title: AppController.hasSelectedChat
           ? AppController.selectedChatName
           : i18nc("@title", "Select a chat")
    padding: 0
    Kirigami.Theme.colorSet: Kirigami.Theme.Window

    function initialsForName(name) {
        const parts = name.trim().split(/\s+/)
        let initials = ""
        for (const part of parts) {
            if (part.length > 0) {
                initials += part.charAt(0).toUpperCase()
            }
            if (initials.length >= 2) {
                break
            }
        }
        return initials.length > 0 ? initials : "?"
    }

    titleDelegate: RowLayout {
        id: headerTitle

        readonly property bool hasPresenceText: AppController.hasSelectedChat
                                                && AppController.selectedChatPresenceText.length > 0
        readonly property real avatarSize: Kirigami.Units.gridUnit * 1.8
        readonly property real subtextPixelSize: Math.max(8, Math.round(Kirigami.Theme.smallFont.pixelSize * 0.82))

        Layout.fillWidth: true
        Layout.minimumWidth: 0
        implicitHeight: avatarSize
        spacing: Kirigami.Units.smallSpacing

        AvatarImage {
            visible: AppController.hasSelectedChat
            Layout.alignment: Qt.AlignVCenter
            Layout.preferredWidth: headerTitle.avatarSize
            Layout.preferredHeight: headerTitle.avatarSize
            avatarLocalPath: AppController.selectedChatAvatarLocalPath
            initials: root.initialsForName(AppController.selectedChatName)
        }

        Item {
            Layout.fillWidth: true
            Layout.minimumWidth: 0
            Layout.preferredWidth: Math.max(titleLabel.implicitWidth, subtextLabel.implicitWidth)
            Layout.alignment: Qt.AlignVCenter
            Layout.preferredHeight: headerTitle.avatarSize

            Label {
                id: titleLabel

                anchors.left: parent.left
                anchors.right: parent.right
                anchors.verticalCenter: parent.verticalCenter
                anchors.verticalCenterOffset: headerTitle.hasPresenceText
                                      ? -(subtextLabel.implicitHeight + Kirigami.Units.smallSpacing / 3) / 2
                                      : 0
                text: root.title
                elide: Text.ElideRight
                font.weight: Font.DemiBold
            }

            Label {
                id: subtextLabel

                anchors.left: parent.left
                anchors.right: parent.right
                anchors.top: titleLabel.bottom
                anchors.topMargin: Kirigami.Units.smallSpacing / 3
                visible: AppController.hasSelectedChat
                text: headerTitle.hasPresenceText ? AppController.selectedChatPresenceText : " "
                elide: Text.ElideRight
                opacity: headerTitle.hasPresenceText ? 1 : 0
                color: Kirigami.Theme.disabledTextColor
                font.family: Kirigami.Theme.smallFont.family
                font.pixelSize: headerTitle.subtextPixelSize
            }
        }
    }

    actions: [
        Kirigami.Action {
            icon.name: "documentinfo-symbolic"
            text: i18nc("@action:button", "Information")
            enabled: false
            visible: AppController.hasSelectedChat
        }
    ]

    ColumnLayout {
        anchors.fill: parent
        spacing: 0

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
            onComposingChanged: composing => AppController.setSelectedChatComposing(composing)
        }
    }
}
