pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Kirigami.Page {
    id: root

    // In the wide layout the close action is always offered for a selected
    // chat; in the single-column layout only while this page is the visible
    // one (the chat list page has its own actions).
    readonly property bool closeChatActionVisible: {
        const window = applicationWindow()
        if (!window || !Whatevr.AppController.hasSelectedChat) {
            return false
        }
        return !window.chatSingleColumnLayout || root.isCurrentPage
    }
    property string replyChatId: ""
    property string replyToMessageId: ""
    property string replyToSenderName: ""
    property string replyToText: ""
    property string replyToMediaKind: ""
    property string replyToMediaMimeType: ""
    property bool replyToOutgoing: false

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

    function clearReplyTarget() {
        replyChatId = ""
        replyToMessageId = ""
        replyToSenderName = ""
        replyToText = ""
        replyToMediaKind = ""
        replyToMediaMimeType = ""
        replyToOutgoing = false
    }

    function setReplyTarget(messageId, senderName, text, mediaKind, mediaMimeType, outgoing) {
        if (messageId.length === 0 || !Whatevr.AppController.hasSelectedChat) {
            return
        }
        replyChatId = Whatevr.AppController.selectedChatId
        replyToMessageId = messageId
        replyToSenderName = senderName
        replyToText = text
        replyToMediaKind = mediaKind
        replyToMediaMimeType = mediaMimeType
        replyToOutgoing = outgoing
        composer.forceInputFocus()
    }

    Keys.onPressed: event => {
        // ESC priority: pane > selection > reply > close chat. The picker is a
        // focused Popup that consumes ESC while open, so by the time ESC
        // reaches here the pane is already closed; leave selection mode, then
        // clear a pending reply, else close the chat.
        if (event.key === Qt.Key_Escape) {
            if (!Whatevr.AppController.hasSelectedChat) {
                return
            }
            if (messageView.selectionActive) {
                messageView.clearSelection()
            } else if (root.replyToMessageId.length > 0) {
                root.clearReplyTarget()
            } else {
                root.closeChatRequested()
            }
            event.accepted = true
            return
        }

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

    DragHandler {
        target: null
        acceptedButtons: Qt.LeftButton
        enabled: Whatevr.AppController.hasSelectedChat
    }

    Connections {
        target: Whatevr.AppController

        function onSelectionChanged() {
            if (root.replyChatId.length > 0 && root.replyChatId !== Whatevr.AppController.selectedChatId) {
                root.clearReplyTarget()
            }
        }
    }

    titleDelegate: RowLayout {
        id: headerTitle

        readonly property bool selectionActive: messageView.selectionActive
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

        // Selection mode swaps the chat identity for the running count.
        ToolButton {
            visible: headerTitle.selectionActive
            Layout.alignment: Qt.AlignVCenter
            icon.name: "dialog-close-symbolic"
            text: Whatevr.I18n.i18nc("@action:button leave message selection", "Cancel Selection")
            display: AbstractButton.IconOnly
            focusPolicy: Qt.NoFocus
            onClicked: messageView.clearSelection()
        }

        Label {
            visible: headerTitle.selectionActive
            Layout.fillWidth: true
            Layout.minimumWidth: 0
            text: Whatevr.I18n.i18ncp("@title number of selected messages", "%1 selected", "%1 selected", messageView.selectedCount)
            elide: Text.ElideRight
            font.weight: Font.DemiBold
        }

        AvatarImage {
            visible: !headerTitle.selectionActive && Whatevr.AppController.hasSelectedChat
            Layout.alignment: Qt.AlignVCenter
            Layout.preferredWidth: headerTitle.avatarSize
            Layout.preferredHeight: headerTitle.avatarSize
            avatarLocalPath: Whatevr.AppController.selectedChatAvatarLocalPath
            initials: root.initialsForName(Whatevr.AppController.selectedChatName)
        }

        Item {
            visible: !headerTitle.selectionActive
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

    // Selection mode swaps the toolbar to message actions. Frequent actions
    // stay visible; single-message and rarer ones live in the overflow menu.
    actions: messageView.selectionActive ? selectionActions : defaultActions

    property list<Kirigami.Action> defaultActions: [
        Kirigami.Action {
            icon.name: "dialog-close-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Close Chat")
            visible: Whatevr.AppController.hasSelectedChat && root.closeChatActionVisible
            onTriggered: root.closeChatRequested()
        }
    ]

    property list<Kirigami.Action> selectionActions: [
        Kirigami.Action {
            icon.name: "edit-copy-symbolic"
            text: Whatevr.I18n.i18nc("@action:button copy selected messages", "Copy")
            enabled: messageView.selectedCount > 0
                     && messageView.selectionRevision >= 0 && !messageView.selectionHasRevoked()
            onTriggered: messageView.copySelectedMessages(false)
        },
        Kirigami.Action {
            icon.name: "mail-forward-symbolic"
            text: Whatevr.I18n.i18nc("@action:button forward selected messages", "Forward")
            enabled: messageView.selectedCount > 0
                     && messageView.selectionRevision >= 0 && !messageView.selectionHasRevoked()
            onTriggered: messageView.openForwardPicker(messageView.selectedMessageIdList())
        },
        Kirigami.Action {
            icon.name: "edit-delete-symbolic"
            text: Whatevr.I18n.i18nc("@action:button delete selected messages locally", "Delete")
            enabled: messageView.selectedCount > 0
            onTriggered: messageView.confirmDeleteSelection(false)
        },
        Kirigami.Action {
            icon.name: "mail-replied-symbolic"
            text: Whatevr.I18n.i18nc("@action:button reply to the selected message", "Reply")
            displayHint: Kirigami.DisplayHint.AlwaysHide
            enabled: messageView.selectedCount === 1
                     && messageView.singleSelectedSnapshot
                     && !messageView.singleSelectedSnapshot.isRevoked
            onTriggered: {
                messageView.replyToSnapshot(messageView.singleSelectedSnapshot)
                messageView.clearSelection()
            }
        },
        Kirigami.Action {
            icon.name: "text-markdown-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Copy as Markdown")
            displayHint: Kirigami.DisplayHint.AlwaysHide
            enabled: messageView.selectedCount > 0
                     && messageView.selectionRevision >= 0 && !messageView.selectionHasRevoked()
            onTriggered: messageView.copySelectedMessages(true)
        },
        Kirigami.Action {
            icon.name: "documentinfo-symbolic"
            text: Whatevr.I18n.i18nc("@action:button delivery details", "Info")
            displayHint: Kirigami.DisplayHint.AlwaysHide
            enabled: messageView.selectedCount === 1
                     && messageView.singleSelectedSnapshot !== null
                     && Boolean(messageView.singleSelectedSnapshot.isOutgoing)
            onTriggered: messageView.openMessageInfo(String(messageView.singleSelectedSnapshot.messageId))
        },
        Kirigami.Action {
            icon.name: "edit-delete-remove-symbolic"
            text: Whatevr.I18n.i18nc("@action:button WhatsApp revoke of all selected", "Delete for Everyone")
            displayHint: Kirigami.DisplayHint.AlwaysHide
            enabled: messageView.selectionRevision >= 0 && messageView.canRevokeSelection()
            onTriggered: messageView.confirmDeleteSelection(true)
        },
        Kirigami.Action {
            icon.name: "edit-select-all-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Select All")
            displayHint: Kirigami.DisplayHint.AlwaysHide
            onTriggered: messageView.selectAllMessages()
        },
        Kirigami.Action {
            icon.name: "dialog-close-symbolic"
            text: Whatevr.I18n.i18nc("@action:button leave message selection", "Cancel")
            onTriggered: messageView.clearSelection()
        }
    ]

    ColumnLayout {
        anchors.fill: parent
        spacing: 0

        Item {
            id: timelineArea

            Layout.fillWidth: true
            Layout.fillHeight: true

            MouseArea {
                anchors.fill: parent
                acceptedButtons: Qt.LeftButton
                onPressed: mouse => {
                    root.forceActiveFocus(Qt.MouseFocusReason)
                    mouse.accepted = true
                }
            }

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
                onReplyToMessageRequested: (messageId, senderName, text, mediaKind, mediaMimeType, outgoing) => root.setReplyTarget(messageId, senderName, text, mediaKind, mediaMimeType, outgoing)
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
            replyToMessageId: root.replyToMessageId
            replyToSenderName: root.replyToSenderName
            replyToText: root.replyToText
            replyToMediaKind: root.replyToMediaKind
            replyToMediaMimeType: root.replyToMediaMimeType
            replyToOutgoing: root.replyToOutgoing
            onSendTextRequested: (text, replyToMessageId) => Whatevr.AppController.sendText(text, replyToMessageId)
            onSendImageRequested: (fileUrl, caption, replyToMessageId) => Whatevr.AppController.sendImage(fileUrl, caption, replyToMessageId)
            onComposingChanged: composing => Whatevr.AppController.setSelectedChatComposing(composing)
            onClearReplyRequested: root.clearReplyTarget()
            onReplyConsumed: root.clearReplyTarget()
        }
    }

}
