pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Kirigami.Page {
    id: root

    property bool closeChatActionVisible: false

    readonly property bool messagesCurrent: Whatevr.AppController.hasSelectedChat
                                            && Whatevr.AppController.displayedMessagesChatId === Whatevr.AppController.selectedChatId
    readonly property bool waitingForMessages: Whatevr.AppController.hasSelectedChat
                                               && Whatevr.AppController.messagesLoading
                                               && (!root.messagesCurrent || Whatevr.AppController.messagesEmpty)

    signal closeChatRequested()

    Layout.fillWidth: true
    Layout.fillHeight: true
    title: Whatevr.AppController.hasSelectedChat
           ? Whatevr.AppController.selectedChatName
           : ""
    padding: 0
    focus: true
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

    function shouldTypeIntoComposer(event) {
        if (!Whatevr.AppController.hasSelectedChat || !composer.visible || !Whatevr.AppController.composerEnabled) {
            return false
        }
        if (event.modifiers & (Qt.ControlModifier | Qt.AltModifier | Qt.MetaModifier)) {
            return false
        }
        return event.text.length > 0 && event.text.charCodeAt(0) >= 0x20
    }

    function typeIntoComposer(text) {
        if (!Whatevr.AppController.hasSelectedChat || !composer.visible || !Whatevr.AppController.composerEnabled || text.length === 0) {
            return
        }

        messageView.clearMessageSelection()
        composer.focusAndInsertText(text)
    }

    Keys.onPressed: event => {
        if (!root.shouldTypeIntoComposer(event)) {
            return
        }

        root.typeIntoComposer(event.text)
        event.accepted = true
    }

    PointHandler {
        target: null
        acceptedButtons: Qt.LeftButton
        enabled: Whatevr.AppController.hasSelectedChat
        onActiveChanged: if (active) messageView.clearMessageSelection()
    }

    titleDelegate: RowLayout {
        id: headerTitle

        readonly property bool hasPresenceText: Whatevr.AppController.hasSelectedChat
                                                && Whatevr.AppController.selectedChatPresenceText.length > 0
        readonly property real avatarSize: Kirigami.Units.gridUnit * 1.8
        readonly property real subtextPixelSize: Math.max(8, Math.round(Kirigami.Theme.smallFont.pixelSize * 0.82))

        Layout.fillWidth: true
        Layout.minimumWidth: 0
        implicitHeight: avatarSize
        spacing: Kirigami.Units.smallSpacing

        TapHandler {
            onTapped: root.forceActiveFocus(Qt.MouseFocusReason)
        }

        AvatarImage {
            visible: Whatevr.AppController.hasSelectedChat
            Layout.alignment: Qt.AlignVCenter
            Layout.preferredWidth: headerTitle.avatarSize
            Layout.preferredHeight: headerTitle.avatarSize
            avatarLocalPath: Whatevr.AppController.selectedChatAvatarLocalPath
            initials: root.initialsForName(Whatevr.AppController.selectedChatName)
        }

        Item {
            Layout.fillWidth: true
            Layout.minimumWidth: 0
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
                visible: Whatevr.AppController.hasSelectedChat
                text: headerTitle.hasPresenceText ? Whatevr.AppController.selectedChatPresenceText : " "
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
            icon.name: "dialog-close-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Close Chat")
            visible: Whatevr.AppController.hasSelectedChat && root.closeChatActionVisible
            onTriggered: root.closeChatRequested()
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
                visible: Whatevr.AppController.hasSelectedChat
                         && root.messagesCurrent
                         && Whatevr.AppController.messageErrorText.length === 0
                         && !Whatevr.AppController.messagesEmpty
                chatId: Whatevr.AppController.selectedChatId
                model: Whatevr.AppController.messageListModel
                loadingOlderMessages: Whatevr.AppController.olderMessagesLoading
                canLoadOlderMessages: Whatevr.AppController.canLoadOlderMessages
                onLoadOlderMessagesRequested: Whatevr.AppController.loadOlderMessages()
                onConversationFocusRequested: root.forceActiveFocus(Qt.MouseFocusReason)
                onTypeIntoComposerRequested: text => root.typeIntoComposer(text)
            }

            BusyIndicator {
                anchors.centerIn: parent
                running: root.waitingForMessages
                visible: running
            }

            Kirigami.Action {
                id: retryMessagesAction

                text: Whatevr.I18n.i18nc("@action:button", "Retry")
                icon.name: "view-refresh-symbolic"
                onTriggered: Whatevr.AppController.retryMessages()
            }

            Kirigami.PlaceholderMessage {
                anchors.centerIn: parent
                width: Math.min(parent.width - Kirigami.Units.largeSpacing * 4,
                                Kirigami.Units.gridUnit * 22)
                visible: !root.waitingForMessages
                         && !messageView.visible
                text: !Whatevr.AppController.hasSelectedChat
                      ? Whatevr.I18n.i18nc("@info", "Select a chat")
                      : (Whatevr.AppController.messageErrorText.length > 0
                         ? Whatevr.I18n.i18nc("@info", "Messages could not be loaded")
                         : Whatevr.I18n.i18nc("@info", "No messages yet"))
                explanation: !Whatevr.AppController.hasSelectedChat
                             ? Whatevr.I18n.i18nc("@info", "Choose a conversation from the chat list to open it here.")
                             : (Whatevr.AppController.messageErrorText.length > 0
                                ? Whatevr.AppController.messageErrorText
                                : Whatevr.I18n.i18nc("@info", "Messages you send and receive will appear here."))

                helpfulAction: Whatevr.AppController.hasSelectedChat && Whatevr.AppController.messageErrorText.length > 0
                               ? retryMessagesAction
                               : null
            }
        }

        MessageComposer {
            id: composer

            Layout.fillWidth: true
            visible: Whatevr.AppController.hasSelectedChat
            enabledForChat: Whatevr.AppController.composerEnabled
            sending: Whatevr.AppController.sendInFlight
            errorText: Whatevr.AppController.composerErrorText
            onSendTextRequested: text => Whatevr.AppController.sendText(text)
            onSendImageRequested: (fileUrl, caption) => Whatevr.AppController.sendImage(fileUrl, caption)
            onComposingChanged: composing => Whatevr.AppController.setSelectedChatComposing(composing)
        }
    }

}
