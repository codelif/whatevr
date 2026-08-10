pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import Qt.labs.platform as Platform
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Item {
    id: root

    Kirigami.Theme.colorSet: Kirigami.Theme.View

    property string chatId: ""
    property alias model: list.model
    property bool loadingMessages: false
    property bool loadingOlderMessages: false
    property bool loadingNewerMessages: false
    property bool showLoadingOlderMessages: false
    property bool canLoadOlderMessages: false
    property bool canLoadNewerMessages: false
    property bool olderMessagesFailed: false
    property bool newerMessagesFailed: false
    property bool messagesAtLiveEdge: true
    // On-demand history from the phone: shown at the visual top once the
    // daemon's local history is fully loaded. historyExhausted means the
    // phone already answered "nothing older exists".
    property bool historyExhausted: false
    property bool phoneHistoryRequesting: false

    // Protocol rows are oldest-to-newest in daemon `sort` order. Older extends
    // prepend rows, so their completion restores the prior top-row anchor.
    // followNewest stays true while the highest row is in view.
    property bool openingChat: false
    property bool followNewest: true
    property bool atNewest: true
    property int pendingNewestMessageCount: 0
    property bool olderLoadRequestQueued: false
    property bool newerLoadRequestQueued: false
    property string olderViewportAnchorId: ""
    property real olderViewportAnchorOffset: 0
    property string phoneHistoryViewportAnchorId: ""
    property real phoneHistoryViewportAnchorOffset: 0
    property bool phoneHistoryAnchorActive: false

    // How close (in rows) to the newest message we must be to keep following it.
    property int followRowThreshold: 2
    // …and how close in pixels. Exact bottom (`atYEnd`, or the last row being
    // the bottom-most visible one) is too strict to hold on to: a row appended
    // at the live edge, a delegate settling from its estimated height to its
    // real one, or a sub-pixel contentHeight revision each leave the viewport a
    // hair short of the bottom, and following would stop for good. Anything
    // within this band still counts as parked at the newest message.
    readonly property real followPixelSlack: Kirigami.Units.gridUnit
    // Start fetching older history once the topmost visible row is within this
    // many rows of the oldest loaded message, so the next page usually arrives
    // before the user reaches the edge.
    property int prefetchRowThreshold: 24

    property int clearSelectionGeneration: 0
    property string activeSelectionMessageId: ""
    property var expandedMessageTextIds: ({})

    // Multi-message selection. selectionRevision bumps on every change so
    // recycled delegates re-evaluate their `selected` binding.
    property bool selectionActive: false
    property var selectedIds: ({})
    property int selectedCount: 0
    property int selectionRevision: 0
    // Snapshot of the single selected message (for Reply/Info toolbar actions).
    readonly property var singleSelectedSnapshot: selectedCount === 1 && selectionRevision >= 0
        ? messageSnapshot(Object.keys(selectedIds)[0])
        : null

    // How far back "Delete for everyone" is offered (WhatsApp's revoke window,
    // a little over two days; the server stays authoritative).
    readonly property int revokeWindowSeconds: 2 * 24 * 60 * 60

    // Floating date indicator shown at the top while scrolling. It mirrors the
    // inline day separators (same pill) and fades out shortly after motion
    // stops; it also hides when an inline separator reaches the top, so the two
    // appear to be the same pill (WhatsApp hand-off).
    property string floatingDateText: ""
    property bool floatingDateActive: false
    property bool floatingDateHandoff: false
    // Index whose date the floating pill currently shows. The date for a given
    // row never changes, so the model lookup is skipped while it stays put.
    property int lastTopIndex: -1
    // Visible-row window feeding the index-based scrollbar. topVisibleIndex is
    // the row at the visual top (the lowest visible index); topRowFraction is
    // how much of it is scrolled off
    // above the viewport. They keep their last value when the indexAt probes
    // land in row spacing, so the thumb never flickers.
    property int topVisibleIndex: -1
    property int bottomVisibleIndex: -1
    property real topRowFraction: 0
    // Set while we move the viewport ourselves (chat open, scroll-to-newest) so
    // those programmatic jumps don't flash the floating date pill.
    property bool programmaticScroll: false
    property real lastScrollY: 0
    property string pendingJumpMessageId: ""
    property double pendingJumpDeadlineMs: 0

    // Unread divider anchor returned in the messages subscribe metadata.
    readonly property string unreadAnchorMessageId: Whatevr.ProtocolController.unreadAnchorMessageId
    readonly property int unreadAnchorCount: Whatevr.ProtocolController.unreadAnchorCount
    readonly property bool unreadAnchorResolving: Whatevr.ProtocolController.unreadAnchorResolving
    // Set on the first genuine user scroll after a chat opens; a late-arriving
    // unread anchor must not yank the viewport away from where the user went.
    property bool userScrolledSinceOpen: false
    // The viewport was already placed at the unread divider for this open.
    property bool unreadAnchorPositioned: false

    signal loadOlderMessagesRequested()
    signal loadNewerMessagesRequested()
    signal loadPhoneHistoryRequested()
    signal conversationFocusRequested()
    signal typeIntoComposerRequested(string text)
    signal replyToMessageRequested(string messageId, string senderName, string text, string mediaKind, string mediaMimeType, bool outgoing)
    signal editMessageRequested(string messageId, string text)
    // An @-mention was clicked in a bubble; the conversation pane opens the
    // matching contact/group info dialog.
    signal mentionClicked(string jid)
    signal mentionAllClicked()
    signal imageViewRequested(string localPath)

    onLoadingOlderMessagesChanged: {
        if (loadingOlderMessages) {
            loadingOlderMessagesDelayTimer.restart()
            return
        }

        loadingOlderMessagesDelayTimer.stop()
        showLoadingOlderMessages = false
        if (!phoneHistoryRequesting) {
            restoreOlderViewport()
        }
    }
    onPhoneHistoryRequestingChanged: if (!phoneHistoryRequesting) restorePhoneHistoryViewport()

    Timer {
        id: loadingOlderMessagesDelayTimer

        interval: 500
        repeat: false
        onTriggered: {
            if (root.loadingOlderMessages) {
                root.showLoadingOlderMessages = true
            }
        }
    }

    Timer {
        id: floatingDateIdleTimer

        interval: 1200
        repeat: false
        onTriggered: root.floatingDateActive = false
    }

    Timer {
        id: jumpSettleTimer

        interval: 50
        repeat: false
        onTriggered: root.settlePendingJump()
    }

    // Open-at-bottom settle window: while running, late contentHeight
    // revisions (rows parsed after the eager window, image thumbnails
    // resolving, the delayed height binding) re-pin the view to the newest
    // message instead of leaving it a few pixels off the bottom.
    Timer {
        id: bottomSettleTimer

        interval: 600
        repeat: false
    }

    Timer {
        id: jumpTimeoutTimer

        interval: 1200
        repeat: false
        onTriggered: root.finishPendingJump()
    }

    DragHandler {
        target: null
        acceptedButtons: Qt.LeftButton
    }

    // Shared by every delegate: the "Read more" label is identical in all of
    // them, so it is measured once here instead of once per ChatBubble.
    TextMetrics {
        id: readMoreSharedMetrics

        text: Whatevr.I18n.i18nc("@action:button expand long message", "Read more")
        font.pointSize: Kirigami.Theme.smallFont.pointSize
        font.weight: Font.DemiBold
    }

    function messageSnapshot(messageId) {
        if (!list.model || typeof list.model.messageSnapshot !== "function") {
            return null
        }
        const snapshot = list.model.messageSnapshot(messageId)
        return snapshot && snapshot.messageId ? snapshot : null
    }

    function openMessageContent(messageId) {
        const snapshot = messageSnapshot(messageId)
        if (!snapshot) {
            return
        }
        messageContentDialog.openFor(snapshot)
    }

    function isSelected(messageId) {
        return selectedIds[messageId] === true
    }

    function toggleSelected(messageId) {
        if (messageId.length === 0) {
            return
        }
        const next = Object.assign({}, selectedIds)
        if (next[messageId] === true) {
            delete next[messageId]
            selectedCount = Math.max(0, selectedCount - 1)
        } else {
            next[messageId] = true
            selectedCount += 1
        }
        selectedIds = next
        selectionRevision += 1
        // The mode follows the count: selecting starts it, deselecting the
        // last message ends it (WhatsApp behaviour).
        selectionActive = selectedCount > 0
        if (selectionActive) {
            clearMessageSelection()
        }
    }

    function enterSelection(messageId) {
        if (!isSelected(messageId)) {
            toggleSelected(messageId)
        } else {
            selectionActive = true
        }
    }

    function clearSelection() {
        selectedIds = ({})
        selectedCount = 0
        selectionActive = false
        selectionRevision += 1
    }

    function selectAllMessages() {
        if (!list.model || typeof list.model.allMessageIds !== "function") {
            return
        }
        const ids = list.model.allMessageIds()
        const next = ({})
        for (const id of ids) {
            next[id] = true
        }
        selectedIds = next
        selectedCount = ids.length
        selectionActive = ids.length > 0
        selectionRevision += 1
    }

    function selectedMessageIdList() {
        return Object.keys(selectedIds)
    }

    // Toggle the selection of every message sharing the day of `messageId`,
    // invoked by clicking a date separator pill while in selection mode. If all
    // of that day's messages are already selected they are deselected, else the
    // whole day is added to the selection.
    function toggleDaySelection(messageId) {
        if (!list.model || typeof list.model.messageIdsForDay !== "function") {
            return
        }
        const ids = list.model.messageIdsForDay(messageId)
        if (ids.length === 0) {
            return
        }
        let allSelected = true
        for (const id of ids) {
            if (selectedIds[id] !== true) {
                allSelected = false
                break
            }
        }
        const next = Object.assign({}, selectedIds)
        for (const id of ids) {
            if (allSelected) {
                if (next[id] === true) {
                    delete next[id]
                    selectedCount = Math.max(0, selectedCount - 1)
                }
            } else if (next[id] !== true) {
                next[id] = true
                selectedCount += 1
            }
        }
        selectedIds = next
        selectionRevision += 1
        selectionActive = selectedCount > 0
        if (selectionActive) {
            clearMessageSelection()
        }
    }

    function copySelectedMessages(asMarkdown) {
        if (!list.model || typeof list.model.copyTextForMessages !== "function") {
            return
        }
        let text = list.model.copyTextForMessages(selectedMessageIdList())
        if (asMarkdown) {
            text = Whatevr.ProtocolController.toCommonMark(text)
        }
        if (text.length > 0) {
            Whatevr.ProtocolController.copyToClipboard(text)
            showNotification(Whatevr.I18n.i18ncp("@info:status", "Message copied", "%1 messages copied", root.selectedCount))
        }
        clearSelection()
    }

    function showNotification(text) {
        const window = ApplicationWindow.window
        if (window && typeof window.showPassiveNotification === "function") {
            window.showPassiveNotification(text, "short")
        }
    }

    function canReplyToSnapshot(snapshot) {
        if (!snapshot || !snapshot.messageId || snapshot.isRevoked) {
            return false
        }
        return String(snapshot.text || "").length > 0
               || String(snapshot.mediaKind || "").length > 0
               || String(snapshot.mediaMimeType || "").length > 0
               || String(snapshot.mediaLocalPath || "").length > 0
               || String(snapshot.mediaCacheKey || "").length > 0
    }

    function replyToSnapshot(snapshot) {
        if (!canReplyToSnapshot(snapshot)) {
            return
        }
        const senderName = snapshot.isOutgoing
            ? Whatevr.I18n.i18nc("@label quoted own message sender", "You")
            : String(snapshot.senderName || "")
        replyToMessageRequested(String(snapshot.messageId),
                                senderName,
                                String(snapshot.textPreview || snapshot.text || ""),
                                String(snapshot.mediaKind || ""),
                                String(snapshot.mediaMimeType || ""),
                                Boolean(snapshot.isOutgoing))
    }

    function canRevokeSnapshot(snapshot) {
        return snapshot !== null
               && Boolean(snapshot.isOutgoing)
               && !snapshot.isRevoked
               && (Date.now() / 1000) - Number(snapshot.timestampUnix || 0) < revokeWindowSeconds
    }

    // Editable: our own, not deleted, still within the edit window, and either a
    // text message or an image (whose caption can be edited). Stickers and other
    // media have no editable caption — mirrors the daemon's buildEditContent.
    function canEditSnapshot(snapshot) {
        if (!snapshot || !snapshot.messageId || !snapshot.isOutgoing || snapshot.isRevoked) {
            return false
        }
        if (!Whatevr.ProtocolController.canEditAt(Number(snapshot.timestampUnix || 0))) {
            return false
        }
        const mediaKind = String(snapshot.mediaKind || "")
        const mediaMime = String(snapshot.mediaMimeType || "")
        const isSticker = mediaKind === "sticker"
        const isImage = !isSticker && (mediaKind === "image" || mediaMime.startsWith("image/"))
        const hasMedia = mediaKind.length > 0 || mediaMime.length > 0
        return (!hasMedia && String(snapshot.text || "").length > 0) || isImage
    }

    function editSnapshot(snapshot) {
        if (!canEditSnapshot(snapshot)) {
            return
        }
        // snapshot.text is the body for a text message, or the caption for media.
        editMessageRequested(String(snapshot.messageId), String(snapshot.text || ""))
    }

    function openMessageInfo(messageId) {
        messageInfoDialog.openFor(messageId)
    }

    function confirmDeleteSelection(forEveryone) {
        if (selectedCount > 0) {
            deleteConfirmDialog.openFor(selectedMessageIdList(), forEveryone)
        }
    }

    // Whether every selected message can still be deleted for everyone.
    function canRevokeSelection() {
        const ids = selectedMessageIdList()
        if (ids.length === 0) {
            return false
        }
        for (const id of ids) {
            if (!canRevokeSnapshot(messageSnapshot(id))) {
                return false
            }
        }
        return true
    }

    // Whether any selected message is a deleted (revoked) tombstone, which
    // cannot be replied to, copied or forwarded.
    function selectionHasRevoked() {
        for (const id of selectedMessageIdList()) {
            const snapshot = messageSnapshot(id)
            if (snapshot && snapshot.isRevoked) {
                return true
            }
        }
        return false
    }

    function openForwardPicker(messageIds) {
        forwardChatPicker.openFor(messageIds)
    }

    function openContextMenu(delegate, posX, posY) {
        const snapshot = messageSnapshot(delegate.messageId)
        if (!snapshot) {
            return
        }
        const pos = delegate.mapToItem(list, posX, posY)
        messageContextMenu.openFor(snapshot, pos.x, pos.y)
    }

    // The viewer's own reaction emoji on a message, or "" if they haven't reacted.
    function currentUserReaction(snapshot) {
        if (!snapshot || !snapshot.reactions) {
            return ""
        }
        for (let i = 0; i < snapshot.reactions.length; ++i) {
            if (snapshot.reactions[i].fromMe) {
                return String(snapshot.reactions[i].emoji || "")
            }
        }
        return ""
    }

    // Adds, replaces, or (when emoji repeats the current one, or is empty)
    // removes the viewer's reaction on a message.
    function reactToMessage(messageId, emoji) {
        if (messageId.length === 0) {
            return
        }
        if (emoji.length === 0) {
            Whatevr.ProtocolController.sendReaction(messageId, "")
            return
        }
        const current = currentUserReaction(messageSnapshot(messageId))
        if (current === emoji) {
            Whatevr.ProtocolController.sendReaction(messageId, "")
        } else {
            Whatevr.ProtocolController.sendReaction(messageId, emoji)
            Whatevr.ProtocolController.emojiModel.addRecentEmoji(emoji)
        }
    }

    function openQuickReactions(delegate, posX, posY) {
        const snapshot = messageSnapshot(delegate.messageId)
        if (!snapshot || snapshot.isRevoked) {
            return
        }
        const pos = delegate.mapToItem(list, posX, posY)
        quickReactionPopup.openFor(delegate.messageId, currentUserReaction(snapshot), pos.x, pos.y)
    }

    function openReactionDetails(delegate) {
        const snapshot = messageSnapshot(delegate.messageId)
        if (!snapshot || !snapshot.reactions || snapshot.reactions.length === 0) {
            return
        }
        reactionDetailsDialog.openFor(snapshot.reactions, delegate.messageId)
    }

    function openReactionPicker(messageId) {
        if (messageId.length === 0) {
            return
        }
        reactionEmojiPopup.targetMessageId = messageId
        reactionEmojiPopup.x = Math.round((list.width - reactionEmojiPopup.width) / 2)
        reactionEmojiPopup.y = Math.round((list.height - reactionEmojiPopup.height) / 2)
        reactionEmojiPopup.prepareForOpen()
        reactionEmojiPopup.open()
    }

    function clearMessageSelection() {
        activeSelectionMessageId = ""
        clearSelectionGeneration += 1
    }

    function claimMessageSelection(messageId) {
        if (messageId.length === 0) {
            return
        }
        if (activeSelectionMessageId === messageId) {
            return
        }

        activeSelectionMessageId = messageId
        clearSelectionGeneration += 1
    }

    function messageTextExpanded(messageId) {
        return messageId.length > 0 && expandedMessageTextIds[messageId] === true
    }

    function expandMessageText(messageId) {
        if (messageId.length === 0) {
            return
        }
        if (list.model && typeof list.model.expandMessageText === "function") {
            list.model.expandMessageText(messageId)
        }
        const next = Object.assign({}, expandedMessageTextIds)
        next[messageId] = true
        expandedMessageTextIds = next
        Qt.callLater(updateScrollState)
    }

    function scrollToNewest() {
        if (list.count > 0) {
            programmaticScroll = true
            list.positionViewAtEnd()
            floatingDateActive = false
            floatingDateIdleTimer.stop()
            bottomSettleTimer.restart()
            Qt.callLater(() => { root.programmaticScroll = false })
        }
        pendingNewestMessageCount = 0
        followNewest = true
        atNewest = true
    }

    // Gap between the bottom of the content and the bottom of the viewport.
    // Zero (or negative, mid-overshoot) means parked at the newest message.
    function distanceFromBottom() {
        return list.contentHeight - list.height - list.contentY
    }

    // list.count trails the model inside a rowsInserted handler: QQuickListView
    // and our Connections both observe the same signal and the handler order is
    // an implementation detail. Ask the model itself so an append is never
    // misread as a prepend.
    function modelRowCount() {
        return list.model && typeof list.model.rowCount === "function"
               ? list.model.rowCount()
               : list.count
    }

    // Put a row in the middle of the viewport. positionViewAtIndex(Center)
    // alone is not enough: while the target row is still unmaterialised the
    // view centres against an *estimated* content height, so the row lands
    // visibly off centre once its real delegate exists. Correcting contentY
    // against the materialised item afterwards is what makes a jump land
    // centred. Rows near either end cannot be centred at all — the clamp
    // leaves them as close as the bounds allow, which is the "when possible".
    function centerOnIndex(index) {
        if (index < 0 || index >= list.count) {
            return
        }
        list.positionViewAtIndex(index, ListView.Center)
        list.forceLayout()
        const item = list.itemAtIndex(index)
        if (item === null || item.height <= 0) {
            return
        }
        list.contentY = Math.max(kineticWheelScroller.minimumY(),
                                 Math.min(kineticWheelScroller.maximumY(),
                                          item.y + (item.height - list.height) / 2))
    }

    function captureOlderViewport() {
        olderViewportAnchorId = ""
        if (topVisibleIndex < 0 || !list.model || typeof list.model.messageIdAt !== "function") {
            return
        }
        const item = list.itemAtIndex(topVisibleIndex)
        olderViewportAnchorId = list.model.messageIdAt(topVisibleIndex)
        olderViewportAnchorOffset = item !== null ? item.y - list.contentY : 0
    }

    function restoreOlderViewport() {
        if (olderViewportAnchorId.length === 0 || !list.model || typeof list.model.indexOf !== "function") {
            return
        }
        const id = olderViewportAnchorId
        const offset = olderViewportAnchorOffset
        olderViewportAnchorId = ""
        const index = list.model.indexOf(id)
        if (index < 0) {
            return
        }
        programmaticScroll = true
        list.positionViewAtIndex(index, ListView.Visible)
        list.forceLayout()
        const item = list.itemAtIndex(index)
        if (item !== null) {
            list.contentY = Math.max(kineticWheelScroller.minimumY(),
                                     Math.min(kineticWheelScroller.maximumY(), item.y - offset))
        }
        Qt.callLater(() => {
            root.programmaticScroll = false
            root.updateScrollState()
        })
    }

    function capturePhoneHistoryViewport() {
        captureOlderViewport()
        phoneHistoryViewportAnchorId = olderViewportAnchorId
        phoneHistoryViewportAnchorOffset = olderViewportAnchorOffset
        phoneHistoryAnchorActive = phoneHistoryViewportAnchorId.length > 0
        olderViewportAnchorId = ""
    }

    function restorePhoneHistoryViewport() {
        if (!phoneHistoryAnchorActive || phoneHistoryViewportAnchorId.length === 0
                || !list.model || typeof list.model.indexOf !== "function") {
            return
        }
        const index = list.model.indexOf(phoneHistoryViewportAnchorId)
        if (index < 0) {
            return
        }
        programmaticScroll = true
        list.positionViewAtIndex(index, ListView.Visible)
        list.forceLayout()
        const item = list.itemAtIndex(index)
        if (item !== null) {
            list.contentY = Math.max(kineticWheelScroller.minimumY(),
                                     Math.min(kineticWheelScroller.maximumY(),
                                              item.y - phoneHistoryViewportAnchorOffset))
        }
        Qt.callLater(() => {
            root.programmaticScroll = false
            root.updateScrollState()
        })
    }

    // Place the unread divider near the middle of the viewport so the user sees
    // context above and unread messages below on the first painted frame.
    // Returns false when there is no anchor to position at, letting callers
    // fall back to the bottom of the chat.
    function positionAtUnreadAnchor() {
        if (unreadAnchorMessageId.length === 0 || !list.model || typeof list.model.indexOf !== "function") {
            return false
        }
        const index = list.model.indexOf(unreadAnchorMessageId)
        if (index < 0 || index >= list.count) {
            return false
        }
        programmaticScroll = true
        floatingDateActive = false
        floatingDateIdleTimer.stop()
        kineticWheelScroller.stopKinetic()
        if (list.flicking) list.cancelFlick()
        list.positionViewAtIndex(index, ListView.Center)
        // Synchronous layout before first visible paint prevents a marker jump
        // after the timeline appears.
        list.forceLayout()
        unreadAnchorPositioned = true
        openingChat = false
        pendingNewestMessageCount = 0
        Qt.callLater(() => {
            root.programmaticScroll = false
            root.updateScrollState()
            root.maybeMarkViewedRead()
        })
        return true
    }

    // Single decision point for clearing a chat's unread state, mirroring how
    // WhatsApp operates: everything is marked read at once, but only while the
    // user is genuinely looking at the unread region — window focused, this
    // chat open, and the viewport overlapping the rows below the divider (or
    // parked at the newest message for unread that arrived while open).
    function maybeMarkViewedRead() {
        if (chatId.length === 0 || list.count === 0 || !visible) {
            return
        }
        if (Whatevr.ProtocolController.selectedChatId !== chatId
                || Whatevr.ProtocolController.selectedChatUnreadCount <= 0
                || Qt.application.state !== Qt.ApplicationActive) {
            return
        }

        // The unread region spans anchorIndex..last (the highest row is newest).
        // Without a locatable anchor only the newest row counts as "viewing".
        let regionStart = list.count - 1
        if (unreadAnchorMessageId.length > 0 && list.model && typeof list.model.indexOf === "function") {
            const anchorIndex = list.model.indexOf(unreadAnchorMessageId)
            if (anchorIndex >= 0) {
                regionStart = anchorIndex
            }
        }
        if (bottomVisibleIndex >= regionStart && list.model
                && typeof list.model.messageIdAt === "function") {
            const watermark = list.model.messageIdAt(bottomVisibleIndex)
            Whatevr.ProtocolController.markSelectedChatViewed(watermark)
        }
    }

    function showReferencedMessageUnavailable() {
        const window = ApplicationWindow.window
        if (window && typeof window.showPassiveNotification === "function") {
            window.showPassiveNotification(Whatevr.I18n.i18nc("@info:status", "Referenced message is not available."), "short")
        }
    }

    function beginProgrammaticJump(messageId) {
        pendingJumpMessageId = messageId
        pendingJumpDeadlineMs = Date.now() + 1000
        programmaticScroll = true
        followNewest = false
        atNewest = false
        floatingDateActive = false
        floatingDateIdleTimer.stop()
        jumpSettleTimer.stop()
        jumpTimeoutTimer.restart()
        kineticWheelScroller.stopKinetic()
        if (list.flicking) list.cancelFlick()
    }

    function finishPendingJump() {
        jumpSettleTimer.stop()
        jumpTimeoutTimer.stop()
        pendingJumpMessageId = ""
        pendingJumpDeadlineMs = 0
        programmaticScroll = false
        lastScrollY = list.contentY
        Qt.callLater(updateScrollState)
    }

    function retryOrFailPendingJump() {
        if (Date.now() <= pendingJumpDeadlineMs) {
            jumpSettleTimer.restart()
            return
        }

        finishPendingJump()
    }

    function settlePendingJump() {
        if (pendingJumpMessageId.length === 0 || !list.model || typeof list.model.indexOf !== "function") {
            finishPendingJump()
            return
        }

        const index = list.model.indexOf(pendingJumpMessageId)
        if (index < 0 || index >= list.count) {
            finishPendingJump()
            showReferencedMessageUnavailable()
            return
        }

        const item = list.itemAtIndex(index)
        if (item === null || item.pooled || item.messageId !== pendingJumpMessageId) {
            retryOrFailPendingJump()
            return
        }

        // The row is real now, so its height is no longer an estimate. Re-centre
        // before glowing: a jump into unmaterialised history is centred against
        // estimated heights first time round and settles off centre otherwise.
        centerOnIndex(index)
        const centred = list.itemAtIndex(index)
        const glowTarget = centred !== null && !centred.pooled ? centred : item
        glowTarget.triggerReplyGlow()
        finishPendingJump()
    }

    function jumpToReplyTarget(messageId) {
        if (messageId.length === 0) {
            showReferencedMessageUnavailable()
            return
        }
        beginProgrammaticJump(messageId)
        Whatevr.ProtocolController.jumpToMessage(messageId)
    }

    function jumpToLoadedMessage(messageId) {
        if (messageId.length === 0 || !list.model || typeof list.model.indexOf !== "function") {
            finishPendingJump()
            showReferencedMessageUnavailable()
            return
        }
        if (pendingJumpMessageId !== messageId) {
            return
        }

        const index = list.model.indexOf(messageId)
        if (index < 0 || index >= list.count) {
            finishPendingJump()
            showReferencedMessageUnavailable()
            return
        }

        // Centre unconditionally, including when the row already happens to be
        // on screen: a goto should always put its target in the middle, not
        // leave it clinging to whichever edge it was already near.
        programmaticScroll = true
        floatingDateActive = false
        floatingDateIdleTimer.stop()
        kineticWheelScroller.stopKinetic()
        if (list.flicking) list.cancelFlick()
        centerOnIndex(index)
        Qt.callLater(settlePendingJump)
    }

    function displayedPendingNewestMessageCount() {
        return pendingNewestMessageCount > 99 ? "99+" : String(pendingNewestMessageCount)
    }

    // Recompute followNewest and fire predictive history prefetch. Cheap: two
    // indexAt probes, no allocations, safe to call on every contentY change.
    function updateScrollState() {
        if (list.count === 0) {
            atNewest = true
            followNewest = true
            pendingNewestMessageCount = 0
            topVisibleIndex = -1
            bottomVisibleIndex = -1
            topRowFraction = 0
            return
        }

        const cx = list.width / 2
        const topIndex = list.indexAt(cx, list.contentY + 1)
        const bottomIndex = list.indexAt(cx, list.contentY + Math.max(1, list.height - 1))

        if (topIndex >= 0) {
            // The date string only changes when the top row changes; caching it
            // avoids a model call (and its string allocation) on every frame.
            if (topIndex !== lastTopIndex) {
                lastTopIndex = topIndex
                floatingDateText = list.model ? list.model.dateTextForRow(topIndex) : ""
            }
            const topItem = list.itemAtIndex(topIndex)
            floatingDateHandoff = topItem !== null
                                  && topItem.showDateSeparator
                                  && (topItem.y - list.contentY) < topItem.dateSeparatorHeight
            topRowFraction = topItem !== null && topItem.height > 0
                             ? Math.max(0, Math.min(1, (list.contentY - topItem.y) / topItem.height))
                             : 0
        } else {
            lastTopIndex = -1
        }

        let lo = -1
        let hi = -1
        if (topIndex >= 0) {
            lo = topIndex
            hi = topIndex
        }
        if (bottomIndex >= 0) {
            lo = lo < 0 ? bottomIndex : Math.min(lo, bottomIndex)
            hi = hi < 0 ? bottomIndex : Math.max(hi, bottomIndex)
        }

        if (lo >= 0) {
            topVisibleIndex = lo
        }
        if (hi >= 0) {
            bottomVisibleIndex = hi
        }

        // Geometry decides first: within followPixelSlack of the bottom counts
        // as parked at the newest message even when the index probe disagrees
        // (it returns -1 whenever it lands in the gap between two rows, which
        // at the very bottom used to drop follow-mode outright).
        const nearBottom = distanceFromBottom() <= followPixelSlack

        if (hi >= 0) {
            atNewest = nearBottom || hi === list.count - 1
            followNewest = atNewest || hi >= list.count - 1 - followRowThreshold
        } else {
            atNewest = nearBottom || list.atYEnd
            followNewest = atNewest
        }

        if (atNewest) {
            pendingNewestMessageCount = 0
        }

        if (shouldPrefetchOlder(lo)) {
            queueOlderLoadRequest()
        }
        if (shouldPrefetchNewer(hi)) {
            queueNewerLoadRequest()
        }

        maybeMarkViewedRead()
    }

    function shouldPrefetchOlder(topIndex) {
        return !openingChat
                && pendingJumpMessageId.length === 0
                && topIndex >= 0
                && canLoadOlderMessages
                && !loadingOlderMessages
                && topIndex <= prefetchRowThreshold
    }

    function queueOlderLoadRequest() {
        if (olderLoadRequestQueued) {
            return
        }
        olderLoadRequestQueued = true
        Qt.callLater(() => {
            olderLoadRequestQueued = false
            if (shouldPrefetchOlder(topVisibleIndex)) {
                captureOlderViewport()
                loadOlderMessagesRequested()
            }
        })
    }

    function shouldPrefetchNewer(bottomIndex) {
        return !openingChat
                && pendingJumpMessageId.length === 0
                && bottomIndex >= 0
                && canLoadNewerMessages
                && !loadingNewerMessages
                && bottomIndex >= list.count - 1 - prefetchRowThreshold
    }

    function queueNewerLoadRequest() {
        if (newerLoadRequestQueued) {
            return
        }
        newerLoadRequestQueued = true
        Qt.callLater(() => {
            newerLoadRequestQueued = false
            if (shouldPrefetchNewer(bottomVisibleIndex)) {
                loadNewerMessagesRequested()
            }
        })
    }

    // Reveal the floating date pill on genuine user scrolling (the kinetic
    // scroller and scrollbar drive contentY directly, so list.moving is never
    // set). Suppress our own programmatic jumps and contentHeight-only changes.
    function noteScroll() {
        if (programmaticScroll || openingChat) {
            lastScrollY = list.contentY
            return
        }
        if (Math.abs(list.contentY - lastScrollY) < 1) {
            return
        }
        lastScrollY = list.contentY
        userScrolledSinceOpen = true
        phoneHistoryAnchorActive = false
        if (floatingDateText.length > 0) {
            floatingDateActive = true
            floatingDateIdleTimer.restart()
        }
    }

    function afterModelReset() {
        if (loadingMessages) {
            return
        }
        lastTopIndex = -1
        topVisibleIndex = -1
        bottomVisibleIndex = -1
        topRowFraction = 0
        if (pendingJumpMessageId.length === 0) {
            // A chat with unread messages opens at the unread divider instead
            // of the newest message; updateScrollState then recomputes
            // followNewest/atNewest from the real viewport.
            if (!userScrolledSinceOpen && unreadAnchorResolving) {
                floatingDateActive = false
                floatingDateIdleTimer.stop()
                Qt.callLater(updateScrollState)
                return
            }
            if (userScrolledSinceOpen || !positionAtUnreadAnchor()) {
                scrollToNewest()
            }
        } else {
            programmaticScroll = true
            floatingDateActive = false
            floatingDateIdleTimer.stop()
        }
        floatingDateActive = false
        floatingDateIdleTimer.stop()
        openingChat = false
        Qt.callLater(updateScrollState)
    }

    onChatIdChanged: {
        if (pendingJumpMessageId.length > 0) {
            finishPendingJump()
        }
        clearSelection()
        expandedMessageTextIds = ({})
        pendingNewestMessageCount = 0
        atNewest = true
        followNewest = true
        userScrolledSinceOpen = false
        unreadAnchorPositioned = false
        phoneHistoryAnchorActive = false
        phoneHistoryViewportAnchorId = ""
        if (chatId.length === 0) {
            openingChat = false
        } else {
            openingChat = true
            // No scroll here: the model may still hold the previous chat's
            // rows. afterModelReset()/onVisibleChanged positions once the new
            // chat's first paint is ready.
        }
    }

    onVisibleChanged: {
        if (visible && pendingJumpMessageId.length === 0) {
            if (unreadAnchorMessageId.length > 0) {
                positionAtUnreadAnchor()
            } else if (followNewest) {
                Qt.callLater(scrollToNewest)
            }
        }
    }

    onLoadingMessagesChanged: if (!loadingMessages && chatId.length > 0) Qt.callLater(afterModelReset)

    ListView {
        id: list

        // Pin the viewport itself to the bottom and only grow as tall as content,
        // keeping short conversations adjacent to the composer.
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        // Delayed: resizing the view triggers a layout pass that revises the
        // contentHeight estimate, so a direct binding re-enters itself while
        // delegates churn during a scroll. Coalescing the write through the
        // event queue breaks that cycle.
        Binding on height {
            value: Math.min(list.contentHeight, list.parent.height)
            delayed: true
        }
        clip: true

        // A viewport-height change (e.g. the pinned banner appearing/disappearing
        // above us) leaves contentY untouched, so a list parked at the newest
        // message drifts off the bottom edge. Re-pin when we were following the
        // newest message and the user isn't mid-jump.
        onHeightChanged: {
            if (root.followNewest && !root.programmaticScroll
                    && root.pendingJumpMessageId.length === 0) {
                // Re-pin synchronously first so no drifted intermediate frame is
                // painted (the late pinned-banner pop-in on first open would
                // otherwise flash the viewport off the bottom); the deferred
                // scrollToNewest then settles followNewest/atNewest state.
                if (list.count > 0) {
                    root.programmaticScroll = true
                    list.positionViewAtEnd()
                    Qt.callLater(() => { root.programmaticScroll = false })
                }
                Qt.callLater(root.scrollToNewest)
            }
        }

        // Once the daemon's local history is fully loaded, the header at the
        // visual top offers pulling older messages from the phone, or
        // states that nothing older exists there.
        header: Item {
            width: list.width
            height: phoneHistoryColumn.visible
                    ? phoneHistoryColumn.implicitHeight + Kirigami.Units.largeSpacing * 2
                    : 0

            Column {
                id: phoneHistoryColumn

                anchors.centerIn: parent
                spacing: Kirigami.Units.smallSpacing
                visible: list.count > 0
                         && (!root.canLoadOlderMessages || root.olderMessagesFailed)
                         && !root.openingChat

                ToolButton {
                    visible: root.olderMessagesFailed
                    anchors.horizontalCenter: parent.horizontalCenter
                    text: Whatevr.I18n.i18nc("@action:button", "Retry loading older messages")
                    icon.name: "view-refresh-symbolic"
                    onClicked: root.loadOlderMessagesRequested()
                }

                Label {
                    visible: !root.olderMessagesFailed && root.historyExhausted
                    anchors.horizontalCenter: parent.horizontalCenter
                    width: Math.min(implicitWidth, list.width - Kirigami.Units.gridUnit * 4)
                    horizontalAlignment: Text.AlignHCenter
                    wrapMode: Text.WordWrap
                    text: Whatevr.I18n.i18nc("@info", "Messages older than these are only available on your phone")
                    font.pointSize: Kirigami.Theme.smallFont.pointSize
                    color: Kirigami.Theme.disabledTextColor
                }

                Row {
                    visible: !root.olderMessagesFailed && !root.historyExhausted && root.phoneHistoryRequesting
                    anchors.horizontalCenter: parent.horizontalCenter
                    spacing: Kirigami.Units.smallSpacing

                    BusyIndicator {
                        anchors.verticalCenter: parent.verticalCenter
                        running: visible
                        implicitWidth: Kirigami.Units.iconSizes.smallMedium
                        implicitHeight: implicitWidth
                    }

                    Label {
                        anchors.verticalCenter: parent.verticalCenter
                        text: Whatevr.I18n.i18nc("@info", "Requesting older messages from your phone…")
                        font.pointSize: Kirigami.Theme.smallFont.pointSize
                        color: Kirigami.Theme.disabledTextColor
                    }
                }

                ToolButton {
                    visible: !root.olderMessagesFailed && !root.historyExhausted && !root.phoneHistoryRequesting
                    anchors.horizontalCenter: parent.horizontalCenter
                    text: Whatevr.I18n.i18nc("@action:button", "Load older messages from phone")
                    icon.name: "cloud-download-symbolic"
                    onClicked: {
                        root.capturePhoneHistoryViewport()
                        root.loadPhoneHistoryRequested()
                    }
                }
            }
        }

        // Inter-message gap follows the appearance density setting (live):
        // compact tightens it, comfortable opens it up.
        spacing: {
            switch (Whatevr.Settings.density) {
            case 0: return Math.round(Kirigami.Units.smallSpacing / 4)  // Compact
            case 2: return Math.round(Kirigami.Units.smallSpacing * 1.5) // Comfortable
            default: return Math.round(Kirigami.Units.smallSpacing / 2)  // Standard
            }
        }
        // Deep cache: cache-buffer delegates are incubated asynchronously, so
        // every row prepared here is one fewer synchronous creation while the
        // user is scrolling (those are what stall frames). Cached text rows
        // are now a Text node plus a few rectangles; the bound cost left in
        // the band is thumbnail decodes, capped per image.
        cacheBuffer: Math.max(height * 4, Kirigami.Units.gridUnit * 120)
        reuseItems: true

        // True while flinging faster than ~1.25 viewport-heights per second.
        // Delegates use this to hold off full-resolution media decoding so the
        // scroll stays smooth; it drops back to false shortly after the fling
        // slows, at which point media upgrades to full-res.
        //
        // Derived from live velocities, not just a timer: the kinetic wheel
        // scroller's velocity decays deterministically per rendered frame, so a
        // frame stall cannot drop the flag mid-fling. (The old timer-only latch
        // expired during stalls, kicking off a full-res decode burst for every
        // visible image at the worst possible moment, compounding the stall.)
        property bool flickableFast: false
        readonly property real fastFlickThreshold: Math.max(Kirigami.Units.gridUnit * 60, height * 1.25)
        readonly property bool fastFlicking: flickableFast
            || Math.abs(kineticWheelScroller.velocity) > fastFlickThreshold
        onVerticalVelocityChanged: {
            if (Math.abs(verticalVelocity) > fastFlickThreshold) {
                flickableFast = true
                flickSettleTimer.restart()
            }
        }

        // Scrollbar thumb drags move contentY positionally, so neither
        // verticalVelocity nor the kinetic scroller's velocity ever reflects
        // them; estimate one from contentY deltas while the thumb is pressed so
        // fast drags also engage the fastFlicking media deferral. Slow precise
        // drags never trip the threshold and keep full-res media.
        property real dragVelocity: 0
        property real lastDragY: 0
        property double lastDragMs: 0
        function noteThumbDrag() {
            const now = Date.now()
            if (lastDragMs > 0) {
                const dt = Math.max(1, now - lastDragMs) / 1000
                const instant = (contentY - lastDragY) / dt
                dragVelocity = dragVelocity * 0.4 + instant * 0.6
                if (Math.abs(dragVelocity) > fastFlickThreshold) {
                    flickableFast = true
                    flickSettleTimer.restart()
                }
            }
            lastDragY = contentY
            lastDragMs = now
        }

        Timer {
            id: flickSettleTimer
            interval: 90
            onTriggered: {
                // Re-check the live velocities instead of clearing blindly; the
                // timer may simply have outlived a stalled frame.
                if (Math.abs(list.verticalVelocity) > list.fastFlickThreshold
                        || Math.abs(list.dragVelocity) > list.fastFlickThreshold) {
                    restart()
                } else {
                    list.flickableFast = false
                }
            }
        }

        // Watchdog for touch flicks: Flickable's fling animation is wall-clock
        // driven, so after a stalled frame it teleports by the elapsed time.
        // Detect the stall and stop the fling gracefully instead.
        FrameAnimation {
            running: list.flicking
            onTriggered: {
                if (frameTime > 0.1) {
                    if (list.flicking) list.cancelFlick()
                }
            }
        }

        flickableDirection: Flickable.VerticalFlick
        boundsBehavior: Flickable.StopAtBounds
        boundsMovement: Flickable.StopAtBounds
        acceptedButtons: Qt.NoButton
        flickDeceleration: 4000
        maximumFlickVelocity: 8000

        readonly property real effectiveBodyPointSize: Whatevr.Settings.messageFontSize > 0
            ? Whatevr.Settings.messageFontSize
            : (Kirigami.Theme.defaultFont.pointSize > 0
                ? Kirigami.Theme.defaultFont.pointSize
                : 10)
        readonly property font bodyMetricsFont: Qt.font({
            family: Kirigami.Theme.defaultFont.family,
            pointSize: effectiveBodyPointSize
        })
        function syncBodyMetricsFont() {
            if (list.model && typeof list.model.setBodyMetricsFont === "function") {
                list.model.setBodyMetricsFont(bodyMetricsFont)
            }
        }
        onBodyMetricsFontChanged: syncBodyMetricsFont()
        onModelChanged: syncBodyMetricsFont()

        delegate: ChatBubble {
            id: messageDelegate

            required property var model
            property bool pooledByListView: false
            readonly property bool insideViewport: !pooledByListView
                                                   && y + height >= list.contentY
                                                   && y <= list.contentY + list.height

            listWidth: list.width
            messageId: String(model.messageId || "")
            readonly property bool messageTextTruncated: Boolean(model.textTruncated)
            readonly property bool messageTextExpanded: root.messageTextExpanded(messageId)
            body: messageTextExpanded ? String(model.text || "") : String(model.textPreview || model.text || "")
            layoutBody: messageTextExpanded ? String(model.layoutText || "") : String(model.layoutTextPreview || model.layoutText || "")
            emojiOnlyCount: messageTextExpanded || !messageTextTruncated ? Number(model.emojiOnlyCount || 0) : 0
            hasRichText: messageTextExpanded ? Boolean(model.hasRichText) : Boolean(model.previewHasRichText)
            richText: messageTextExpanded ? String(model.richText || "") : String(model.previewRichText || "")
            replyPreviewBody: String(model.textPreview || model.text || "")
            textTruncated: messageTextTruncated
            textExpanded: messageTextExpanded
            widestLineWidth: Number(model.widestLineWidth || 0)
            lastLineWidth: Number(model.lastLineWidth || 0)
            readMoreTextWidth: readMoreSharedMetrics.advanceWidth
            timeText: String(model.timeText || "")
            dateSeparatorText: String(model.dateSeparatorText || "")
            status: Number(model.status || 0)
            outgoing: Boolean(model.isOutgoing)
            senderName: String(model.senderName || "")
            senderAvatarLocalPath: String(model.senderAvatarLocalPath || "")
            senderInitials: String(model.senderInitials || "?")
            showSenderHeader: Boolean(model.showSenderHeader)
            showSenderAvatar: Boolean(model.showSenderAvatar)
            showSenderGutter: Boolean(model.showSenderGutter)
            groupStart: Boolean(model.groupStart)
            groupEnd: Boolean(model.groupEnd)
            mediaKind: String(model.mediaKind || "")
            mediaMimeType: String(model.mediaMimeType || "")
            mediaLocalPath: String(model.mediaLocalPath || "")
            mediaThumbnailLocalPath: String(model.mediaThumbnailLocalPath || "")
            mediaCacheKey: String(model.mediaCacheKey || "")
            mediaIntrinsicWidth: Number(model.mediaWidth || 0)
            mediaIntrinsicHeight: Number(model.mediaHeight || 0)
            mediaAnimated: Boolean(model.mediaAnimated)
            isRevoked: Boolean(model.isRevoked)
            isEdited: Boolean(model.isEdited)
            isStarred: Boolean(model.isStarred)
            isPinned: Boolean(model.isPinned)
            selectionModeActive: root.selectionActive
            selected: root.selectionRevision >= 0 && root.isSelected(messageId)
            pooled: pooledByListView
            activeInViewport: insideViewport
            fastFlicking: list.fastFlicking
            mediaDownloading: Boolean(model.mediaDownloading)
            mediaDownloadError: String(model.mediaDownloadError || "")
            mediaDownloadProgress: model.mediaDownloadProgress !== undefined ? Number(model.mediaDownloadProgress) : -1
            unreadSeparatorCount: root.unreadAnchorMessageId.length > 0 && messageId === root.unreadAnchorMessageId
                                  ? root.unreadAnchorCount : 0
            replyToMessageId: String(model.replyToMessageId || "")
            replyToSenderName: String(model.replyToSenderName || "")
            replyToText: String(model.replyToText || "")
            replyToMediaKind: String(model.replyToMediaKind || "")
            replyToMediaMimeType: String(model.replyToMediaMimeType || "")
            replyToOutgoing: Boolean(model.replyToIsOutgoing)
            reactions: model.reactions
            clearSelectionGeneration: root.clearSelectionGeneration
            activeSelectionMessageId: root.activeSelectionMessageId
            onConversationFocusRequested: root.conversationFocusRequested()
            onMessageSelectionClaimed: messageId => root.claimMessageSelection(messageId)
            onTypeIntoComposerRequested: text => root.typeIntoComposerRequested(text)
            onReplyRequested: (messageId, senderName, text, mediaKind, mediaMimeType, outgoing) => root.replyToMessageRequested(messageId, senderName, text, mediaKind, mediaMimeType, outgoing)
            onReplyPreviewActivated: messageId => root.jumpToReplyTarget(messageId)
            onReadMoreRequested: messageId => root.openMessageContent(messageId)
            onImageActivated: localPath => root.imageViewRequested(localPath)
            onMentionClicked: jid => root.mentionClicked(jid)
            onMentionAllClicked: root.mentionAllClicked()
            onContextMenuRequested: (posX, posY) => root.openContextMenu(messageDelegate, posX, posY)
            onReactionPickerRequested: (posX, posY) => root.openQuickReactions(messageDelegate, posX, posY)
            onReactionToggleRequested: emoji => root.reactToMessage(messageDelegate.messageId, emoji)
            onReactionDetailsRequested: root.openReactionDetails(messageDelegate)
            onSelectionToggleRequested: root.toggleSelected(messageDelegate.messageId)
            onDaySelectionToggleRequested: root.toggleDaySelection(messageDelegate.messageId)

            ListView.onPooled: {
                pooledByListView = true
            }

            ListView.onReused: {
                pooledByListView = false
            }
        }

        onContentYChanged: {
            if (rowScrollBar.dragging) {
                noteThumbDrag()
            }
            root.updateScrollState()
            root.noteScroll()
        }
        onContentHeightChanged: {
            // During the post-open settle window, content-height revisions
            // (late row parses, thumbnails resolving intrinsic size) would
            // otherwise leave a bottom-opened chat parked a few pixels up.
            // Re-pin synchronously so no drifted frame is painted. Guarded on
            // followNewest so unread-anchor opens are never touched, and the
            // window ends at the first real user scroll.
            if (bottomSettleTimer.running
                    && root.followNewest
                    && !root.programmaticScroll
                    && root.pendingJumpMessageId.length === 0
                    && list.count > 0) {
                root.programmaticScroll = true
                list.positionViewAtEnd()
                Qt.callLater(() => { root.programmaticScroll = false })
            }
            root.updateScrollState()
        }
        onMovementEnded: root.updateScrollState()

        Connections {
            target: list.model
            ignoreUnknownSignals: true
            function onModelReset() {
                // Chat switches and structural reloads land here; show the newest
                // message. Older-history appends do not reset the model.
                Qt.callLater(root.afterModelReset)
            }
            function onModelReplaced() {
                // replaceMessages() usually swaps a chat's content via incremental
                // insert/remove rather than a full reset, so onModelReset never
                // fires on a normal open. Finalise the open here too (clears
                // openingChat, re-enabling history prefetch). Guarded so routine
                // same-chat refetches never yank the viewport to the newest message.
                if (root.openingChat) {
                    Qt.callLater(root.afterModelReset)
                }
            }
            function onRowsInserted(parent, first, last) {
                // Live-edge messages append at the end. Older extends prepend;
                // their viewport is restored when loadingOlderMessages clears.
                if (!root.openingChat && last === root.modelRowCount() - 1) {
                    if (root.followNewest && root.pendingJumpMessageId.length === 0) {
                        Qt.callLater(root.scrollToNewest)
                    } else {
                        root.pendingNewestMessageCount = Math.min(100, root.pendingNewestMessageCount + last - first + 1)
                    }
                }
                if (root.phoneHistoryAnchorActive && list.model
                        && typeof list.model.indexOf === "function"
                        && first < list.model.indexOf(root.phoneHistoryViewportAnchorId)) {
                    Qt.callLater(root.restorePhoneHistoryViewport)
                }
            }
        }

        Component.onCompleted: {
            syncBodyMetricsFont()
            Qt.callLater(root.scrollToNewest)
        }
    }

    Connections {
        target: Whatevr.ProtocolController

        function onUnreadAnchorChanged() {
            // The anchor can resolve after the chat already opened from the
            // message cache (the divider position needed the fresh page). Move
            // there as long as the user hasn't taken over scrolling.
            if (root.chatId.length === 0
                    || root.unreadAnchorPositioned
                    || root.userScrolledSinceOpen
                    || root.pendingJumpMessageId.length > 0) {
                // Declining to move the viewport still ends the open. Leaving
                // openingChat latched here (the user scrolled, or a jump took
                // the viewport, before the anchor resolved) killed live-edge
                // follow *and* older-history prefetch for the rest of the
                // chat's life — both are guarded on !openingChat.
                root.openingChat = false
                return
            }
            Qt.callLater(() => {
                if (root.userScrolledSinceOpen || root.pendingJumpMessageId.length > 0) {
                    root.openingChat = false
                    return
                }
                if (Whatevr.ProtocolController.unreadAnchorMessageId.length > 0) {
                    // An anchor the model does not (yet) hold: fall back to the
                    // newest message rather than sitting latched in openingChat.
                    if (!root.positionAtUnreadAnchor()
                            && !Whatevr.ProtocolController.unreadAnchorResolving) {
                        root.scrollToNewest()
                        root.openingChat = false
                    }
                } else if (root.openingChat && !Whatevr.ProtocolController.unreadAnchorResolving) {
                    root.scrollToNewest()
                    root.openingChat = false
                }
            })
        }

        function onSelectionChanged() {
            // Follows the live unread badge: unread arriving while the user is
            // parked at the bottom of the open chat should clear right away.
            root.maybeMarkViewedRead()
        }

        function onMessageJumpReady(messageId) {
            root.jumpToLoadedMessage(messageId)
        }

        function onMessageJumpUnavailable(messageId) {
            if (root.pendingJumpMessageId !== messageId) {
                return
            }
            root.finishPendingJump()
            root.showReferencedMessageUnavailable()
        }
    }

    Connections {
        target: Qt.application

        function onStateChanged() {
            // Window (re)gaining focus is what turns "unread region visible"
            // into "actually being viewed".
            root.maybeMarkViewedRead()
        }
    }

    KineticWheelScroller {
        id: kineticWheelScroller

        anchors.fill: list
        target: list
        wheelStep: Kirigami.Units.gridUnit * 4
        maximumVelocity: 16000
        // The top edge is only final once all history is loaded; until then the
        // prefetched page usually fills any overshoot before it becomes visible.
        clampAtOrigin: !root.canLoadOlderMessages
    }

    RowScrollBar {
        id: rowScrollBar

        anchors.top: parent.top
        anchors.bottom: parent.bottom
        anchors.right: parent.right
        z: kineticWheelScroller.z + 1

        count: list.count
        topVisibleIndex: root.topVisibleIndex
        bottomVisibleIndex: root.bottomVisibleIndex
        topRowFraction: root.topRowFraction

        onDraggingChanged: {
            // Reset the drag-velocity estimator on both grab and release so
            // its first sample never spans the idle gap before the drag.
            list.dragVelocity = 0
            list.lastDragMs = 0
            if (dragging) {
                if (root.pendingJumpMessageId.length > 0) {
                    root.finishPendingJump()
                }
                kineticWheelScroller.stopKinetic()
                if (list.flicking) list.cancelFlick()
            } else {
                Qt.callLater(root.updateScrollState)
            }
        }

        onDragPositionRequested: (index, fraction) => {
            // positionViewAtIndex materialises the row near the viewport; the
            // exact alignment is done through contentY below.
            list.positionViewAtIndex(index, ListView.Visible)
            const item = list.itemAtIndex(index)
            if (item !== null && item.height > 0) {
                // item.y puts the row top at the viewport top; hide `fraction`
                // of it above so thumb and view round-trip cleanly.
                list.contentY = Math.max(kineticWheelScroller.minimumY(),
                                         Math.min(kineticWheelScroller.maximumY(),
                                                  item.y + fraction * item.height))
            }
        }
        onJumpToNewestRequested: {
            Whatevr.ProtocolController.jumpToBottom()
            list.positionViewAtEnd()
        }
    }

    AbstractButton {
        id: goToBottomButton

        readonly property bool hasPendingNewestMessages: root.pendingNewestMessageCount > 0

        anchors.right: parent.right
        anchors.bottom: parent.bottom
        anchors.margins: Kirigami.Units.largeSpacing
        width: Kirigami.Units.gridUnit * 2.25
        height: width
        visible: list.count > 0 && (!root.atNewest || !root.messagesAtLiveEdge)
        z: kineticWheelScroller.z + 1
        hoverEnabled: true
        focusPolicy: Qt.NoFocus

        Accessible.name: hasPendingNewestMessages
                         ? Whatevr.I18n.i18nc("@action:button", "Go to bottom, %1 new messages", root.displayedPendingNewestMessageCount())
                         : Whatevr.I18n.i18nc("@action:button", "Go to bottom")

        onClicked: {
            root.pendingNewestMessageCount = 0
            kineticWheelScroller.stopKinetic()
            if (list.flicking) list.cancelFlick()
            Whatevr.ProtocolController.jumpToBottom()
            root.scrollToNewest()
            root.conversationFocusRequested()
        }

        background: Rectangle {
            radius: Kirigami.Units.cornerRadius
            color: goToBottomButton.hasPendingNewestMessages
                   ? Kirigami.Theme.highlightColor
                   : Qt.alpha(Kirigami.Theme.backgroundColor, goToBottomButton.hovered || goToBottomButton.pressed ? 0.98 : 0.9)
            border.color: goToBottomButton.hasPendingNewestMessages
                          ? Qt.alpha(Kirigami.Theme.highlightColor, 0.6)
                          : Qt.alpha(Kirigami.Theme.textColor, goToBottomButton.hovered || goToBottomButton.pressed ? 0.2 : 0.12)
        }

        contentItem: Item {
            Kirigami.Icon {
                anchors.centerIn: parent
                visible: !goToBottomButton.hasPendingNewestMessages
                source: "go-down"
                width: Kirigami.Units.iconSizes.smallMedium
                height: width
                color: Kirigami.Theme.textColor
            }

            Label {
                anchors.fill: parent
                visible: goToBottomButton.hasPendingNewestMessages
                text: root.displayedPendingNewestMessageCount()
                color: Kirigami.Theme.highlightedTextColor
                horizontalAlignment: Text.AlignHCenter
                verticalAlignment: Text.AlignVCenter
                font.weight: Font.Bold
                font.pointSize: Kirigami.Theme.smallFont.pointSize
            }
        }
    }

    Rectangle {
        anchors.top: parent.top
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.topMargin: Kirigami.Units.smallSpacing
        width: loadingOlderIndicator.implicitWidth + Kirigami.Units.largeSpacing * 2
        height: loadingOlderIndicator.implicitHeight + Kirigami.Units.smallSpacing * 2
        radius: height / 2
        color: Qt.alpha(Kirigami.Theme.backgroundColor, 0.88)
        border.color: Qt.alpha(Kirigami.Theme.textColor, 0.12)
        visible: root.showLoadingOlderMessages
        z: 10

        Row {
            id: loadingOlderIndicator
            anchors.centerIn: parent
            spacing: Kirigami.Units.smallSpacing

            BusyIndicator {
                running: root.showLoadingOlderMessages
                implicitWidth: Kirigami.Units.iconSizes.smallMedium
                implicitHeight: implicitWidth
            }

            Label {
                text: Whatevr.I18n.i18nc("@info", "Loading older messages")
                font.pointSize: Kirigami.Theme.smallFont.pointSize
                color: Kirigami.Theme.disabledTextColor
            }
        }
    }

    DateSeparatorPill {
        id: floatingDatePill

        anchors.top: parent.top
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.topMargin: Kirigami.Units.smallSpacing
        z: 9
        text: root.floatingDateText
        visible: opacity > 0
        opacity: (root.floatingDateActive
                  && !root.floatingDateHandoff
                  && root.floatingDateText.length > 0
                  && !root.showLoadingOlderMessages) ? 1 : 0

        Behavior on opacity {
            NumberAnimation {
                duration: Kirigami.Units.longDuration
                easing.type: Easing.OutCubic
            }
        }
    }

    Connections {
        target: Whatevr.ProtocolController

        function onMessageActionFailed(errorText) {
            root.showNotification(errorText)
        }

        function onMessageForwarded(chatCount) {
            root.showNotification(Whatevr.I18n.i18ncp("@info:status", "Forwarded to %1 chat", "Forwarded to %1 chats", chatCount))
        }
    }

    Connections {
        target: Whatevr.ProtocolController.stickers

        function onStickerFavoriteFailed(errorText) {
            root.showNotification(errorText)
        }
    }

    Menu {
        id: messageContextMenu

        // Snapshot of the right-clicked message (MessageListModel::messageSnapshot).
        property var ctx: null
        // Favorite state is re-read when the lazily fetched key set changes.
        property bool ctxStickerFavorite: false
        // The MenuItem QQC2 generates for the link submenu; resolved once so
        // its visibility can track the link count (single links get a flat item).
        property Item linkSubMenuItem: null
        // Likewise, submenu visibility does not reliably hide the generated row.
        property Item pinSubMenuItem: null
        // Natural row height, captured before any row is collapsed. Hidden rows
        // must also zero their implicitHeight: the style sums implicitHeight (not
        // height), so a merely invisible row still inflates the menu and adds a
        // scrollbar.
        property real menuRowHeight: 0

        readonly property bool ctxValid: ctx !== null
        readonly property string ctxMessageId: ctxValid ? String(ctx.messageId) : ""
        readonly property string ctxText: ctxValid ? String(ctx.text || "") : ""
        readonly property bool ctxHasRichText: ctxValid && Boolean(ctx.hasRichText)
        readonly property var ctxLinks: ctxValid && ctx.links ? ctx.links : []
        readonly property bool ctxOutgoing: ctxValid && Boolean(ctx.isOutgoing)
        readonly property bool ctxIsRevoked: ctxValid && Boolean(ctx.isRevoked)
        readonly property string ctxMediaKind: ctxValid ? String(ctx.mediaKind || "") : ""
        readonly property string ctxMediaMimeType: ctxValid ? String(ctx.mediaMimeType || "") : ""
        readonly property string ctxMediaLocalPath: ctxValid ? String(ctx.mediaLocalPath || "") : ""
        readonly property string ctxMediaCacheKey: ctxValid ? String(ctx.mediaCacheKey || "") : ""
        readonly property bool ctxIsSticker: ctxMediaKind === "sticker"
        readonly property bool ctxIsImage: !ctxIsSticker && (ctxMediaKind === "image" || ctxMediaMimeType.startsWith("image/"))
        readonly property bool ctxHasMediaFile: ctxMediaLocalPath.length > 0
        readonly property bool ctxHasText: ctxText.length > 0 && !ctxIsRevoked
        readonly property bool ctxIsStarred: ctxValid && Boolean(ctx.isStarred)
        readonly property bool ctxIsPinned: ctxValid && Boolean(ctx.isPinned)
        readonly property bool ctxCanReply: root.canReplyToSnapshot(ctx)
        readonly property bool ctxCanRevoke: root.canRevokeSnapshot(ctx)
        readonly property bool ctxCanEdit: root.canEditSnapshot(ctx)

        parent: list
        // The KDE desktop style uses different menu frame metrics per axis.
        // Keep this app menu compact and visually even around all edges.
        readonly property real framePadding: Kirigami.Units.smallSpacing
        readonly property real horizontalFramePadding: framePadding
        readonly property real verticalFramePadding: framePadding
        topPadding: verticalFramePadding
        bottomPadding: verticalFramePadding
        leftPadding: horizontalFramePadding
        rightPadding: horizontalFramePadding

        // No exit animation: right-clicking another message dismisses the open
        // menu and reopens it at the new position in the same press; a fading
        // copy left at the old spot reads as a second menu flashing.
        exit: Transition {}

        function openFor(snapshot, x, y) {
            ctx = snapshot
            if (ctxIsSticker && ctxMediaCacheKey.length > 0)
                Whatevr.ProtocolController.stickers.beginFavoriteTracking()
            ctxStickerFavorite = ctxIsSticker && ctxMediaCacheKey.length > 0
                                 && Whatevr.ProtocolController.stickers.isStickerFavorite(ctxMediaCacheKey)
            if (linkSubMenuItem) {
                const show = ctxLinks.length > 1
                linkSubMenuItem.visible = show
                linkSubMenuItem.implicitHeight = show ? menuRowHeight : 0
            }
            if (pinSubMenuItem) {
                const show = !ctxIsPinned && !ctxIsRevoked
                pinSubMenuItem.visible = show
                pinSubMenuItem.implicitHeight = show ? menuRowHeight : 0
            }
            // heightRatio is stale after the imperative implicitHeight toggles
            // above; relayout the body so no scrollbar flashes on open.
            if (contentItem)
                contentItem.forceLayout()
            this.x = x
            this.y = y
            open()
        }

        onClosed: Whatevr.ProtocolController.stickers.endFavoriteTracking()

        function linkLabel(link) {
            return link.length > 48 ? link.substring(0, 45) + "…" : link
        }

        Component.onCompleted: {
            for (let i = 0; i < count; ++i) {
                const item = itemAt(i)
                if (item && item.subMenu === copyLinkSubMenu) {
                    linkSubMenuItem = item
                } else if (item && item.subMenu === pinDurationSubMenu) {
                    pinSubMenuItem = item
                }
            }
            // Capture the natural row height before collapsing any row.
            if (pinSubMenuItem)
                menuRowHeight = pinSubMenuItem.implicitHeight
            else if (linkSubMenuItem)
                menuRowHeight = linkSubMenuItem.implicitHeight
            if (linkSubMenuItem) {
                linkSubMenuItem.visible = false
                linkSubMenuItem.implicitHeight = 0
            }
            if (pinSubMenuItem) {
                pinSubMenuItem.visible = false
                pinSubMenuItem.implicitHeight = 0
            }
        }

        Connections {
            target: Whatevr.ProtocolController.stickers

            function onFavoritesChanged() {
                if (messageContextMenu.ctxIsSticker && messageContextMenu.ctxMediaCacheKey.length > 0) {
                    messageContextMenu.ctxStickerFavorite =
                        Whatevr.ProtocolController.stickers.isStickerFavorite(messageContextMenu.ctxMediaCacheKey)
                }
            }
        }

        // Quick-reaction row: tapping an emoji reacts immediately; the inner
        // TapHandlers grab the press so this MenuItem never triggers itself.
        MenuItem {
            id: reactionRowItem

            visible: !messageContextMenu.ctxIsRevoked
            height: visible ? reactionRow.implicitHeight : 0
            padding: 0
            topPadding: 0
            bottomPadding: 0
            leftPadding: 0
            rightPadding: 0
            focusPolicy: Qt.NoFocus
            // Report only the bar's natural minimal width so the menu width is
            // governed by the (usually wider) text rows; the bar then stretches
            // to fill it via fillWidth, instead of forcing the menu over-wide.
            implicitWidth: reactionRow.contentWidth

            background: Rectangle {
                color: "transparent"
            }

            contentItem: QuickReactionBar {
                id: reactionRow

                fillWidth: reactionRowItem.width
                contentPadding: Math.max(1, Math.round(Kirigami.Units.smallSpacing / 2))
                emojiBottomMargin: 0
                currentEmoji: messageContextMenu.ctxValid ? root.currentUserReaction(messageContextMenu.ctx) : ""
                onReacted: emoji => {
                    root.reactToMessage(messageContextMenu.ctxMessageId, emoji)
                    messageContextMenu.close()
                }
                onPickerRequested: {
                    const targetId = messageContextMenu.ctxMessageId
                    messageContextMenu.close()
                    root.openReactionPicker(targetId)
                }
            }
        }

        MenuSeparator {
            visible: !messageContextMenu.ctxIsRevoked
        }

        MenuItem {
            icon.name: "mail-replied-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Reply")
            visible: messageContextMenu.ctxCanReply
            onTriggered: root.replyToSnapshot(messageContextMenu.ctx)
        }

        MenuItem {
            icon.name: "document-edit-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Edit")
            visible: messageContextMenu.ctxCanEdit
            onTriggered: root.editSnapshot(messageContextMenu.ctx)
        }

        MenuItem {
            icon.name: "mail-forward-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Forward…")
            visible: !messageContextMenu.ctxIsRevoked
            onTriggered: root.openForwardPicker([messageContextMenu.ctxMessageId])
        }

        MenuSeparator {
            visible: messageContextMenu.ctxHasText
                     || messageContextMenu.ctxLinks.length > 0
                     || (messageContextMenu.ctxHasMediaFile && (messageContextMenu.ctxIsImage || messageContextMenu.ctxIsSticker))
        }

        MenuItem {
            icon.name: "edit-copy-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu copies the whole message text", "Copy Text")
            visible: messageContextMenu.ctxHasText
            onTriggered: {
                Whatevr.ProtocolController.copyToClipboard(messageContextMenu.ctxText)
                root.showNotification(Whatevr.I18n.i18nc("@info:status", "Text copied"))
            }
        }

        MenuItem {
            icon.name: "text-markdown-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Copy as Markdown")
            visible: messageContextMenu.ctxHasText && messageContextMenu.ctxHasRichText
            onTriggered: {
                Whatevr.ProtocolController.copyToClipboard(Whatevr.ProtocolController.toCommonMark(messageContextMenu.ctxText))
                root.showNotification(Whatevr.I18n.i18nc("@info:status", "Markdown copied"))
            }
        }

        MenuItem {
            icon.name: "edit-link-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu copies the message's only link", "Copy Link")
            visible: messageContextMenu.ctxLinks.length === 1
            onTriggered: {
                Whatevr.ProtocolController.copyToClipboard(String(messageContextMenu.ctxLinks[0]))
                root.showNotification(Whatevr.I18n.i18nc("@info:status", "Link copied"))
            }
        }

        Menu {
            id: copyLinkSubMenu

            title: Whatevr.I18n.i18nc("@action:inmenu submenu of the message's links", "Copy Link")
            icon.name: "edit-link-symbolic"

            readonly property real framePadding: Kirigami.Units.smallSpacing
            readonly property real horizontalFramePadding: framePadding
            readonly property real verticalFramePadding: framePadding
            topPadding: verticalFramePadding
            bottomPadding: verticalFramePadding
            leftPadding: horizontalFramePadding
            rightPadding: horizontalFramePadding

            MenuItem {
                text: Whatevr.I18n.i18nc("@action:inmenu", "Copy All Links")
                onTriggered: {
                    Whatevr.ProtocolController.copyToClipboard(messageContextMenu.ctxLinks.join("\n"))
                    root.showNotification(Whatevr.I18n.i18nc("@info:status", "Links copied"))
                }
            }

            MenuSeparator {}

            Instantiator {
                model: messageContextMenu.ctxLinks.length > 1
                       ? messageContextMenu.ctxLinks.slice(0, 10)
                       : []
                delegate: MenuItem {
                    required property string modelData

                    text: messageContextMenu.linkLabel(modelData)
                    onTriggered: {
                        Whatevr.ProtocolController.copyToClipboard(modelData)
                        root.showNotification(Whatevr.I18n.i18nc("@info:status", "Link copied"))
                    }
                }
                onObjectAdded: (index, object) => copyLinkSubMenu.insertItem(index + 2, object)
                onObjectRemoved: (index, object) => copyLinkSubMenu.removeItem(object)
            }
        }

        MenuItem {
            icon.name: "edit-copy-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Copy Image")
            visible: (messageContextMenu.ctxIsImage || messageContextMenu.ctxIsSticker)
                     && messageContextMenu.ctxHasMediaFile
            onTriggered: {
                Whatevr.ProtocolController.copyImageToClipboard(messageContextMenu.ctxMediaLocalPath)
                root.showNotification(Whatevr.I18n.i18nc("@info:status", "Image copied"))
            }
        }

        MenuItem {
            icon.name: "document-save-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Save As…")
            visible: messageContextMenu.ctxHasMediaFile
            onTriggered: saveMediaDialog.openFor(messageContextMenu.ctxMediaLocalPath)
        }

        MenuSeparator {
            visible: messageContextMenu.ctxIsSticker && messageContextMenu.ctxMediaCacheKey.length > 0
        }

        MenuItem {
            icon.name: messageContextMenu.ctxStickerFavorite ? "starred-symbolic" : "non-starred-symbolic"
            text: messageContextMenu.ctxStickerFavorite
                  ? Whatevr.I18n.i18nc("@action:inmenu", "Remove from Favorite Stickers")
                  : Whatevr.I18n.i18nc("@action:inmenu", "Add to Favorite Stickers")
            visible: messageContextMenu.ctxIsSticker && messageContextMenu.ctxMediaCacheKey.length > 0
            onTriggered: Whatevr.ProtocolController.stickers.setStickerFavorite(messageContextMenu.ctxMediaCacheKey,
                                                                           messageContextMenu.ctxMessageId,
                                                                           !messageContextMenu.ctxStickerFavorite)
        }

        MenuSeparator {
            visible: !messageContextMenu.ctxIsRevoked
        }

        MenuItem {
            icon.name: messageContextMenu.ctxIsStarred ? "starred-symbolic" : "non-starred-symbolic"
            text: messageContextMenu.ctxIsStarred
                  ? Whatevr.I18n.i18nc("@action:inmenu", "Unstar")
                  : Whatevr.I18n.i18nc("@action:inmenu", "Star")
            visible: !messageContextMenu.ctxIsRevoked
            onTriggered: Whatevr.ProtocolController.setMessageStarred(messageContextMenu.ctxMessageId,
                                                                      !messageContextMenu.ctxIsStarred)
        }

        MenuItem {
            icon.name: "window-unpin-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu unpin a message from the chat", "Unpin")
            visible: messageContextMenu.ctxIsPinned && !messageContextMenu.ctxIsRevoked
            onTriggered: Whatevr.ProtocolController.unpinMessage(messageContextMenu.ctxMessageId)
        }

        Menu {
            id: pinDurationSubMenu

            title: Whatevr.I18n.i18nc("@action:inmenu pin a message in the chat", "Pin")
            icon.name: "pin-symbolic"
            // Row visibility is driven imperatively via the captured generated
            // MenuItem (openFor). A `visible` binding here would not hide the row
            // and, since a Menu is a Popup, would auto-open this submenu on first
            // show.

            readonly property real framePadding: Kirigami.Units.smallSpacing
            topPadding: framePadding
            bottomPadding: framePadding
            leftPadding: framePadding
            rightPadding: framePadding

            MenuItem {
                text: Whatevr.I18n.i18nc("@action:inmenu pin duration", "For 24 hours")
                onTriggered: Whatevr.ProtocolController.pinMessage(messageContextMenu.ctxMessageId, 24 * 60 * 60)
            }
            MenuItem {
                text: Whatevr.I18n.i18nc("@action:inmenu pin duration", "For 7 days")
                onTriggered: Whatevr.ProtocolController.pinMessage(messageContextMenu.ctxMessageId, 7 * 24 * 60 * 60)
            }
            MenuItem {
                text: Whatevr.I18n.i18nc("@action:inmenu pin duration", "For 30 days")
                onTriggered: Whatevr.ProtocolController.pinMessage(messageContextMenu.ctxMessageId, 30 * 24 * 60 * 60)
            }
        }

        MenuSeparator {}

        MenuItem {
            icon.name: "edit-select-all-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu start multi-message selection", "Select")
            onTriggered: root.enterSelection(messageContextMenu.ctxMessageId)
        }

        MenuItem {
            icon.name: "documentinfo-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu delivery/read details", "Info")
            visible: messageContextMenu.ctxOutgoing
            onTriggered: root.openMessageInfo(messageContextMenu.ctxMessageId)
        }

        MenuSeparator {}

        MenuItem {
            icon.name: "edit-delete-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu removes the message locally", "Delete for Me…")
            onTriggered: deleteConfirmDialog.openFor([messageContextMenu.ctxMessageId], false)
        }

        MenuItem {
            icon.name: "edit-delete-remove-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu WhatsApp revoke", "Delete for Everyone…")
            visible: messageContextMenu.ctxCanRevoke
            onTriggered: deleteConfirmDialog.openFor([messageContextMenu.ctxMessageId], true)
        }
    }

    Platform.FileDialog {
        id: saveMediaDialog

        property string sourcePath: ""

        fileMode: Platform.FileDialog.SaveFile

        function openFor(path) {
            sourcePath = path
            const base = path.substring(path.lastIndexOf("/") + 1)
            currentFile = Platform.StandardPaths.writableLocation(Platform.StandardPaths.PicturesLocation) + "/" + base
            open()
        }

        onAccepted: {
            if (Whatevr.ProtocolController.saveMediaAs(sourcePath, file)) {
                root.showNotification(Whatevr.I18n.i18nc("@info:status", "File saved"))
            }
        }
    }

    Kirigami.PromptDialog {
        id: deleteConfirmDialog

        // PromptDialog inherits Kirigami.Dialog's self-referential `y` centring,
        // which loops against QQuickPopup's height fitting. Centre on the stable
        // implicitHeight instead, mirroring CenteredDialog (which this can't
        // derive from, being a PromptDialog rather than a plain Dialog).
        y: parent ? Math.round((parent.height - implicitHeight) / 2) : 0

        property var messageIds: []
        property bool forEveryone: false

        function openFor(ids, everyone) {
            messageIds = ids
            forEveryone = everyone
            open()
        }

        title: forEveryone
               ? Whatevr.I18n.i18nc("@title:dialog", "Delete for everyone?")
               : Whatevr.I18n.i18ncp("@title:dialog", "Delete message?", "Delete %1 messages?", messageIds.length)
        subtitle: forEveryone
                  ? Whatevr.I18n.i18ncp("@info", "The message will be deleted for everyone in this chat.",
                                        "%1 messages will be deleted for everyone in this chat.", messageIds.length)
                  : Whatevr.I18n.i18ncp("@info", "The message will only be removed on this device.",
                                        "%1 messages will only be removed on this device.", messageIds.length)
        standardButtons: Kirigami.Dialog.Cancel
        showCloseButton: false

        customFooterActions: [
            Kirigami.Action {
                text: deleteConfirmDialog.forEveryone
                      ? Whatevr.I18n.i18nc("@action:button", "Delete for Everyone")
                      : Whatevr.I18n.i18nc("@action:button", "Delete for Me")
                icon.name: "edit-delete-symbolic"
                onTriggered: {
                    for (const id of deleteConfirmDialog.messageIds) {
                        if (deleteConfirmDialog.forEveryone) {
                            Whatevr.ProtocolController.revokeMessage(id)
                        } else {
                            Whatevr.ProtocolController.deleteMessageForMe(id)
                        }
                    }
                    root.clearSelection()
                    deleteConfirmDialog.close()
                }
            }
        ]
    }

    MessageInfoDialog {
        id: messageInfoDialog
    }

    ForwardChatPickerDialog {
        id: forwardChatPicker

        onForwardConfirmed: (messageIds, chatIds) => {
            for (const messageId of messageIds) {
                Whatevr.ProtocolController.forwardMessage(messageId, chatIds)
            }
            root.clearSelection()
        }
    }

    QuickReactionPopup {
        id: quickReactionPopup

        parent: list

        onReacted: (messageId, emoji) => root.reactToMessage(messageId, emoji)
        onPickerRequested: (messageId, currentEmoji) => root.openReactionPicker(messageId)
    }

    ReactionEmojiPopup {
        id: reactionEmojiPopup

        property string targetMessageId: ""

        parent: list

        onEmojiSelected: emoji => {
            root.reactToMessage(reactionEmojiPopup.targetMessageId, emoji)
            close()
        }
    }

    ReactionDetailsDialog {
        id: reactionDetailsDialog
    }

    MessageContentDialog {
        id: messageContentDialog
    }
}
