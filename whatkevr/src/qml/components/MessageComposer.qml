pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Qt.labs.platform as Platform
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Frame {
    id: root

    property bool enabledForChat: false
    property bool sending: false
    property string errorText: ""
    property real composerOverlayX: 0
    property real composerOverlayY: 0
    property real composerOverlayWidth: 0
    property real composerOverlayHeight: 0
    property string replyToMessageId: ""
    property string replyToSenderName: ""
    property string replyToText: ""
    property string replyToMediaKind: ""
    property string replyToMediaMimeType: ""
    property bool replyToOutgoing: false
    readonly property bool replying: replyToMessageId.length > 0
    // Edit-in-place state. Mirrors the reply banner but submits an edit to an
    // existing message instead of sending a new one. editingMessageId non-empty
    // puts the composer in edit mode.
    property string editingMessageId: ""
    property string editingOriginalText: ""
    readonly property bool editing: editingMessageId.length > 0

    // Inline suggestion state, shared by the `:keyword` emoji bar and the `@`
    // mention bar. suggestionMode selects which kind the current results are.
    property var suggestionResults: []
    property int suggestionIndex: 0
    property int suggestionStart: -1
    property string suggestionMode: "emoji"
    readonly property bool suggestionsActive: suggestionBar.visible && root.suggestionResults.length > 0

    // Hover may not steal the selection until the pointer really moves: the
    // HoverHandlers re-fire under a stationary cursor both when the bar appears
    // and on every results refresh (each keystroke resets suggestionIndex to 0),
    // which would silently override the keyboard selection.
    property bool suggestionHoverArmed: false
    property var suggestionHoverBasePos: undefined
    onSuggestionResultsChanged: {
        suggestionHoverArmed = false
        suggestionHoverBasePos = undefined
    }

    // @-mention authoring. The roster is the daemon's `group_members` view for
    // the displayed conversation, read through at render time (the revision tick
    // makes that read reactive); pendingMentions records the {jid, displayName}
    // pairs inserted so submit can rewrite each "@DisplayName" run to the
    // on-wire "@<userpart>" + JID list.
    readonly property bool isGroupChat: Whatevr.ProtocolController.selectedChatId.endsWith("@g.us")
    readonly property var mentionMembers: Whatevr.ProtocolController.chatMembersRevision >= 0
                                          ? Whatevr.ProtocolController.chatMembers("")
                                          : []
    property var pendingMentions: []

    signal sendTextRequested(string text, string replyToMessageId, var mentionedJids)
    signal sendImageRequested(string fileUrl, string caption, string replyToMessageId)
    signal composingChanged(bool composing)
    signal clearReplyRequested()
    signal replyConsumed()
    signal editRequested(string messageId, string text)
    signal clearEditRequested()
    signal editConsumed()

    /// Whatever was typed when edit mode took the input over, put back when it
    /// ends. Entering and leaving edit used to silently destroy a half-typed
    /// draft with no undo.
    property string stashedDraft: ""

    // Prefill with the message body on entering edit mode, restore the draft
    // on leaving. editingOriginalText is set before editingMessageId at the
    // call site, so it is already current when this fires.
    onEditingChanged: {
        if (root.editing) {
            root.stashedDraft = input.getText(0, input.length)
            root.setText(root.editingOriginalText)
            root.forceInputFocus()
        } else {
            root.setText(root.stashedDraft)
            root.stashedDraft = ""
        }
    }

    padding: Kirigami.Units.smallSpacing

    function inputPlainText() {
        return input.getText(0, input.length).trim()
    }

    Kirigami.Theme.colorSet: Kirigami.Theme.View

    background: Rectangle {
        Kirigami.Theme.inherit: false
        Kirigami.Theme.colorSet: Kirigami.Theme.View
        color: Kirigami.Theme.backgroundColor

        Rectangle {
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.top: parent.top
            height: 1
            color: Qt.alpha(Kirigami.Theme.textColor, 0.10)
        }
    }

    function submitText() {
        const text = root.inputPlainText()
        if (!root.enabledForChat || text.length === 0 || root.sending) {
            return
        }
        root.setComposing(false)
        if (root.editing) {
            root.editRequested(root.editingMessageId, text)
            root.editConsumed()
        } else {
            const payload = root.buildSendPayload(text)
            root.sendTextRequested(payload.text, root.replyToMessageId, payload.jids)
            root.replyConsumed()
        }
        input.clear()
        root.pendingMentions = []
        root.hideSuggestions()
        emojiPicker.close()
    }

    function forceInputFocus() {
        if (root.visible && input.enabled) {
            input.forceActiveFocus(Qt.OtherFocusReason)
        }
    }

    // Guards the programmatic setText() below so the resulting onTextChanged is
    // not mistaken for the user typing (which would emit a composing presence).
    property bool restoringText: false

    // Replace the composer contents wholesale (used to restore a per-contact draft
    // when switching chats). Resets transient suggestion/composing state so the
    // restored text doesn't carry the previous chat's activity over.
    function setText(text) {
        root.restoringText = true
        input.text = text || ""
        input.cursorPosition = input.length
        root.restoringText = false
        root.pendingMentions = []
        root.hideSuggestions()
        root.setComposing(false)
    }

    function focusAndInsertText(text, focusInput) {
        if (!root.visible || !input.enabled || text.length === 0) {
            return
        }

        if (focusInput === undefined || focusInput) {
            input.forceActiveFocus(Qt.OtherFocusReason)
        }
        input.insert(input.cursorPosition, text)
    }

    // Returns { start, query } for the `:keyword` token immediately before the
    // cursor, or null when there is no open token (no colon, a closing colon, a
    // space, or a colon that is part of something like "12:30").
    function activeEmojiQuery() {
        const pos = input.cursorPosition
        const before = input.getText(0, pos)
        const colon = before.lastIndexOf(":")
        if (colon < 0) {
            return null
        }
        const token = before.substring(colon + 1)
        if (!/^[a-z0-9_+-]+$/i.test(token)) {
            return null
        }
        if (colon > 0) {
            const prev = before.charAt(colon - 1)
            if (/[a-z0-9_+-]/i.test(prev)) {
                return null
            }
        }
        return { start: colon, query: token }
    }

    function updateEmojiSuggestions() {
        if (!root.enabledForChat) {
            root.hideSuggestions()
            return
        }
        const match = root.activeEmojiQuery()
        if (!match || match.query.length < 2) {
            root.hideSuggestions()
            return
        }
        const results = Whatevr.ProtocolController.emojiModel.searchEmoji(match.query, 40)
        if (!results || results.length === 0) {
            root.hideSuggestions()
            return
        }
        root.suggestionStart = match.start
        root.suggestionResults = results
        root.suggestionIndex = 0
        suggestionBar.visible = true
        suggestionList.positionViewAtBeginning()
    }

    function hideSuggestions() {
        if (!suggestionBar.visible && root.suggestionResults.length === 0) {
            return
        }
        suggestionBar.visible = false
        root.suggestionResults = []
        root.suggestionIndex = 0
        root.suggestionStart = -1
        root.suggestionMode = "emoji"
    }

    // Unified suggestion entry point: an open `@` token (groups only) takes the
    // mention bar, otherwise the `:keyword` emoji bar applies.
    function updateSuggestions() {
        if (!root.enabledForChat) {
            root.hideSuggestions()
            return
        }
        if (root.isGroupChat) {
            const mentionMatch = root.activeMentionQuery()
            if (mentionMatch) {
                // The roster is not subscribed on chat open — a large group is
                // expensive for the daemon to resolve and nothing showed it
                // until now. Asking here means the first `@` may render only
                // "Everyone"; the members arrive moments later as upserts and
                // onChatMembersChanged re-runs this.
                Whatevr.ProtocolController.ensureChatMembers()
                root.updateMentionSuggestions(mentionMatch)
                return
            }
        }
        root.updateEmojiSuggestions()
    }

    // Returns { start, query } for an open `@mention` token before the cursor:
    // an '@' at line start or after whitespace, with no '@'/newline after it and
    // not starting with whitespace. Returns null otherwise.
    function activeMentionQuery() {
        const pos = input.cursorPosition
        const before = input.getText(0, pos)
        const at = before.lastIndexOf("@")
        if (at < 0) {
            return null
        }
        if (at > 0 && !/\s/.test(before.charAt(at - 1))) {
            return null
        }
        const token = before.substring(at + 1)
        if (token.indexOf("@") >= 0 || token.indexOf("\n") >= 0 || /^\s/.test(token)) {
            return null
        }
        return { start: at, query: token }
    }

    function mentionCandidates(query) {
        const q = query.trim().toLowerCase()
        const results = []
        // "Everyone" (@all) leads the list when its tokens match the query.
        if (q.length === 0 || "everyone".indexOf(q) === 0 || "all".indexOf(q) === 0) {
            results.push({ jid: "", displayName: "all", label: "Everyone", avatar: "", isAll: true })
        }
        for (const member of root.mentionMembers) {
            const name = member.display_name || member.phone || member.jid
            if (q.length === 0 || name.toLowerCase().indexOf(q) >= 0) {
                results.push({ jid: member.jid, displayName: name, label: name,
                               avatar: member.avatar_path || "", isAll: false })
            }
            if (results.length >= 40) {
                break
            }
        }
        return results
    }

    function updateMentionSuggestions(match) {
        const results = root.mentionCandidates(match.query)
        if (results.length === 0) {
            root.hideSuggestions()
            return
        }
        root.suggestionMode = "mention"
        root.suggestionStart = match.start
        root.suggestionResults = results
        root.suggestionIndex = 0
        suggestionBar.visible = true
        suggestionList.positionViewAtBeginning()
    }

    function acceptMention(index) {
        const item = root.suggestionResults[index]
        if (!item || root.suggestionStart < 0) {
            root.hideSuggestions()
            return
        }
        const start = root.suggestionStart
        const end = input.cursorPosition
        const inserted = "@" + item.displayName + " "
        input.remove(start, end)
        input.insert(start, inserted)
        const updated = root.pendingMentions.slice()
        updated.push({ jid: item.jid, displayName: item.displayName, isAll: item.isAll === true })
        root.pendingMentions = updated
        root.hideSuggestions()
    }

    // Rewrite the plain composer text into the on-wire form: each recorded
    // "@DisplayName" run becomes "@<userpart>" with its JID collected; @all keeps
    // its literal token but expands to every member JID. Recorded mentions whose
    // literal no longer appears (the user edited them) are dropped — never stale.
    function buildSendPayload(plainText) {
        let outText = plainText
        const jids = []
        for (const mention of root.pendingMentions) {
            const token = "@" + mention.displayName
            const idx = outText.indexOf(token)
            if (idx < 0) {
                continue
            }
            if (mention.isAll) {
                for (const member of root.mentionMembers) {
                    if (member.jid && jids.indexOf(member.jid) < 0) {
                        jids.push(member.jid)
                    }
                }
                continue
            }
            const userpart = mention.jid.split("@")[0]
            outText = outText.substring(0, idx) + "@" + userpart + outText.substring(idx + token.length)
            if (jids.indexOf(mention.jid) < 0) {
                jids.push(mention.jid)
            }
        }
        return { text: outText, jids: jids }
    }

    function cycleSuggestion(delta) {
        const count = root.suggestionResults.length
        if (count === 0) {
            return
        }
        root.suggestionIndex = ((root.suggestionIndex + delta) % count + count) % count
        suggestionList.positionViewAtIndex(root.suggestionIndex, ListView.Contain)
    }

    function acceptSuggestion(index) {
        if (root.suggestionMode === "mention") {
            root.acceptMention(index)
            return
        }
        const item = root.suggestionResults[index]
        if (!item || root.suggestionStart < 0) {
            root.hideSuggestions()
            return
        }
        // Capture locals first: input.remove() fires onTextChanged synchronously,
        // which re-enters updateEmojiSuggestions()/hideSuggestions() and resets
        // suggestionStart to -1 before the insert below would run.
        const start = root.suggestionStart
        const end = input.cursorPosition
        const emoji = item.emoji
        input.remove(start, end)
        input.insert(start, emoji)
        Whatevr.ProtocolController.emojiModel.addRecentEmoji(emoji)
        root.hideSuggestions()
    }

    function updateComposerOverlayRect() {
        if (!Overlay.overlay) {
            return
        }

        const pos = root.mapToItem(Overlay.overlay, 0, 0)
        root.composerOverlayX = pos.x
        root.composerOverlayY = pos.y
        root.composerOverlayWidth = root.width
        root.composerOverlayHeight = root.height
    }

    function positionEmojiPicker() {
        if (!Overlay.overlay) {
            return false
        }

        root.updateComposerOverlayRect()

        const margin = Kirigami.Units.smallSpacing
        // Minimums shrink with the window so the picker still opens on narrow
        // (single-column) layouts instead of silently doing nothing.
        const minHeight = Math.min(Kirigami.Units.gridUnit * 12,
                                   Math.max(0, Overlay.overlay.height * 0.4))
        const maxHeight = Kirigami.Units.gridUnit * 24
        const minWidth = Math.min(Kirigami.Units.gridUnit * 20,
                                  Math.max(0, Overlay.overlay.width - margin * 2))
        const maxWidth = Kirigami.Units.gridUnit * 30
        let targetX = root.composerOverlayX + margin
        let availableWidth = Math.max(0,
                                      Math.min(root.composerOverlayWidth - margin * 2,
                                               Overlay.overlay.width - targetX - margin))
        if (availableWidth < minWidth) {
            // Composer narrower than the picker wants: span the window instead.
            targetX = margin
            availableWidth = Math.max(0, Overlay.overlay.width - margin * 2)
        }
        const availableAbove = root.composerOverlayY - margin * 2

        if (availableWidth < minWidth || availableAbove < minHeight) {
            return false
        }

        emojiPicker.width = Math.min(maxWidth, availableWidth)
        emojiPicker.height = Math.min(maxHeight, availableAbove)
        emojiPicker.x = Math.max(margin, Math.min(targetX, Overlay.overlay.width - emojiPicker.width - margin))
        emojiPicker.y = Math.max(margin, root.composerOverlayY - emojiPicker.height - margin)
        return true
    }

    function toggleEmojiPicker() {
        if (emojiPicker.opened) {
            emojiPicker.close()
            return
        }

        emojiPicker.prepareForOpen()
        if (!root.positionEmojiPicker()) {
            return
        }
        emojiPicker.open()
    }

    function handlePickerGeometryChanged() {
        if (emojiPicker.opened && !root.positionEmojiPicker()) {
            emojiPicker.close()
        }
    }

    function setComposing(composing) {
        if (composing && (!root.enabledForChat || root.sending || input.text.trim().length === 0)) {
            composing = false
        }

        if (composingTimer.running !== composing) {
            composingTimer.running = composing
        }
        if (pauseTimer.running && !composing) {
            pauseTimer.stop()
        }
        root.composingChanged(composing)
    }

    function noteDraftActivity() {
        if (!root.enabledForChat || root.sending || input.text.trim().length === 0) {
            root.setComposing(false)
            return
        }

        root.setComposing(true)
        pauseTimer.restart()
    }

    onEnabledForChatChanged: {
        if (!enabledForChat) {
            root.setComposing(false)
            emojiPicker.close()
            root.hideSuggestions()
            root.clearReplyRequested()
        }
    }
    onSendingChanged: {
        if (sending) {
            root.setComposing(false)
        }
    }

    contentItem: ColumnLayout {
        spacing: Kirigami.Units.smallSpacing

        Kirigami.InlineMessage {
            Layout.fillWidth: true
            visible: root.errorText.length > 0
            type: Kirigami.MessageType.Error
            showCloseButton: false
            text: root.errorText
        }

        ReplyPreview {
            Layout.fillWidth: true
            visible: root.replying
            senderName: root.replyToSenderName
            body: root.replyToText
            mediaKind: root.replyToMediaKind
            mediaMimeType: root.replyToMediaMimeType
            outgoing: root.replyToOutgoing
            showCloseButton: true
            fillColor: Qt.alpha(Kirigami.Theme.textColor, 0.045)
            borderColor: Qt.alpha(Kirigami.Theme.textColor, 0.09)
            onCloseRequested: root.clearReplyRequested()
        }

        // Edit banner: the reply preview reused with a distinct (neutral/amber)
        // accent and an "Editing message" header so edit mode reads differently
        // from a reply. Reply and edit are mutually exclusive.
        ReplyPreview {
            Layout.fillWidth: true
            visible: root.editing
            title: Whatevr.I18n.i18nc("@label composer edit banner", "Editing message")
            iconName: "document-edit-symbolic"
            body: root.editingOriginalText
            showCloseButton: true
            closeButtonText: Whatevr.I18n.i18nc("@action:button", "Cancel edit")
            accentColor: Kirigami.Theme.neutralTextColor
            fillColor: Qt.alpha(Kirigami.Theme.neutralTextColor, 0.07)
            borderColor: Qt.alpha(Kirigami.Theme.neutralTextColor, 0.18)
            onCloseRequested: root.clearEditRequested()
        }

        Item {
            id: suggestionBar

            visible: false
            clip: true
            Layout.fillWidth: true
            Layout.preferredHeight: visible ? Kirigami.Units.gridUnit * 2.2 : 0

            Rectangle {
                anchors.fill: parent
                radius: Kirigami.Units.smallSpacing
                color: Qt.alpha(Kirigami.Theme.textColor, 0.045)
                border.width: 1
                border.color: Qt.alpha(Kirigami.Theme.textColor, 0.09)

                HoverHandler {
                    // Arms hover selection only once the pointer moves away from
                    // where it sat when the bar (re)appeared: one pointChanged is
                    // delivered on appearance without any actual mouse motion.
                    onPointChanged: {
                        if (root.suggestionHoverArmed) {
                            return
                        }
                        const p = point.scenePosition
                        if (root.suggestionHoverBasePos === undefined) {
                            root.suggestionHoverBasePos = Qt.point(p.x, p.y)
                            return
                        }
                        if (Math.abs(p.x - root.suggestionHoverBasePos.x) > 2
                                || Math.abs(p.y - root.suggestionHoverBasePos.y) > 2) {
                            root.suggestionHoverArmed = true
                        }
                    }
                }

                RowLayout {
                    anchors.fill: parent
                    anchors.margins: Kirigami.Units.smallSpacing / 2
                    spacing: Kirigami.Units.smallSpacing

                    ListView {
                        id: suggestionList

                        Layout.fillWidth: true
                        Layout.fillHeight: true
                        orientation: ListView.Horizontal
                        clip: true
                        boundsBehavior: Flickable.StopAtBounds
                        model: root.suggestionResults
                        currentIndex: root.suggestionIndex
                        spacing: Kirigami.Units.smallSpacing / 2

                        delegate: Item {
                            id: suggestionCell

                            required property int index
                            required property var modelData
                            readonly property bool isMention: root.suggestionMode === "mention"

                            height: suggestionList.height
                            width: isMention
                                   ? Math.min(mentionContent.implicitWidth + Kirigami.Units.smallSpacing * 2,
                                              suggestionList.width)
                                   : height

                            Rectangle {
                                anchors.fill: parent
                                anchors.margins: 1
                                radius: Kirigami.Units.smallSpacing
                                color: suggestionCell.index === root.suggestionIndex
                                       ? Qt.alpha(Kirigami.Theme.highlightColor, 0.22)
                                       : "transparent"
                                border.width: suggestionCell.index === root.suggestionIndex ? 1 : 0
                                border.color: Qt.alpha(Kirigami.Theme.highlightColor, 0.6)
                            }

                            Text {
                                visible: !suggestionCell.isMention
                                anchors.centerIn: parent
                                text: suggestionCell.modelData.emoji || ""
                                font.family: Whatevr.ProtocolController.emojiModel.emojiFontFamily
                                font.pixelSize: Math.round(suggestionCell.height * 0.55)
                                renderType: Text.QtRendering
                                horizontalAlignment: Text.AlignHCenter
                                verticalAlignment: Text.AlignVCenter
                            }

                            RowLayout {
                                id: mentionContent

                                visible: suggestionCell.isMention
                                anchors.fill: parent
                                anchors.leftMargin: Kirigami.Units.smallSpacing
                                anchors.rightMargin: Kirigami.Units.smallSpacing
                                spacing: Kirigami.Units.smallSpacing / 2

                                AvatarImage {
                                    visible: suggestionCell.modelData.isAll !== true
                                    Layout.preferredWidth: Math.round(suggestionList.height * 0.74)
                                    Layout.preferredHeight: Math.round(suggestionList.height * 0.74)
                                    avatarLocalPath: suggestionCell.modelData.avatar || ""
                                    initials: (suggestionCell.modelData.label || "?").charAt(0).toUpperCase()
                                }

                                Label {
                                    Layout.fillWidth: true
                                    text: suggestionCell.modelData.label || ""
                                    elide: Text.ElideRight
                                    verticalAlignment: Text.AlignVCenter
                                }
                            }

                            TapHandler {
                                onTapped: root.acceptSuggestion(suggestionCell.index)
                            }

                            HoverHandler {
                                onHoveredChanged: if (hovered && root.suggestionHoverArmed) root.suggestionIndex = suggestionCell.index
                                // Arming happens mid-hover (hovered won't re-fire),
                                // but the same motion delivers pointChanged here.
                                onPointChanged: {
                                    if (hovered && root.suggestionHoverArmed
                                            && root.suggestionIndex !== suggestionCell.index) {
                                        root.suggestionIndex = suggestionCell.index
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }

        RowLayout {
            Layout.fillWidth: true
            spacing: Kirigami.Units.smallSpacing

            ToolButton {
                id: emojiButton

                text: Whatevr.I18n.i18nc("@action:button", "Choose emoji or sticker")
                display: AbstractButton.IconOnly
                checkable: true
                checked: emojiPicker.opened
                enabled: root.enabledForChat && !root.sending
                onClicked: root.toggleEmojiPicker()
                Layout.alignment: Qt.AlignVCenter

                contentItem: Text {
                    text: "☺"
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                    color: emojiButton.checked ? Kirigami.Theme.highlightColor : Kirigami.Theme.textColor
                    opacity: emojiButton.enabled ? 1 : 0.38
                    font.family: Kirigami.Theme.defaultFont.family
                    font.pixelSize: Math.round(Kirigami.Theme.defaultFont.pixelSize * 1.55)
                    font.weight: Font.DemiBold
                    renderType: Text.QtRendering
                }
            }

            ScrollView {
                Layout.fillWidth: true
                Layout.preferredHeight: Math.min(input.implicitHeight + Kirigami.Units.smallSpacing,
                                                 Kirigami.Units.gridUnit * 6)
                Layout.minimumHeight: Kirigami.Units.gridUnit * 2.6
                clip: true
                ScrollBar.vertical.interactive: false
                ScrollBar.vertical.policy: ScrollBar.AsNeeded
                background: Rectangle {
                    Kirigami.Theme.inherit: false
                    Kirigami.Theme.colorSet: Kirigami.Theme.View
                    color: Kirigami.Theme.backgroundColor
                }

                TextArea {
                    id: input

                    enabled: root.enabledForChat && !root.sending
                    placeholderText: root.enabledForChat
                                     ? Whatevr.I18n.i18nc("@info:placeholder", "Message")
                                     : Whatevr.I18n.i18nc("@info:placeholder", "Select a chat to message")
                    textFormat: TextEdit.PlainText
                    wrapMode: TextArea.Wrap
                    background: null
                    selectByMouse: true
                    verticalAlignment: TextEdit.AlignVCenter

                    onTextChanged: {
                        // Restoring a draft programmatically must not be reported
                        // to the contact as live typing.
                        if (root.restoringText) {
                            return
                        }
                        root.noteDraftActivity()
                        root.updateSuggestions()
                    }
                    onCursorPositionChanged: root.updateSuggestions()

                    Keys.onReturnPressed: event => {
                        if (root.suggestionsActive) {
                            root.acceptSuggestion(root.suggestionIndex)
                            event.accepted = true
                            return
                        }
                        const ctrl = (event.modifiers & Qt.ControlModifier) || (event.modifiers & Qt.MetaModifier)
                        if (Whatevr.Settings.enterToSend) {
                            // Enter sends; Shift+Enter inserts a newline.
                            if (event.modifiers & Qt.ShiftModifier) {
                                event.accepted = false
                                return
                            }
                        } else {
                            // Enter inserts a newline; Ctrl/Cmd+Enter sends.
                            if (!ctrl) {
                                event.accepted = false
                                return
                            }
                        }
                        event.accepted = true
                        root.submitText()
                    }

                    Keys.onPressed: event => {
                        if (event.key === Qt.Key_Escape && root.suggestionsActive) {
                            root.hideSuggestions()
                            event.accepted = true
                            return
                        }

                        if ((event.key === Qt.Key_Tab || event.key === Qt.Key_Backtab)
                                && root.suggestionsActive) {
                            root.cycleSuggestion(event.key === Qt.Key_Backtab ? -1 : 1)
                            event.accepted = true
                            return
                        }

                        if (event.matches(StandardKey.Paste) && !root.editing) {
                            if (Whatevr.ProtocolController.sendClipboardImage(root.inputPlainText(), root.replyToMessageId)) {
                                event.accepted = true
                                root.setComposing(false)
                                root.replyConsumed()
                                input.clear()
                                root.hideSuggestions()
                            }
                            return
                        }

                        if (event.key === Qt.Key_Backspace
                                && event.modifiers === Qt.NoModifier
                                && input.cursorPosition > 0
                                && input.selectionStart === input.selectionEnd) {
                            const previous = Whatevr.ProtocolController.previousGraphemeBoundary(input.text, input.cursorPosition)
                            input.remove(previous, input.cursorPosition)
                            event.accepted = true
                        }
                    }
                }
            }

            ToolButton {
                // Attaching media isn't part of an in-place edit (only the
                // caption/body changes), so hide it while editing.
                visible: !root.editing
                icon.name: "image-x-generic-symbolic"
                text: Whatevr.I18n.i18nc("@action:button", "Attach image")
                display: AbstractButton.IconOnly
                enabled: root.enabledForChat && !root.sending
                onClicked: imageDialog.open()
                Layout.alignment: Qt.AlignVCenter
            }

            ToolButton {
                icon.name: root.editing ? "checkmark-symbolic" : "document-send-symbolic"
                text: root.editing
                      ? Whatevr.I18n.i18nc("@action:button", "Save edit")
                      : Whatevr.I18n.i18nc("@action:button", "Send")
                display: AbstractButton.IconOnly
                enabled: root.enabledForChat && !root.sending && input.text.trim().length > 0
                onClicked: root.submitText()
                Layout.alignment: Qt.AlignVCenter
            }
        }
    }

    ExpressionPicker {
        id: emojiPicker

        parent: Overlay.overlay
        z: 10001
        replyToMessageId: root.replyToMessageId
        onEmojiSelected: emoji => root.focusAndInsertText(emoji, false)
        onStickerChosen: keepOpen => {
            root.replyConsumed()
            if (!keepOpen) {
                emojiPicker.close()
                root.forceInputFocus()
            }
        }
    }

    // The @-mention roster is a live view, so there is nothing to fetch or
    // cache: the daemon's first (stored-participant) fill and its later live
    // enrichment both arrive as ordinary upserts. All that is left is re-running
    // an open picker against the new roster, and forgetting the mentions typed
    // into the previous chat.
    Connections {
        target: Whatevr.ProtocolController

        function onChatMembersChanged() {
            if (root.isGroupChat && input.activeFocus) {
                root.updateSuggestions()
            }
        }

        function onSelectionChanged() {
            root.pendingMentions = []
        }
    }

    Item {
        id: emojiInteractionGuard

        parent: Overlay.overlay
        anchors.fill: parent
        visible: emojiPicker.opened
        z: 10000

        readonly property real pickerBottom: emojiPicker.y + emojiPicker.height
        readonly property real pickerRight: emojiPicker.x + emojiPicker.width
        readonly property real composerBottom: root.composerOverlayY + root.composerOverlayHeight
        readonly property real composerRight: root.composerOverlayX + root.composerOverlayWidth

        function closePicker(mouse) {
            emojiPicker.close()
            mouse.accepted = true
        }

        MouseArea {
            x: 0
            y: 0
            width: parent.width
            height: Math.max(0, emojiPicker.y)
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }

        MouseArea {
            x: 0
            y: emojiPicker.y
            width: Math.max(0, emojiPicker.x)
            height: emojiPicker.height
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }

        MouseArea {
            x: emojiInteractionGuard.pickerRight
            y: emojiPicker.y
            width: Math.max(0, parent.width - emojiInteractionGuard.pickerRight)
            height: emojiPicker.height
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }

        MouseArea {
            x: 0
            y: emojiInteractionGuard.pickerBottom
            width: parent.width
            height: Math.max(0, root.composerOverlayY - emojiInteractionGuard.pickerBottom)
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }

        MouseArea {
            x: 0
            y: root.composerOverlayY
            width: Math.max(0, root.composerOverlayX)
            height: root.composerOverlayHeight
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }

        MouseArea {
            x: emojiInteractionGuard.composerRight
            y: root.composerOverlayY
            width: Math.max(0, parent.width - emojiInteractionGuard.composerRight)
            height: root.composerOverlayHeight
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }

        MouseArea {
            x: 0
            y: emojiInteractionGuard.composerBottom
            width: parent.width
            height: Math.max(0, parent.height - emojiInteractionGuard.composerBottom)
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }
    }

    onWidthChanged: root.handlePickerGeometryChanged()
    onHeightChanged: root.handlePickerGeometryChanged()

    Connections {
        target: Overlay.overlay
        function onWidthChanged() { if (emojiPicker.opened) emojiPicker.close() }
        function onHeightChanged() { if (emojiPicker.opened) emojiPicker.close() }
    }

    Timer {
        id: composingTimer

        interval: 10000
        repeat: true
        onTriggered: root.noteDraftActivity()
    }

    Timer {
        id: pauseTimer

        interval: 5000
        repeat: false
        onTriggered: root.setComposing(false)
    }

    Platform.FileDialog {
        id: imageDialog

        title: Whatevr.I18n.i18nc("@title:window", "Attach image")
        nameFilters: [Whatevr.I18n.i18nc("@item:inlistbox", "Images (*.png *.jpg *.jpeg *.webp)")]
        fileMode: Platform.FileDialog.OpenFile
        onAccepted: {
            root.setComposing(false)
            root.sendImageRequested(file, root.inputPlainText(), root.replyToMessageId)
            root.replyConsumed()
            input.clear()
            root.hideSuggestions()
        }
    }
}
