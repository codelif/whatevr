pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr
import "Wallpapers.js" as Wallpapers

Kirigami.Page {
    id: root

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
            initials: root.initialsForName(Whatevr.ProtocolController.selectedChatName)

            TapHandler {
                enabled: Whatevr.ProtocolController.hasSelectedChat
                onTapped: root.openChatInfo()
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

            MessageView {
                id: messageView

                anchors.fill: parent
                anchors.margins: Kirigami.Units.smallSpacing
                // Hiding this destroys every delegate and rebuilds them on the
                // way back, so "empty for a moment" must not read as "gone":
                // a reload of the same chat keeps the view mounted.
                visible: Whatevr.ProtocolController.hasSelectedChat
                          && root.pinnedLayoutReady
                          && root.messagesCurrent
                          && !Whatevr.ProtocolController.unreadAnchorResolving
                          && Whatevr.ProtocolController.messageErrorText.length === 0
                          && (!Whatevr.ProtocolController.messagesEmpty
                              || Whatevr.ProtocolController.messagesReloading)
                chatId: Whatevr.ProtocolController.selectedChatId
                model: Whatevr.ProtocolController.messageListModel
                loadingMessages: Whatevr.ProtocolController.messagesLoading
                loadingOlderMessages: Whatevr.ProtocolController.olderMessagesLoading
                loadingNewerMessages: Whatevr.ProtocolController.newerMessagesLoading
                canLoadOlderMessages: Whatevr.ProtocolController.canLoadOlderMessages
                canLoadNewerMessages: Whatevr.ProtocolController.canLoadNewerMessages
                olderMessagesFailed: Whatevr.ProtocolController.olderMessagesFailed
                newerMessagesFailed: Whatevr.ProtocolController.newerMessagesFailed
                messagesAtLiveEdge: Whatevr.ProtocolController.messagesAtLiveEdge
                historyExhausted: Whatevr.ProtocolController.selectedChatHistoryExhausted
                phoneHistoryRequesting: Whatevr.ProtocolController.phoneHistoryRequesting
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
                    const snapshot = messageView.messageSnapshot(messageId)
                    messageImageViewer.showImage(localPath, messageId,
                                                 snapshot ? String(snapshot.mediaFileName || "") : "",
                                                 snapshot ? Number(snapshot.timestampUnix || 0) : 0)
                }
                onVideoViewRequested: (messageId, localPath, streamUrl, streamId, kind, durationSecs, startAt) => {
                    const snapshot = messageView.messageSnapshot(messageId)
                    messageImageViewer.showVideo(messageId, localPath, streamUrl, streamId, kind, durationSecs, startAt,
                                                 snapshot ? String(snapshot.mediaFileName || "") : "",
                                                 snapshot ? Number(snapshot.timestampUnix || 0) : 0)
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
            onSendImageRequested: (fileUrl, caption, replyToMessageId) => Whatevr.ProtocolController.sendMedia(fileUrl, caption, replyToMessageId)
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

}
