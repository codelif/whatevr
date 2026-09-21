pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr
import "Initials.js" as Initials
import "Wallpapers.js" as Wallpapers

Kirigami.Page {
    id: root

    // The transcript pane currently on screen.
    //
    // There is one pane per warm chat rather than one pane, because a re-open
    // is only free if the rows are still there, and handing a single ListView a
    // different model destroys every delegate it holds. So the panes are parked
    // and one of them is shown. This is what the selection actions, the media
    // viewer and the context menus below talk to, and because a parked pane is
    // never destroyed it is only ever null before the first one is built.
    property MessageView messageView: null

    // In the wide layout the close action is always offered for a selected
    // chat; in the single-column layout only while this page is the visible
    // one (the chat list page has its own actions).
    readonly property bool closeChatActionVisible: {
        const window = applicationWindow()
        if (!window || !Whatevr.ProtocolController.hasSelectedChat) {
            return false
        }
        return !window.chatSingleColumnLayout || root.isCurrentPage
    }
    // The chat whose draft is currently loaded in the composer. Tracked so a
    // chat switch/close can stash the outgoing chat's text and restore the
    // incoming chat's draft (the composer otherwise keeps its text across chats).
    property string composerChatId: ""
    property string replyChatId: ""
    property string replyToMessageId: ""
    property string replyToSenderName: ""
    property string replyToText: ""
    property string replyToMediaKind: ""
    property string replyToMediaMimeType: ""
    property bool replyToOutgoing: false
    // Edit-in-place state. editingMessageId being non-empty puts the composer in
    // edit mode (prefilled body, "Editing" banner). Reply and edit are mutually
    // exclusive.
    property string editChatId: ""
    property string editingMessageId: ""
    property string editingOriginalText: ""

    // Whether the rows on screen belong to the chat on screen. A same-chat
    // re-subscribe (a jump, go-to-bottom from mid-history, a daemon-side reset)
    // empties the model for one round trip; that is a reload of this
    // conversation, not the absence of one, so the pane keeps its chrome
    // instead of collapsing to the placeholder and rebuilding itself.
    readonly property bool messagesCurrent: Whatevr.ProtocolController.hasSelectedChat
                                             && (Whatevr.ProtocolController.displayedMessagesChatId === Whatevr.ProtocolController.selectedChatId
                                                 || Whatevr.ProtocolController.messagesReloading)
    readonly property bool waitingForMessages: Whatevr.ProtocolController.hasSelectedChat
                                               && (Whatevr.ProtocolController.messagesLoading
                                                   || Whatevr.ProtocolController.unreadAnchorResolving
                                                   || !root.pinnedLayoutReady)
                                               && (!root.messagesCurrent
                                                   || Whatevr.ProtocolController.messagesEmpty
                                                   || Whatevr.ProtocolController.unreadAnchorResolving
                                                   || !root.pinnedLayoutReady)
    property string pinnedLayoutChatId: ""
    property bool pinnedLayoutSettled: false
    readonly property bool pinnedSlotReserved: Whatevr.ProtocolController.hasSelectedChat
                                               && root.messagesCurrent
                                               && (!Whatevr.ProtocolController.pinnedMessagesReady
                                                   || pinnedBanner.count > 0)
    readonly property bool pinnedLayoutReady: !Whatevr.ProtocolController.hasSelectedChat
                                               || (root.pinnedLayoutChatId === Whatevr.ProtocolController.selectedChatId
                                                  && Whatevr.ProtocolController.pinnedMessagesReady
                                                  && root.pinnedLayoutSettled)

    onMessagesCurrentChanged: requestPinnedLayoutSettle()
    onPinnedSlotReservedChanged: requestPinnedLayoutSettle()

    function beginPinnedLayoutSettle() {
        root.pinnedLayoutChatId = Whatevr.ProtocolController.selectedChatId
        root.pinnedLayoutSettled = false
        pinnedLayoutSettleTimer.stop()
        if (Whatevr.ProtocolController.hasSelectedChat
                && root.messagesCurrent
                && Whatevr.ProtocolController.pinnedMessagesReady) {
            pinnedLayoutSettleTimer.start()
        }
    }

    function requestPinnedLayoutSettle() {
        if (!Whatevr.ProtocolController.hasSelectedChat) {
            root.pinnedLayoutChatId = ""
            root.pinnedLayoutSettled = true
            pinnedLayoutSettleTimer.stop()
            return
        }

        if (root.pinnedLayoutChatId !== Whatevr.ProtocolController.selectedChatId) {
            root.beginPinnedLayoutSettle()
            return
        }

        if (!root.pinnedLayoutSettled
                && root.messagesCurrent
                && Whatevr.ProtocolController.pinnedMessagesReady
                && !pinnedLayoutSettleTimer.running) {
            pinnedLayoutSettleTimer.start()
        }
    }

    signal closeChatRequested()

    Layout.fillWidth: true
    Layout.fillHeight: true
    title: Whatevr.ProtocolController.hasSelectedChat
           ? Whatevr.ProtocolController.selectedChatName
           : ""
    padding: 0
    focus: true
    Kirigami.Theme.colorSet: Kirigami.Theme.Window

    // Opens the contact/group info page for the currently selected chat. Group
    // JIDs end with "@g.us"; everything else is a 1:1 user.
    function openChatInfo() {
        const chatId = Whatevr.ProtocolController.selectedChatId
        if (chatId.length === 0) {
            return
        }
        if (chatId.endsWith("@g.us")) {
            contactInfoDialog.openFor({ isGroup: true, targetChatId: chatId })
        } else {
            contactInfoDialog.openFor({ isGroup: false, targetJid: chatId })
        }
    }

    function shouldTypeIntoComposer(event) {
        if (!Whatevr.ProtocolController.hasSelectedChat || !composer.visible || !Whatevr.ProtocolController.composerEnabled) {
            return false
        }
        if (event.modifiers & (Qt.ControlModifier | Qt.AltModifier | Qt.MetaModifier)) {
            return false
        }
        return event.text.length > 0 && event.text.charCodeAt(0) >= 0x20
    }

    function typeIntoComposer(text) {
        if (!Whatevr.ProtocolController.hasSelectedChat || !composer.visible || !Whatevr.ProtocolController.composerEnabled || text.length === 0) {
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
        if (messageId.length === 0 || !Whatevr.ProtocolController.hasSelectedChat) {
            return
        }
        // Reply and edit are mutually exclusive; starting a reply cancels an edit.
        root.clearEditTarget()
        replyChatId = Whatevr.ProtocolController.selectedChatId
        replyToMessageId = messageId
        replyToSenderName = senderName
        replyToText = text
        replyToMediaKind = mediaKind
        replyToMediaMimeType = mediaMimeType
        replyToOutgoing = outgoing
        composer.forceInputFocus()
    }

    function clearEditTarget() {
        editChatId = ""
        editingMessageId = ""
        editingOriginalText = ""
    }

    function setEditTarget(messageId, text) {
        if (messageId.length === 0 || !Whatevr.ProtocolController.hasSelectedChat) {
            return
        }
        // Reply and edit are mutually exclusive; starting an edit cancels a reply.
        root.clearReplyTarget()
        editChatId = Whatevr.ProtocolController.selectedChatId
        // Set the body before the id: the composer prefills on editingMessageId
        // becoming non-empty and reads editingOriginalText then.
        editingOriginalText = text
        editingMessageId = messageId
        composer.forceInputFocus()
    }

    Keys.onPressed: event => {
        // ESC priority: pane > selection > reply > close chat. Convention:
        // every escape-closable popup sets `focus: true` so it consumes ESC at
        // the popup layer and this close-chat fallback never fires while one is
        // open (the picker is such a focused Popup). Any new popup that closes
        // on ESC MUST set `focus: true` or it will leak ESC here and close the
        // chat. By the time ESC reaches here no popup was open: leave selection
        // mode, then clear a pending reply, else close the chat.
        if (event.key === Qt.Key_Escape) {
            if (!Whatevr.ProtocolController.hasSelectedChat) {
                return
            }
            if (messageView.selectionActive) {
                messageView.clearSelection()
            } else if (root.editingMessageId.length > 0) {
                root.clearEditTarget()
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
        enabled: Whatevr.ProtocolController.hasSelectedChat
        onActiveChanged: if (active) messageView.clearMessageSelection()
    }

    DragHandler {
        target: null
        acceptedButtons: Qt.LeftButton
        enabled: Whatevr.ProtocolController.hasSelectedChat
    }

    Connections {
        target: Whatevr.ProtocolController

        function onSelectionChanged() {
            const newChatId = Whatevr.ProtocolController.selectedChatId
            if (newChatId !== root.pinnedLayoutChatId) {
                root.beginPinnedLayoutSettle()
            }
            // selectionChanged also fires for presence/avatar updates of the same
            // chat; only react when the open chat actually changed.
            if (newChatId !== root.composerChatId) {
                // Cancel an in-progress edit first: it empties the composer, so
                // stashing the old draft and loading the new one below operate on
                // real draft text rather than the edit body (and the edit-exit
                // doesn't clobber the incoming chat's freshly loaded draft).
                if (root.editingMessageId.length > 0) {
                    root.clearEditTarget()
                }
                // Stash the previous chat's composer text as its draft, then load
                // the new chat's draft. setChatDraft ignores empty ids/text.
                Whatevr.ProtocolController.setChatDraft(root.composerChatId, composer.inputPlainText())
                composer.setText(Whatevr.ProtocolController.chatDraft(newChatId))
                root.composerChatId = newChatId
                // Pull keyboard focus into the conversation when a chat opens so
                // its key handler is live immediately — Escape closes the chat and
                // typing routes into the composer without needing a click first.
                // (Only on a real chat change, never on presence/avatar refreshes,
                // so focus is not yanked away mid-interaction.)
                if (newChatId.length > 0) {
                    root.forceActiveFocus(Qt.OtherFocusReason)
                }
            }
            if (root.replyChatId.length > 0 && root.replyChatId !== newChatId) {
                root.clearReplyTarget()
            }
            if (root.editChatId.length > 0 && root.editChatId !== newChatId) {
                root.clearEditTarget()
            }
        }
    }

    Connections {
        target: Whatevr.ProtocolController

        function onPinnedMessagesChanged() {
            if (Whatevr.ProtocolController.pinnedMessagesReady) {
                root.requestPinnedLayoutSettle()
            } else if (root.pinnedLayoutChatId !== Whatevr.ProtocolController.selectedChatId
                       || !root.pinnedLayoutSettled) {
                root.pinnedLayoutChatId = Whatevr.ProtocolController.selectedChatId
                root.pinnedLayoutSettled = false
                pinnedLayoutSettleTimer.stop()
            }
        }
    }

    Timer {
        id: pinnedLayoutSettleTimer

        interval: 0
        onTriggered: root.pinnedLayoutSettled = Whatevr.ProtocolController.hasSelectedChat
                                                && root.messagesCurrent
                                                && Whatevr.ProtocolController.pinnedMessagesReady
                                                 && root.pinnedLayoutChatId === Whatevr.ProtocolController.selectedChatId
    }

    titleDelegate: RowLayout {
        id: headerTitle

        readonly property bool selectionActive: messageView.selectionActive
        readonly property bool hasPresenceText: Whatevr.ProtocolController.hasSelectedChat
                                                && Whatevr.ProtocolController.selectedChatPresenceText.length > 0
        readonly property real avatarSize: Kirigami.Units.gridUnit * 1.8
        readonly property real subtextPixelSize: Math.max(8, Math.round(Kirigami.Theme.smallFont.pixelSize * 0.82))

        visible: false
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
            visible: !headerTitle.selectionActive && Whatevr.ProtocolController.hasSelectedChat
            Layout.alignment: Qt.AlignVCenter
            Layout.preferredWidth: headerTitle.avatarSize
            Layout.preferredHeight: headerTitle.avatarSize
            avatarLocalPath: Whatevr.ProtocolController.selectedChatAvatarLocalPath
            initials: Initials.firstTwo(Whatevr.ProtocolController.selectedChatName)

             TapHandler {
                 enabled: Whatevr.ProtocolController.hasSelectedChat
                onTapped: Whatevr.ProtocolController.viewProfilePicture(
                              Whatevr.ProtocolController.selectedChatId)
             }
        }

        ToolButton {
            visible: !headerTitle.selectionActive && Whatevr.ProtocolController.hasSelectedChat
            Layout.alignment: Qt.AlignVCenter
            icon.name: "view-more-symbolic"
            text: Whatevr.I18n.i18nc("@action:button chat header menu", "Chat menu")
            display: AbstractButton.IconOnly
            onClicked: chatHeaderMenu.open()
        }

        Menu {
            id: chatHeaderMenu

            MenuItem {
                text: Whatevr.I18n.i18nc("@action:menu chat info", "Chat info")
                icon.name: "dialog-information-symbolic"
                onTriggered: root.openChatInfo()
            }
            MenuItem {
                text: Whatevr.I18n.i18nc("@action:menu chat media", "Media, links and documents")
                icon.name: "folder-pictures-symbolic"
                onTriggered: applicationWindow().pageStack.layers.push(
                    Qt.resolvedUrl("ChatMediaGalleryPage.qml"), {
                        chatId: Whatevr.ProtocolController.selectedChatId,
                        chatName: Whatevr.ProtocolController.selectedChatName
                    })
            }
            MenuItem {
                text: Whatevr.I18n.i18nc("@action:menu starred chat messages", "Starred messages")
                icon.name: "starred-symbolic"
                onTriggered: applicationWindow().openWorkspace("starred")
            }
            MenuItem {
                text: Whatevr.I18n.i18nc("@action:menu export chat", "Export chat…")
                icon.name: "document-save-symbolic"
                onTriggered: messageView.exportChatDialog.openFor(
                    Whatevr.ProtocolController.selectedChatId,
                    Whatevr.ProtocolController.selectedChatName)
            }
            MenuSeparator {}
            MenuItem {
                text: Whatevr.I18n.i18nc("@action:menu close chat", "Close chat")
                icon.name: "dialog-close-symbolic"
                onTriggered: root.closeChatRequested()
            }
        }

        Item {
            visible: !headerTitle.selectionActive
            Layout.fillWidth: true
            Layout.minimumWidth: 0
            Layout.alignment: Qt.AlignVCenter
            Layout.preferredHeight: headerTitle.avatarSize

            TapHandler {
                enabled: Whatevr.ProtocolController.hasSelectedChat
                onTapped: root.openChatInfo()
            }

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
                visible: Whatevr.ProtocolController.hasSelectedChat
                text: headerTitle.hasPresenceText ? Whatevr.ProtocolController.selectedChatPresenceText : " "
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
            icon.name: "view-more-symbolic"
            text: Whatevr.I18n.i18nc("@action:button chat header menu", "Chat menu")
            displayHint: Kirigami.DisplayHint.IconOnly
            visible: Whatevr.ProtocolController.hasSelectedChat
            onTriggered: chatHeaderMenu.open()
        },
        Kirigami.Action {
            icon.name: "search-symbolic"
            text: Whatevr.I18n.i18nc("@action:button search within this chat", "Search")
            displayHint: Kirigami.DisplayHint.IconOnly
            visible: Whatevr.ProtocolController.hasSelectedChat
            checkable: true
            checked: Whatevr.ProtocolController.chatSearchActive
            onTriggered: {
                if (Whatevr.ProtocolController.chatSearchActive) {
                    Whatevr.ProtocolController.closeChatSearch()
                } else {
                    Whatevr.ProtocolController.openChatSearch()
                }
            }
        },
        Kirigami.Action {
            icon.name: "starred-symbolic"
            text: Whatevr.I18n.i18nc("@action:button starred messages in this chat", "Starred messages")
            displayHint: Kirigami.DisplayHint.AlwaysHide
            visible: Whatevr.ProtocolController.hasSelectedChat
            onTriggered: {
                applicationWindow().pageStack.layers.push(Qt.resolvedUrl("StarredMessagesPage.qml"), {
                    chatId: Whatevr.ProtocolController.selectedChatId,
                    headerTitle: Whatevr.I18n.i18nc("@title starred messages in one chat",
                                                    "Starred in %1", Whatevr.ProtocolController.selectedChatName)
                })
            }
        },
        Kirigami.Action {
            icon.name: "document-save-symbolic"
            text: Whatevr.I18n.i18nc("@action:button export this chat to a text file", "Export chat…")
            displayHint: Kirigami.DisplayHint.AlwaysHide
            visible: Whatevr.ProtocolController.hasSelectedChat
            onTriggered: messageView.exportChatDialog.openFor(
                Whatevr.ProtocolController.selectedChatId,
                Whatevr.ProtocolController.selectedChatName)
        },
        Kirigami.Action {
            icon.name: "dialog-close-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Close Chat")
            displayHint: Kirigami.DisplayHint.AlwaysHide
            visible: Whatevr.ProtocolController.hasSelectedChat && root.closeChatActionVisible
            onTriggered: root.closeChatRequested()
        }
    ]

    property list<Kirigami.Action> selectionActions: [
        Kirigami.Action {
            icon.name: "edit-copy-symbolic"
            text: Whatevr.I18n.i18nc("@action:button copy selected messages", "Copy")
            displayHint: Kirigami.DisplayHint.KeepVisible
            enabled: messageView.selectedCount > 0
                     && messageView.selectionRevision >= 0 && !messageView.selectionHasRevoked()
            onTriggered: messageView.copySelectedMessages(false)
        },
        Kirigami.Action {
            icon.name: "mail-forward-symbolic"
            text: Whatevr.I18n.i18nc("@action:button forward selected messages", "Forward…")
            displayHint: Kirigami.DisplayHint.KeepVisible
            enabled: messageView.selectedCount > 0
                     && messageView.selectionRevision >= 0 && !messageView.selectionHasRevoked()
            onTriggered: messageView.openForwardPicker(messageView.selectedMessageIdList())
        },
        Kirigami.Action {
            icon.name: "mail-replied-symbolic"
            text: Whatevr.I18n.i18nc("@action:button reply to the selected message", "Reply")
            displayHint: Kirigami.DisplayHint.AlwaysHide
            enabled: messageView.selectedCount === 1
                     && messageView.singleSelectedSnapshot
                     && messageView.canReplyToSnapshot(messageView.singleSelectedSnapshot)
            onTriggered: {
                messageView.replyToSnapshot(messageView.singleSelectedSnapshot)
                messageView.clearSelection()
            }
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
            separator: true
            displayHint: Kirigami.DisplayHint.AlwaysHide
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
            separator: true
            displayHint: Kirigami.DisplayHint.AlwaysHide
        },
        Kirigami.Action {
            icon.name: "edit-delete-symbolic"
            text: Whatevr.I18n.i18nc("@action:button delete selected messages locally", "Delete for Me…")
            displayHint: Kirigami.DisplayHint.AlwaysHide
            enabled: messageView.selectedCount > 0
            onTriggered: messageView.confirmDeleteSelection(false)
        },
        Kirigami.Action {
            icon.name: "edit-delete-remove-symbolic"
            text: Whatevr.I18n.i18nc("@action:button WhatsApp revoke of all selected", "Delete for Everyone…")
            displayHint: Kirigami.DisplayHint.AlwaysHide
            enabled: messageView.selectionRevision >= 0 && messageView.canRevokeSelection()
            onTriggered: messageView.confirmDeleteSelection(true)
        },
        Kirigami.Action {
            separator: true
            displayHint: Kirigami.DisplayHint.AlwaysHide
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

    // The focused in-chat search match: MessageView owns the scroll + reply
    // glow, so the jump is driven from here whenever the controller changes
    // which match is active (search start, next, previous).
    property string lastChatSearchJumpId: ""

    Connections {
        target: Whatevr.ProtocolController
        function onChatSearchChanged() {
            const id = Whatevr.ProtocolController.chatSearchActiveMessageId
            if (id.length === 0) {
                root.lastChatSearchJumpId = ""
            } else if (id !== root.lastChatSearchJumpId) {
                root.lastChatSearchJumpId = id
                messageView.jumpToReplyTarget(id)
            }
        }
    }

    ColumnLayout {
        anchors.fill: parent
        spacing: 0

        // Explicit in-page header. Kirigami's titleDelegate is not consistently
        // rendered when this pane lives inside WorkspacePane's StackLayout.
        // Keep the chat identity/actions in normal layout flow so they cannot
        // disappear when switching between workspace tabs.
        RowLayout {
            Layout.fillWidth: true
            Layout.preferredHeight: Kirigami.Units.gridUnit * 3.2
            Layout.leftMargin: Kirigami.Units.largeSpacing
            Layout.rightMargin: Kirigami.Units.smallSpacing
            spacing: Kirigami.Units.smallSpacing
            visible: Whatevr.ProtocolController.hasSelectedChat

            AvatarImage {
                Layout.preferredWidth: Kirigami.Units.gridUnit * 2.1
                Layout.preferredHeight: Kirigami.Units.gridUnit * 2.1
                avatarLocalPath: Whatevr.ProtocolController.selectedChatAvatarLocalPath
                initials: Initials.firstTwo(Whatevr.ProtocolController.selectedChatName)
                TapHandler { onTapped: root.openChatInfo() }
            }

            ColumnLayout {
                Layout.fillWidth: true
                spacing: 0
                Label {
                    Layout.fillWidth: true
                    text: Whatevr.ProtocolController.selectedChatName
                    elide: Text.ElideRight
                    font.weight: Font.DemiBold
                }
                Label {
                    Layout.fillWidth: true
                    text: Whatevr.ProtocolController.selectedChatPresenceText
                    visible: text.length > 0
                    color: Kirigami.Theme.disabledTextColor
                    font: Kirigami.Theme.smallFont
                    elide: Text.ElideRight
                }
            }

            ToolButton {
                icon.name: "search-symbolic"
                display: AbstractButton.IconOnly
                text: Whatevr.I18n.i18nc("@action:button search this chat", "Search in chat")
                onClicked: {
                    if (Whatevr.ProtocolController.chatSearchActive)
                        Whatevr.ProtocolController.closeChatSearch()
                    else
                        Whatevr.ProtocolController.openChatSearch()
                }
            }

            ToolButton {
                id: chatMenuButton

                icon.name: "view-more-symbolic"
                display: AbstractButton.IconOnly
                text: Whatevr.I18n.i18nc("@action:button chat header menu", "Chat menu")
                // Anchor the popup under the button, right-aligned, instead
                // of open() which drops it at a default (left) position.
                onClicked: {
                    const pos = chatMenuButton.mapToItem(explicitChatHeaderMenu.parent,
                                                         0, chatMenuButton.height)
                    explicitChatHeaderMenu.x = pos.x + chatMenuButton.width
                        - explicitChatHeaderMenu.implicitWidth
                    explicitChatHeaderMenu.y = pos.y
                    explicitChatHeaderMenu.open()
                }
            }

            Menu {
                id: explicitChatHeaderMenu

                MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu chat info", "Chat info")
                    icon.name: "dialog-information-symbolic"
                    onTriggered: root.openChatInfo()
                }
                MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu chat media", "Media, links and documents")
                    icon.name: "folder-pictures-symbolic"
                    onTriggered: applicationWindow().pageStack.layers.push(
                        Qt.resolvedUrl("ChatMediaGalleryPage.qml"), {
                            chatId: Whatevr.ProtocolController.selectedChatId,
                            chatName: Whatevr.ProtocolController.selectedChatName
                        })
                }
                MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu starred chat messages", "Starred messages")
                    icon.name: "starred-symbolic"
                    onTriggered: applicationWindow().openWorkspace("starred")
                }
                MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu scheduled messages", "Scheduled messages")
                    icon.name: "appointment-new-symbolic"
                    onTriggered: applicationWindow().pageStack.layers.push(
                        Qt.resolvedUrl("ScheduledMessagesPage.qml"), {
                            chatId: Whatevr.ProtocolController.selectedChatId,
                            chatName: Whatevr.ProtocolController.selectedChatName
                        })
                }
                MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu export chat", "Export chat…")
                    icon.name: "document-save-symbolic"
                    onTriggered: messageView.exportChatDialog.openFor(
                        Whatevr.ProtocolController.selectedChatId,
                        Whatevr.ProtocolController.selectedChatName)
                }
                MenuSeparator {}
                MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu close chat", "Close chat")
                    icon.name: "dialog-close-symbolic"
                    onTriggered: root.closeChatRequested()
                }
            }
        }

        // In-chat search strip: matches navigation with a live n/m counter.
        // Driven entirely by the protocol controller's chat-search state.
        Control {
            id: chatSearchBar

            Layout.fillWidth: true
            visible: Whatevr.ProtocolController.chatSearchActive
            padding: Kirigami.Units.smallSpacing
            Kirigami.Theme.colorSet: Kirigami.Theme.Window
            Kirigami.Theme.inherit: false

            background: Rectangle {
                color: Kirigami.Theme.backgroundColor
                Kirigami.Separator {
                    anchors.left: parent.left
                    anchors.right: parent.right
                    anchors.bottom: parent.bottom
                }
            }

            onVisibleChanged: {
                if (visible) {
                    chatSearchInput.forceActiveFocus()
                } else {
                    chatSearchInput.text = ""
                }
            }

            contentItem: RowLayout {
                spacing: Kirigami.Units.smallSpacing

                Kirigami.SearchField {
                    id: chatSearchInput

                    Layout.fillWidth: true
                    placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Search in this conversation")
                    onTextChanged: Whatevr.ProtocolController.setChatSearchQuery(text)
                    Keys.onReturnPressed: Whatevr.ProtocolController.chatSearchNext()
                    Keys.onEnterPressed: Whatevr.ProtocolController.chatSearchNext()
                    Keys.onEscapePressed: Whatevr.ProtocolController.closeChatSearch()
                }

                Label {
                    visible: Whatevr.ProtocolController.chatSearchQuery.length > 0
                    text: Whatevr.ProtocolController.chatSearchMatchCount > 0
                          ? Whatevr.I18n.i18nc("@info:status search match position, e.g. 2 of 9",
                                               "%1 of %2",
                                               Whatevr.ProtocolController.chatSearchCurrentIndex,
                                               Whatevr.ProtocolController.chatSearchMatchCount)
                          : Whatevr.I18n.i18nc("@info:status no search matches", "No matches")
                    color: Kirigami.Theme.disabledTextColor
                    font: Kirigami.Theme.smallFont
                }

                ToolButton {
                    icon.name: "go-up-symbolic"
                    enabled: Whatevr.ProtocolController.chatSearchMatchCount > 0
                    display: AbstractButton.IconOnly
                    text: Whatevr.I18n.i18nc("@action:button newer search match", "Previous match")
                    ToolTip.visible: hovered
                    ToolTip.text: text
                    onClicked: Whatevr.ProtocolController.chatSearchPrevious()
                }

                ToolButton {
                    icon.name: "go-down-symbolic"
                    enabled: Whatevr.ProtocolController.chatSearchMatchCount > 0
                    display: AbstractButton.IconOnly
                    text: Whatevr.I18n.i18nc("@action:button older search match", "Next match")
                    ToolTip.visible: hovered
                    ToolTip.text: text
                    onClicked: Whatevr.ProtocolController.chatSearchNext()
                }

                ToolButton {
                    icon.name: "dialog-close-symbolic"
                    display: AbstractButton.IconOnly
                    text: Whatevr.I18n.i18nc("@action:button close in-chat search", "Close search")
                    ToolTip.visible: hovered
                    ToolTip.text: text
                    onClicked: Whatevr.ProtocolController.closeChatSearch()
                }
            }
        }

        Item {
            id: pinnedBannerSlot

            Layout.fillWidth: true
            Layout.preferredHeight: root.pinnedSlotReserved
                                    ? pinnedBanner.implicitHeight
                                    : 0
            clip: true

            PinnedMessagesBanner {
                id: pinnedBanner

                anchors.fill: parent
                visible: Whatevr.ProtocolController.hasSelectedChat
                         && Whatevr.ProtocolController.pinnedMessagesReady
                         && root.messagesCurrent
                         && count > 0
                onMessageActivated: messageId => messageView.jumpToReplyTarget(messageId)
            }
        }

        // A live share is the one thing in a chat that keeps happening while
        // its bubble is scrolled away, so it gets a strip of its own. It
        // collapses to nothing the moment the last share ends.
        Item {
            id: liveLocationBannerSlot

            Layout.fillWidth: true
            Layout.preferredHeight: liveLocationBanner.visible ? liveLocationBanner.implicitHeight : 0
            clip: true

            LiveLocationBanner {
                id: liveLocationBanner

                anchors.fill: parent
                visible: Whatevr.ProtocolController.hasSelectedChat
                         && root.messagesCurrent
                         && count > 0
                onMessageActivated: messageId => messageView.jumpToReplyTarget(messageId)
            }
        }

        Item {
            id: timelineArea

            Layout.fillWidth: true
            Layout.fillHeight: true

            // Conversation wallpaper lives outside MessageView so it stays visible
            // while messages are loading, empty, or switching between chats.
            Rectangle {
                id: wallpaperBackground

                anchors.fill: parent
                readonly property string wallpaperColor: Wallpapers.colorFor(Whatevr.Settings.chatWallpaper)
                color: wallpaperColor.length > 0 ? wallpaperColor : Kirigami.Theme.backgroundColor
            }

            // Optional doodle pattern layered over the background colour. Hidden
            // when the pattern is "None" or its source resolves to empty.
            ChatWallpaper {
                anchors.fill: parent
                backgroundColor: wallpaperBackground.color
                originX: timelineArea.x
                originY: timelineArea.y
                // Painted only for a real conversation, but the motif is handed
                // over unconditionally so a large custom SVG rasterises while the
                // pane is still empty. Gating the source on hasSelectedChat meant
                // the decode did not start until the first chat opened, which is
                // exactly when the frame budget is tightest.
                active: Whatevr.ProtocolController.hasSelectedChat
                source: {
                    switch (Whatevr.Settings.chatWallpaperPattern) {
                    case "doodle": return "qrc:/data/wallpapers/doodle.svg";
                    case "custom": return Whatevr.Settings.chatWallpaperPath.length > 0
                        ? Whatevr.ProtocolController.localFileUrl(Whatevr.Settings.chatWallpaperPath)
                        : "";
                    default: return "";
                    }
                }
                scalePercent: Whatevr.Settings.chatWallpaperScale
                intensity: Whatevr.Settings.chatWallpaperOpacity / 100
                tint: Whatevr.Settings.chatWallpaperTint
            }

            MouseArea {
                anchors.fill: parent
                acceptedButtons: Qt.LeftButton
                onPressed: mouse => {
                    root.forceActiveFocus(Qt.MouseFocusReason)
                    mouse.accepted = true
                }
            }

            // One transcript pane per warm chat, and the reason they are a
            // Repeater over a *constant* count rather than over the pool
            // itself: a model that changed length or order would have the
            // Repeater rebuild its delegates, and rebuilding the delegates is
            // the exact cost this pool exists to avoid. The count never moves,
            // a chat keeps its slot until it is evicted, and switching between
            // two warm chats therefore changes nothing but which pane is
            // visible.
            Repeater {
                id: transcriptPanes

                model: Whatevr.ProtocolController.warmWindowCount

                delegate: MessageView {
                    id: pane

                    required property int index

                    // Re-read whenever a slot changes hands. For a switch
                    // between two chats that are both already warm, nothing in
                    // here changes at all.
                    readonly property var slot: Whatevr.ProtocolController.warmWindows[index]
                    readonly property string slotChatId: slot ? String(slot.chatId ?? "") : ""
                    // The session that owns this slot. Every piece of transcript
                    // state below is read off it rather than off the controller,
                    // so a parked pane keeps showing its own chat's state.
                    readonly property var slotSession: slot ? slot.session : null
                    // The controller names the one window it is driving. Chat id
                    // alone is not enough: a chat jumped into keeps both its
                    // live-edge window and its anchored one warm, and comparing
                    // ids made both panes current at once.
                    readonly property bool isCurrent: slotChatId.length > 0
                                                      && slotChatId === Whatevr.ProtocolController.selectedChatId
                                                      && (slot ? slot.active === true : false)

                    // The pane the rest of this file talks to. Panes are never
                    // destroyed, so once one has claimed this it is never null
                    // again; with no chat selected it stays whichever pane was
                    // last on screen, which is what the selection actions want
                    // to keep reading.
                    onIsCurrentChanged: if (isCurrent) root.messageView = pane
                    Component.onCompleted: if (!root.messageView) root.messageView = pane

                    anchors.fill: parent
                    anchors.margins: Kirigami.Units.smallSpacing
                    visible: isCurrent
                             && root.pinnedLayoutReady
                             && root.messagesCurrent
                             && !!slotSession
                             && !slotSession.unreadAnchorResolving
                             && slotSession.messageErrorText.length === 0
                             && (!slotSession.messagesEmpty || slotSession.messagesReloading)
                    chatId: slotChatId
                    isCurrentPane: isCurrent
                    session: slotSession
                    model: slot ? slot.model : null
                    // Read off this pane's own session, so a parked pane shows
                    // its chat's state rather than a quiescent stand-in.
                    loadingMessages: !!slotSession && slotSession.messagesLoading
                    loadingOlderMessages: !!slotSession && slotSession.olderMessagesLoading
                    loadingNewerMessages: !!slotSession && slotSession.newerMessagesLoading
                    canLoadOlderMessages: !!slotSession && slotSession.canLoadOlderMessages
                    canLoadNewerMessages: !!slotSession && slotSession.canLoadNewerMessages
                    olderMessagesFailed: !!slotSession && slotSession.olderMessagesFailed
                    newerMessagesFailed: !!slotSession && slotSession.newerMessagesFailed
                    messagesAtLiveEdge: !slotSession || slotSession.messagesAtLiveEdge
                    historyExhausted: isCurrent && Whatevr.ProtocolController.selectedChatHistoryExhausted
                    phoneHistoryRequesting: !!slotSession && slotSession.phoneHistoryRequesting
                    onLoadOlderMessagesRequested: Whatevr.ProtocolController.loadOlderMessages()
                    onLoadNewerMessagesRequested: Whatevr.ProtocolController.loadNewerMessages()
                    onLoadPhoneHistoryRequested: Whatevr.ProtocolController.requestOlderMessagesFromPhone()
                    onConversationFocusRequested: root.forceActiveFocus(Qt.MouseFocusReason)
                    onTypeIntoComposerRequested: text => root.typeIntoComposer(text)
                    onReplyToMessageRequested: (messageId, senderName, text, mediaKind, mediaMimeType, outgoing) => root.setReplyTarget(messageId, senderName, text, mediaKind, mediaMimeType, outgoing)
                    onEditMessageRequested: (messageId, text) => root.setEditTarget(messageId, text)
                    onMentionClicked: jid => contactInfoDialog.openFor({ isGroup: false, targetJid: jid })
                    onMentionAllClicked: root.openChatInfo()
                    onImageViewRequested: (messageId, localPath) => {
                        // The delegate only carries what it renders; the file name
                        // and send time come from the row snapshot so Save As can
                        // name and date the file after the message.
                        const snapshot = pane.messageSnapshot(messageId)
                        messageImageViewer.showImage(localPath, messageId,
                                                     snapshot ? String(snapshot.mediaFileName || "") : "",
                                                     snapshot ? Number(snapshot.timestampUnix || 0) : 0)
                    }
                    onAlbumViewRequested: (albumMessageId, index) => {
                        // The gallery is only the pictures that are actually on
                        // disk. Stepping onto one that has not been fetched would
                        // be a full-screen nothing; the mosaic behind is where an
                        // undownloaded picture is asked for, and it is one tap
                        // away.
                        const snapshot = pane.messageSnapshot(albumMessageId)
                        const tiles = snapshot && snapshot.album ? (snapshot.album.items ?? []) : []
                        const entries = []
                        let start = 0
                        for (let i = 0; i < tiles.length; ++i) {
                            const media = tiles[i].media ?? {}
                            const path = String(media.path ?? "")
                            if (path.length === 0)
                                continue
                            if (i <= index)
                                start = entries.length
                            entries.push({
                                id: String(tiles[i].id ?? ""),
                                kind: String(tiles[i].kind ?? "image"),
                                path: path,
                                fileName: String(media.filename ?? ""),
                                timestampUnix: Number(tiles[i].timestamp ?? 0),
                                width: Number(media.width ?? 0),
                                height: Number(media.height ?? 0),
                                durationSecs: Number(media.duration_secs ?? 0),
                            })
                        }
                        if (entries.length > 0)
                            messageImageViewer.showGallery(entries, start)
                    }
                    onVideoViewRequested: (messageId, localPath, streamUrl, streamId, kind, durationSecs, startAt) => {
                        const snapshot = pane.messageSnapshot(messageId)
                        messageImageViewer.showVideo(messageId, localPath, streamUrl, streamId, kind, durationSecs, startAt,
                                                     snapshot ? String(snapshot.mediaFileName || "") : "",
                                                     snapshot ? Number(snapshot.timestampUnix || 0) : 0,
                                                     snapshot ? Number(snapshot.mediaWidth || 0) : 0,
                                                     snapshot ? Number(snapshot.mediaHeight || 0) : 0)
                    }
                }
            }

            // Full-screen viewer for message photos and video. Saving and
            // forwarding go back through the conversation's own dialogs, so
            // there is one Save As and one chat picker in the app.
            MediaViewer {
                id: messageImageViewer

                onSaveRequested: (localPath, kind, fileName, timestampUnix) => messageView.saveMedia(localPath, kind, fileName, timestampUnix)
                onForwardRequested: messageId => messageView.openForwardPicker([messageId])
            }

            BusyIndicator {
                anchors.centerIn: parent
                running: root.waitingForMessages && busySpinnerDelay.expired
                visible: running
            }

            // Delay the spinner so cached/small first paints do not flash it;
            // only genuinely slow network or unread/pin resolution shows it.
            Timer {
                id: busySpinnerDelay

                property bool expired: false

                interval: 400
                running: root.waitingForMessages
                onTriggered: expired = true
                onRunningChanged: if (!running) expired = false
            }

            Kirigami.Action {
                id: retryMessagesAction

                text: Whatevr.I18n.i18nc("@action:button", "Retry")
                icon.name: "view-refresh-symbolic"
                onTriggered: Whatevr.ProtocolController.retryMessages()
            }

            Kirigami.PlaceholderMessage {
                anchors.centerIn: parent
                width: Math.min(parent.width - Kirigami.Units.largeSpacing * 4,
                                Kirigami.Units.gridUnit * 22)
                visible: !root.waitingForMessages
                         && !messageView.visible
                text: !Whatevr.ProtocolController.hasSelectedChat
                      ? Whatevr.I18n.i18nc("@info", "Select a chat")
                       : (Whatevr.ProtocolController.messageErrorText.length > 0
                         ? Whatevr.I18n.i18nc("@info", "Messages could not be loaded")
                         : Whatevr.I18n.i18nc("@info", "No messages yet"))
                explanation: !Whatevr.ProtocolController.hasSelectedChat
                             ? Whatevr.I18n.i18nc("@info", "Choose a conversation from the chat list to open it here.")
                              : (Whatevr.ProtocolController.messageErrorText.length > 0
                                 ? Whatevr.ProtocolController.messageErrorText
                                : Whatevr.I18n.i18nc("@info", "Messages you send and receive will appear here."))

                helpfulAction: Whatevr.ProtocolController.hasSelectedChat && Whatevr.ProtocolController.messageErrorText.length > 0
                               ? retryMessagesAction
                               : null
            }
        }

        MessageComposer {
            id: composer

            Layout.fillWidth: true
            visible: Whatevr.ProtocolController.hasSelectedChat
            enabledForChat: Whatevr.ProtocolController.composerEnabled
            opacity: Whatevr.ProtocolController.selectedChatCanSend ? 1 : 0.72
            sending: Whatevr.ProtocolController.sendInFlight
            errorText: Whatevr.ProtocolController.composerErrorText
            replyToMessageId: root.replyToMessageId
            replyToSenderName: root.replyToSenderName
            replyToText: root.replyToText
            replyToMediaKind: root.replyToMediaKind
            replyToMediaMimeType: root.replyToMediaMimeType
            replyToOutgoing: root.replyToOutgoing
            editingMessageId: root.editingMessageId
            editingOriginalText: root.editingOriginalText
            onSendTextRequested: (text, replyToMessageId, mentionedJids) => Whatevr.ProtocolController.sendText(text, replyToMessageId, mentionedJids)
            onSendImageRequested: (fileUrl, caption, replyToMessageId, kind, viewOnce) => Whatevr.ProtocolController.sendMedia(fileUrl, caption, replyToMessageId, kind, viewOnce)
            onSendMediaBatchRequested: (fileUrls, caption, replyToMessageId, kind, viewOnce) => Whatevr.ProtocolController.sendMediaBatch(fileUrls, caption, replyToMessageId, kind, viewOnce)
            onComposingChanged: composing => Whatevr.ProtocolController.setSelectedChatComposing(composing)
            onClearReplyRequested: root.clearReplyTarget()
            onReplyConsumed: root.clearReplyTarget()
            onEditRequested: (messageId, text) => Whatevr.ProtocolController.editMessage(messageId, text)
            onClearEditRequested: root.clearEditTarget()
            onEditConsumed: root.clearEditTarget()
        }
    }

    ContactInfoDialog {
        id: contactInfoDialog
    }

    // Drag-and-drop in two halves: upper stages as documents, lower as
    // photos/video. Drops land in the staging dialog (caption + Send/Cancel),
    // never straight onto the wire.
    DropArea {
        id: documentDropArea

        anchors.top: parent.top
        anchors.left: parent.left
        anchors.right: parent.right
        height: parent.height / 2
        enabled: Whatevr.ProtocolController.hasSelectedChat && Whatevr.ProtocolController.composerEnabled
        onDropped: drop => {
            if (drop.hasUrls) {
                composer.stageDrop(drop.urls, "document")
            }
        }
    }

    DropArea {
        id: mediaDropArea

        anchors.bottom: parent.bottom
        anchors.left: parent.left
        anchors.right: parent.right
        height: parent.height / 2
        enabled: documentDropArea.enabled
        onDropped: drop => {
            if (drop.hasUrls) {
                composer.stageDrop(drop.urls, "")
            }
        }
    }

    Rectangle {
        anchors.fill: parent
        visible: documentDropArea.containsDrag || mediaDropArea.containsDrag
        color: Qt.alpha(Kirigami.Theme.highlightColor, 0.10)
        border.color: Kirigami.Theme.highlightColor
        border.width: 2
        radius: Kirigami.Units.cornerRadius
        z: 1000

        // Divider between the document (top) and media (bottom) halves.
        Rectangle {
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.verticalCenter: parent.verticalCenter
            height: 1
            color: Kirigami.Theme.highlightColor
        }

        Label {
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.top: parent.top
            anchors.topMargin: parent.height / 4
            horizontalAlignment: Text.AlignHCenter
            text: Whatevr.I18n.i18nc("@info drag-and-drop hint", "Drop here to send as documents")
            font.weight: Font.Bold
            color: documentDropArea.containsDrag ? Kirigami.Theme.highlightColor : Kirigami.Theme.textColor
        }

        Label {
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: parent.bottom
            anchors.bottomMargin: parent.height / 4
            horizontalAlignment: Text.AlignHCenter
            text: Whatevr.I18n.i18nc("@info drag-and-drop hint", "Drop here to send as photos or video")
            font.weight: Font.Bold
            color: mediaDropArea.containsDrag ? Kirigami.Theme.highlightColor : Kirigami.Theme.textColor
        }
    }

}
