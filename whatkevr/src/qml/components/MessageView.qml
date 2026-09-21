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
    // Export dialog lives inside this component; QML callers cannot reach a
    // nested id without an alias.
    property alias exportChatDialog: exportChatDialog
    // Whether this is the pane the conversation is showing. Jump results are
    // broadcast to every warm pane, and a parked one that answers them reports
    // the target missing (it does not hold it) and leaves its highlight behind.
    property bool isCurrentPane: true
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
    // The visible window, named by time rather than by screen edge, because the
    // rows run newest-first and an index no longer says where a message is
    // drawn. The oldest visible row is the one highest on screen and carries
    // the larger index; the newest sits against the composer at the smaller
    // one. topRowFraction is how much of the topmost row is scrolled off above
    // the viewport. All three keep their last value when the indexAt probes
    // land in row spacing, so the thumb never flickers.
    property int oldestVisibleRow: -1
    property int newestVisibleRow: -1
    property real topRowFraction: 0
    // Set while we move the viewport ourselves (chat open, scroll-to-newest) so
    // those programmatic jumps don't flash the floating date pill.
    property bool programmaticScroll: false
    property real lastScrollY: 0
    property string pendingJumpMessageId: ""
    property double pendingJumpDeadlineMs: 0

    // The transcript this pane draws. Set by ConversationPane from the pool
    // slot; null only for a slot that holds no chat yet.
    property var session: null

    // Unread divider anchor returned in the messages subscribe metadata.
    readonly property string unreadAnchorMessageId: session ? session.unreadAnchorMessageId : ""
    readonly property int unreadAnchorCount: session ? session.unreadAnchorCount : 0
    readonly property bool unreadAnchorResolving: !!session && session.unreadAnchorResolving
    // Set on the first genuine user scroll after a chat opens; a late-arriving
    // unread anchor must not yank the viewport away from where the user went.
    property bool userScrolledSinceOpen: false
    // The viewport was already placed at the unread divider for this open.
    property bool unreadAnchorPositioned: false
    // Deadline for the re-centring passes that follow the first placement of
    // the unread divider (see settleUnreadAnchor).
    property double unreadAnchorSettleDeadlineMs: 0
    // Absolute caps on how long a placement may wait for the pane to become
    // laid out (see viewportReady). Only a pane that never appears hits these.
    property double unreadAnchorHiddenDeadlineMs: 0
    property double pendingJumpHiddenDeadlineMs: 0

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
    signal imageViewRequested(string messageId, string localPath)
    /// A video, GIF or video note asked to open full screen.
    signal videoViewRequested(string messageId, string localPath, string streamUrl, string streamId, string kind, int durationSecs, real startAt)
    /// A picture in an album asked to open full screen, with the album behind
    /// it so the viewer can walk the rest of the set.
    signal albumViewRequested(string albumMessageId, int index)

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

    Timer {
        id: unreadAnchorSettleTimer

        interval: 50
        repeat: false
        onTriggered: root.settleUnreadAnchor()
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

    // Likewise shared: at most one row glows at a time (a jump lands on exactly
    // one message), so the whole list drives one animation chain instead of
    // giving every delegate its own five animation objects (DN9).
    SequentialAnimation {
        id: sharedReplyGlow

        property ChatBubble glowTarget: null

        PropertyAction {
            target: sharedReplyGlow.glowTarget
            property: "replyGlowOpacity"
            value: 0
        }
        NumberAnimation {
            target: sharedReplyGlow.glowTarget
            property: "replyGlowOpacity"
            from: 0
            to: 1
            duration: Kirigami.Units.shortDuration
            easing.type: Easing.OutCubic
        }
        PauseAnimation {
            duration: Kirigami.Units.shortDuration
        }
        NumberAnimation {
            target: sharedReplyGlow.glowTarget
            property: "replyGlowOpacity"
            from: 1
            to: 0
            duration: Kirigami.Units.longDuration
            easing.type: Easing.OutCubic
        }
    }

    // Stops the glow and un-latches whatever row it was on. The animation ends
    // by fading to 0, so a glow that is merely interrupted leaves its row lit at
    // whatever opacity it had reached, and nothing else ever puts it out: that
    // is the highlight that stayed stuck behind a tagged message and survived
    // switching chats.
    function clearReplyGlow() {
        if (sharedReplyGlow.running) {
            sharedReplyGlow.stop()
        }
        if (sharedReplyGlow.glowTarget !== null) {
            sharedReplyGlow.glowTarget.replyGlowOpacity = 0
            sharedReplyGlow.glowTarget = null
        }
    }

    // Retargets the shared glow. A glow already in flight is stopped and its
    // row reset first, otherwise the previous target would be left latched at
    // whatever opacity it had reached.
    function playReplyGlow(bubble) {
        if (bubble === null) {
            return
        }
        if (sharedReplyGlow.running) {
            sharedReplyGlow.stop()
            if (sharedReplyGlow.glowTarget !== null) {
                sharedReplyGlow.glowTarget.replyGlowOpacity = 0
            }
        }
        sharedReplyGlow.glowTarget = bubble
        sharedReplyGlow.restart()
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

    // Select-all has existed as a function with no way to invoke it. In
    // selection mode Ctrl+A is what everyone reaches for.
    Shortcut {
        sequences: [StandardKey.SelectAll]
        enabled: root.selectionActive
        onActivated: root.selectAllMessages()
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

    function openMessageInfo(messageId, senderDevice) {
        messageInfoDialog.openFor(messageId, senderDevice || 0)
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

    /// Opens Save As for a media file. Public so the full-screen viewer reuses
    /// this dialog rather than growing one of its own.
    function saveMedia(localPath, kind, fileName, timestampUnix) {
        saveMediaDialog.openFor(localPath, kind, fileName, timestampUnix || 0)
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

    function openPollVoters(delegate, optionIndex) {
        const poll = delegate.poll
        if (!poll || !poll.options || poll.options.length === 0) {
            return
        }
        pollVotersDialog.openFor(poll, delegate.messageId, optionIndex)
    }

    function openEventResponses(delegate, response) {
        const plan = delegate.eventInfo
        if (!plan) {
            return
        }
        eventResponsesDialog.openFor(plan, delegate.messageId, response)
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
        // Keep the expanding row where the reader is looking: the row grows
        // downward, and without re-anchoring, everything below the tap
        // scrolled away under the cursor.
        let anchorOffset = -1
        const index = list.model && typeof list.model.indexOf === "function"
            ? list.model.indexOf(messageId) : -1
        const item = index >= 0 ? list.itemAtIndex(index) : null
        if (item !== null) {
            anchorOffset = item.y - list.contentY
        }
        if (list.model && typeof list.model.expandMessageText === "function") {
            list.model.expandMessageText(messageId)
        }
        const next = Object.assign({}, expandedMessageTextIds)
        next[messageId] = true
        expandedMessageTextIds = next
        if (item !== null && anchorOffset >= 0 && !followNewest) {
            Qt.callLater(function() {
                const again = list.itemAtIndex(index)
                if (again !== null) {
                    list.contentY = again.y - anchorOffset
                }
            })
        }
        Qt.callLater(updateScrollState)
    }

    function scrollToNewest() {
        traceViewport("scrollToNewest")
        cancelUnreadAnchorSettle()
        if (list.count > 0) {
            programmaticScroll = true
            list.positionViewAtBeginning()
            floatingDateActive = false
            floatingDateIdleTimer.stop()
            bottomSettleTimer.restart()
            refreshBottomPin()
            Qt.callLater(() => { root.programmaticScroll = false })
        }
        pendingNewestMessageCount = 0
        followNewest = true
        atNewest = true
    }

    // Coalesces the settle-window re-pin to at most one per event-loop turn.
    // positionViewAtBeginning() forces a synchronous layout of the whole materialised
    // band, so calling it once per content-height revision was the dominant
    // cost of the frames after a chat opened.
    property bool bottomRepinQueued: false
    function queueBottomRepin() {
        if (bottomRepinQueued) {
            return
        }
        bottomRepinQueued = true
        Qt.callLater(applyBottomRepin)
    }

    property int bottomRepinRetries: 0
    function applyBottomRepin() {
        bottomRepinQueued = false
        if (!followNewest || pendingJumpMessageId.length > 0 || list.count === 0) {
            bottomRepinRetries = 0
            return
        }
        if (programmaticScroll) {
            // Something else is placing the viewport this turn. Its own reset
            // of the flag is already queued ahead of us, so ask again behind it
            // rather than dropping the request: the row that settles its height
            // and moves the bottom of the transcript regularly does so in the
            // very turn a jump-to-bottom is still holding this flag, and a
            // dropped request is a reader left short of the newest message with
            // nothing to tell them why.
            if (bottomRepinRetries < 3) {
                bottomRepinRetries += 1
                bottomRepinQueued = true
                Qt.callLater(applyBottomRepin)
            }
            return
        }
        bottomRepinRetries = 0
        // positionViewAtBeginning() forces a synchronous layout of the whole
        // materialised band, and the settle window fires on every content-height
        // revision, several per send while the new row settles. Skip it when
        // the viewport did not actually drift, which is the ordinary case for a
        // list that was already parked at the bottom.
        if (Math.abs(distanceFromBottom()) < 1) {
            return
        }
        traceViewport("applyBottomRepin")
        programmaticScroll = true
        list.positionViewAtBeginning()
        refreshBottomPin()
        Qt.callLater(() => { root.programmaticScroll = false })
    }

    // Gap between the bottom of the content and the bottom of the viewport.
    // Zero (or negative, mid-overshoot) means parked at the newest message.
    //
    // The bottom bound is originY + contentHeight - height, not
    // contentHeight - height: a ListView lays its materialised rows out from
    // wherever the band happens to sit and estimates everything above from the
    // running average row height, so originY is neither zero nor stable. In a
    // transcript whose rows run from a one-line reply to a 600px card it swings
    // by thousands of pixels as the band slides, and dropping it answered five
    // figures for a view sitting exactly on the bottom. Every geometric bottom
    // test in here was reading that number. The wheel scroller has always had
    // the bound right, so ask it rather than keeping a second opinion.
    function distanceFromBottom() {
        return kineticWheelScroller.maximumY() - list.contentY
    }

    // True while the viewport is sitting on the bottom of the content.
    //
    // Distinct from followNewest, which is a two-row band and deliberately
    // survives a small scroll away. This is the stick-to-the-bottom invariant,
    // and it is what decides whether content settling underneath the reader
    // takes the view down with it: a card measuring itself a moment after the
    // view was placed used to move the bottom of the transcript and leave the
    // viewport where it was, which is a jump-to-bottom that lands short.
    property bool pinnedToBottom: true
    function refreshBottomPin() {
        pinnedToBottom = list.count > 0 && distanceFromBottom() <= 1
    }

    // Scroll anchoring used to live here: when a row above the viewport changed
    // height after the list had placed it, the view was nudged by the same
    // amount so the reader stayed on the words they were reading.
    //
    // A bottom-up list does not need it, and running it anyway would be the bug
    // it was written to fix, with the sign flipped. Laid out from the newest
    // message upward, a row growing somewhere up in history moves only the rows
    // older than itself; everything between it and the composer is positioned
    // from the bottom and does not care. There is no shove left to undo, so
    // compensating for one would be a shove of its own.

    // Viewport-placement trace (WHATKEVR_PERF=1). Every path that can move the
    // viewport during an open reports through here, so one reproduction shows
    // which one actually ran and in what order.
    function traceViewport(what, extra) {
        if (!Whatevr.ProtocolController.perfLogging) {
            return
        }
        console.log("[vp]", what,
                    "| visible=" + visible,
                    "listH=" + list.height.toFixed(0),
                    "contentH=" + list.contentHeight.toFixed(0),
                    "contentY=" + list.contentY.toFixed(0),
                    "count=" + list.count,
                    "opening=" + openingChat,
                    "follow=" + followNewest,
                    "userScrolled=" + userScrolledSinceOpen,
                    "jump=" + pendingJumpMessageId,
                    "anchor=" + unreadAnchorMessageId,
                    "anchorPositioned=" + unreadAnchorPositioned,
                    "resolving=" + unreadAnchorResolving,
                    extra === undefined ? "" : "| " + extra)
    }

    // Closes the chat-open stopwatch started in subscribeMessages(). The phase
    // that matters is not when the rows arrive but when they are on the glass,
    // and QML is the only side that knows: openingChat clearing means the model
    // is complete and the viewport is placed, but the frame carrying that has
    // not been rendered yet. A FrameAnimation fires once per rendered frame, so
    // arming it here and stamping on its first tick measures a real paint.
    // A stamp is owed from the moment a chat is selected until the frame that
    // first shows its rows. It is not enough to stamp when openingChat clears:
    // a switch empties the window first, so the pane reaches a settled, painted,
    // empty state a few milliseconds in, and the rows arrive after it. The debt
    // is therefore held until there is something on screen to have painted.
    property bool openStampOwed: false

    readonly property bool openStampReady: openStampOwed && !openingChat && list.count > 0
    onOpenStampReadyChanged: {
        if (openStampReady) {
            Whatevr.ProtocolController.markChatOpenPhase("settled")
            paintProbe.running = true
        }
    }

    // FrameAnimation fires once per rendered frame, so arming it after the
    // window is placed and stamping on its first tick measures a real paint
    // rather than the intent to paint.
    FrameAnimation {
        id: paintProbe

        running: false
        onTriggered: {
            running = false
            root.openStampOwed = false
            Whatevr.ProtocolController.markChatOpenPhase("painted")
        }
    }

    // Scroll pacing (WHATKEVR_PERF=1). Frame gaps are the only honest measure of
    // whether scrolling is smooth: an average is useless here, because what is
    // felt as lag is the handful of frames that took four times as long as the
    // rest. So this counts frames while the transcript is actually moving and
    // reports the distribution once a second, along with how far the view
    // travelled and how many rows the band was holding, which is what the cost
    // is proportional to.
    //
    // One object per pane, not per row, and it does not run at all unless the
    // env var is set.
    FrameAnimation {
        id: scrollProbe

        readonly property bool transcriptMoving: list.moving || list.flicking
                                                 || Math.abs(list.verticalVelocity) > 1
                                                 || rowScrollBar.dragging
        running: Whatevr.ProtocolController.perfLogging && root.visible && transcriptMoving

        property int frames: 0
        property int overOneFrame: 0
        property int overFourFrames: 0
        property real worstMs: 0
        property real travelled: 0
        property real lastY: 0
        property double windowStart: 0

        onRunningChanged: {
            if (running) {
                frames = 0; overOneFrame = 0; overFourFrames = 0
                worstMs = 0; travelled = 0
                lastY = list.contentY
                windowStart = Date.now()
            } else if (frames > 0) {
                report("settle")
            }
        }

        function report(why) {
            const elapsed = Math.max(1, Date.now() - windowStart)
            console.log("[perf] scroll", why,
                        "frames=" + frames,
                        "fps=" + (frames * 1000 / elapsed).toFixed(0),
                        ">16ms=" + overOneFrame,
                        ">66ms=" + overFourFrames,
                        "worst=" + worstMs.toFixed(1) + "ms",
                        "travel=" + travelled.toFixed(0) + "px",
                        "rows=" + root.materialisedRowCount(),
                        "band=" + list.cacheBuffer.toFixed(0) +
                        " fast=" + list.fastFlicking)
            frames = 0; overOneFrame = 0; overFourFrames = 0
            worstMs = 0; travelled = 0
            windowStart = Date.now()
        }

        // Rows held at the end of the previous frame, so a stall can be
        // attributed: a long frame that also churned twenty rows is delegate
        // work, one that churned none while the view barely moved is something
        // else entirely (a decode landing, a layout pass, the collector).
        property int lastRows: 0

        onTriggered: {
            const ms = frameTime * 1000
            const moved = Math.abs(list.contentY - lastY)
            frames += 1
            if (ms > 16.6) overOneFrame += 1
            if (ms > 66) overFourFrames += 1
            if (ms > worstMs) worstMs = ms
            travelled += moved
            lastY = list.contentY

            // Sampled every frame, not only on a stall: compared against a
            // baseline refreshed once in thirty frames, churn read zero for
            // every stall whether or not a single row had moved, which is
            // exactly the thing it exists to distinguish.
            const rows = root.materialisedRowCount()
            const churn = rows - lastRows
            lastRows = rows
            if (ms > 66) {
                console.log("[perf] stall", ms.toFixed(0) + "ms",
                            "moved=" + moved.toFixed(0) + "px",
                            "rows=" + rows,
                            "churn=" + churn,
                            "band=" + list.cacheBuffer.toFixed(0),
                            "fast=" + list.fastFlicking,
                            "flicking=" + list.flicking,
                            "kinetic=" + Math.abs(kineticWheelScroller.velocity).toFixed(0))
            }

            if (Date.now() - windowStart >= 1000) {
                report("moving")
            }
        }
    }

    // How many rows the list is currently holding materialised. Walks the
    // content item's children rather than asking the view, because that count
    // (viewport plus cache band) is exactly what a scroll re-binds per frame.
    function materialisedRowCount() {
        const content = list.contentItem
        if (!content) {
            return 0
        }
        let n = 0
        const kids = content.children
        for (let i = 0; i < kids.length; ++i) {
            if (kids[i].messageId !== undefined && String(kids[i].messageId).length > 0) {
                n += 1
            }
        }
        return n
    }

    // Whether the list can actually answer geometry questions. ConversationPane
    // keeps this pane hidden until the messages *and* the pinned-banner layout
    // have both settled, and a hidden view materialises no delegates: every
    // itemAtIndex() is null and positionViewAtIndex() works purely off the
    // estimated content height. Placing a viewport in that state lands nowhere
    // useful, so both settle loops wait for this instead of burning their
    // deadline against a view that cannot answer.
    function viewportReady() {
        return visible && list.height > 0 && list.count > 0
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
            traceViewport("centerOnIndex:out-of-range", "index=" + index)
            return
        }
        list.positionViewAtIndex(index, ListView.Center)
        list.forceLayout()
        const item = list.itemAtIndex(index)
        if (item === null || item.height <= 0) {
            traceViewport("centerOnIndex:unmaterialised", "index=" + index
                          + " item=" + (item === null ? "NULL" : "h=" + item.height))
            return
        }
        traceViewport("centerOnIndex:ok", "index=" + index + " itemY=" + item.y.toFixed(0)
                      + " itemH=" + item.height.toFixed(0))
        list.contentY = Math.max(kineticWheelScroller.minimumY(),
                                 Math.min(kineticWheelScroller.maximumY(),
                                          item.y + (item.height - list.height) / 2))
    }

    // Put the unread divider — not the anchor row — in the middle of the
    // viewport. The divider is drawn at the *top* of the anchor row, so
    // centring the row itself pushes the divider (item.height - list.height)/2
    // above the viewport whenever the anchor message is taller than the screen,
    // which is ordinary for a long message or a media bubble: a 1021px row in a
    // 758px viewport hid the marker by 131px. Placing the row's top at the
    // viewport centre puts the marker on screen with the unread messages
    // reading downward from it, and the clamp is the "when possible" at either
    // end of a chat.
    function centerDividerOnIndex(index) {
        if (index < 0 || index >= list.count) {
            traceViewport("centerDividerOnIndex:out-of-range", "index=" + index)
            return false
        }
        list.positionViewAtIndex(index, ListView.Center)
        list.forceLayout()
        const item = list.itemAtIndex(index)
        if (item === null || item.height <= 0) {
            traceViewport("centerDividerOnIndex:unmaterialised", "index=" + index)
            return false
        }
        list.contentY = Math.max(kineticWheelScroller.minimumY(),
                                 Math.min(kineticWheelScroller.maximumY(),
                                          item.y - list.height / 2))
        traceViewport("centerDividerOnIndex:ok", "index=" + index + " itemY=" + item.y.toFixed(0)
                      + " itemH=" + item.height.toFixed(0))
        return true
    }

    function captureOlderViewport() {
        olderViewportAnchorId = ""
        if (oldestVisibleRow < 0 || !list.model || typeof list.model.messageIdAt !== "function") {
            return
        }
        const item = list.itemAtIndex(oldestVisibleRow)
        olderViewportAnchorId = list.model.messageIdAt(oldestVisibleRow)
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
            traceViewport("positionAtUnreadAnchor:no-anchor")
            return false
        }
        const index = list.model.indexOf(unreadAnchorMessageId)
        if (index < 0 || index >= list.count) {
            traceViewport("positionAtUnreadAnchor:not-in-model", "index=" + index)
            return false
        }
        traceViewport("positionAtUnreadAnchor", "index=" + index + " ready=" + viewportReady())
        programmaticScroll = true
        floatingDateActive = false
        floatingDateIdleTimer.stop()
        kineticWheelScroller.stopKinetic()
        if (list.flicking) list.cancelFlick()
        openingChat = false
        pendingNewestMessageCount = 0
        // The first pass centres against estimates for every row that is not
        // materialised yet — including the divider's own height, which the
        // anchor delegate only gains once it is built — and cannot do even that
        // while the pane is still hidden. Keep re-centring until the row is
        // real. Claiming the placement here (returning true) is what stops
        // afterModelReset falling back to the newest message meanwhile.
        unreadAnchorSettleDeadlineMs = Date.now() + 1000
        unreadAnchorHiddenDeadlineMs = Date.now() + 10000
        if (viewportReady()) {
            centerDividerOnIndex(index)
        }
        unreadAnchorSettleTimer.restart()
        return true
    }

    // Re-centre the unread divider until its row is materialised, then hand the
    // viewport back. Mirrors settlePendingJump; programmaticScroll stays true
    // for the duration so the bottom re-pin paths (list.onHeightChanged,
    // onContentHeightChanged) do not drag the view to the newest message while
    // the divider is still settling.
    function settleUnreadAnchor() {
        traceViewport("settleUnreadAnchor", "ready=" + viewportReady())
        if (unreadAnchorMessageId.length === 0 || !list.model
                || typeof list.model.indexOf !== "function") {
            finishUnreadAnchorSettle()
            return
        }
        // The user taking over always wins; the settle must never fight a drag.
        if (list.dragging || list.flicking || kineticWheelScroller.interactionActive
                || kineticWheelScroller.kineticActive) {
            userScrolledSinceOpen = true
            finishUnreadAnchorSettle()
            return
        }
        // Still hidden: hold the placement open rather than spend its deadline
        // on a view that cannot lay out. The divider was never placed at all in
        // this state, so giving up here is what left it off screen.
        if (!viewportReady()) {
            if (Date.now() <= unreadAnchorHiddenDeadlineMs) {
                unreadAnchorSettleDeadlineMs = Date.now() + 1000
                unreadAnchorSettleTimer.restart()
                return
            }
            finishUnreadAnchorSettle()
            return
        }

        const index = list.model.indexOf(unreadAnchorMessageId)
        if (index < 0 || index >= list.count) {
            finishUnreadAnchorSettle()
            return
        }

        const item = list.itemAtIndex(index)
        if (item === null || item.pooled || item.messageId !== unreadAnchorMessageId) {
            if (Date.now() <= unreadAnchorSettleDeadlineMs) {
                centerDividerOnIndex(index)
                unreadAnchorSettleTimer.restart()
                return
            }
            finishUnreadAnchorSettle()
            return
        }

        centerDividerOnIndex(index)
        unreadAnchorPositioned = true
        finishUnreadAnchorSettle()
    }

    function finishUnreadAnchorSettle() {
        unreadAnchorSettleTimer.stop()
        unreadAnchorSettleDeadlineMs = 0
        programmaticScroll = false
        lastScrollY = list.contentY
        updateScrollState()
        maybeMarkViewedRead()
    }

    // Drop a settle that has been superseded (a jump, an explicit scroll to the
    // newest message). The caller owns programmaticScroll from here on.
    function cancelUnreadAnchorSettle() {
        unreadAnchorSettleTimer.stop()
        unreadAnchorSettleDeadlineMs = 0
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

        // The unread region runs from the anchor down to row 0, because the rows
        // are held newest-first: everything unread is *at or below* the anchor's
        // index, not above it. Without a locatable anchor only the newest row
        // counts as "viewing".
        let regionStart = 0
        if (unreadAnchorMessageId.length > 0 && list.model && typeof list.model.indexOf === "function") {
            const anchorIndex = list.model.indexOf(unreadAnchorMessageId)
            if (anchorIndex >= 0) {
                regionStart = anchorIndex
            }
        }
        if (newestVisibleRow >= 0 && newestVisibleRow <= regionStart && list.model
                && typeof list.model.messageIdAt === "function") {
            const watermark = list.model.messageIdAt(newestVisibleRow)
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
        cancelUnreadAnchorSettle()
        pendingJumpMessageId = messageId
        pendingJumpDeadlineMs = Date.now() + 1000
        pendingJumpHiddenDeadlineMs = Date.now() + 10000
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
        pendingJumpHiddenDeadlineMs = 0
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
        traceViewport("settlePendingJump", "ready=" + viewportReady())
        if (pendingJumpMessageId.length === 0 || !list.model || typeof list.model.indexOf !== "function") {
            finishPendingJump()
            return
        }
        // A jump can land while ConversationPane still has this pane hidden
        // (the pinned-banner layout settles on its own schedule). Nothing is
        // materialised then, so the 1 s deadline used to expire against a view
        // that could not answer, the jump was abandoned without ever moving or
        // glowing, and the visibility flip that followed scrolled to the newest
        // message. Hold the jump open until the pane can actually lay out.
        if (!viewportReady()) {
            if (Date.now() <= pendingJumpHiddenDeadlineMs) {
                pendingJumpDeadlineMs = Date.now() + 1000
                jumpSettleTimer.restart()
                jumpTimeoutTimer.restart()
                return
            }
            finishPendingJump()
            showReferencedMessageUnavailable()
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
            // Re-position on the way round: this is also what forces the row to
            // materialise. A jump whose first centring happened while the pane
            // was hidden has nothing near the target yet, so waiting alone would
            // never produce the delegate we are waiting for.
            centerOnIndex(index)
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
        traceViewport("jumpToLoadedMessage", "id=" + messageId)
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

    // How many of the rows in [first, last] were received rather than sent.
    // Falls back to the whole range when the model cannot say, which is the
    // old behaviour and never under-counts what the user missed.
    function incomingRowsBetween(first, last) {
        if (!list.model || typeof list.model.isOutgoingAt !== "function") {
            return last - first + 1
        }
        let count = 0
        for (let row = first; row <= last; ++row) {
            if (!list.model.isOutgoingAt(row)) {
                ++count
            }
        }
        return count
    }

    function displayedPendingNewestMessageCount() {
        return pendingNewestMessageCount > 99 ? "99+" : String(pendingNewestMessageCount)
    }

    // Recompute followNewest and fire predictive history prefetch. Cheap: two
    // indexAt probes, no allocations, safe to call on every contentY change.
    // updateScrollState() costs two indexAt() probes plus an itemAtIndex(), each
    // forcing a layout pass, and its callers are contentY and contentHeight,
    // which change several times per frame while scrolling. Coalesce to one run
    // per event-loop turn: nothing reads the result in between, and the probes
    // are cheaper *and* more accurate once the frame's geometry has settled.
    property bool scrollStateQueued: false
    function queueScrollStateUpdate() {
        if (scrollStateQueued) {
            return
        }
        scrollStateQueued = true
        Qt.callLater(applyScrollStateUpdate)
    }

    function applyScrollStateUpdate() {
        scrollStateQueued = false
        updateScrollState()
    }

    function updateScrollState() {
        if (list.count === 0) {
            atNewest = true
            followNewest = true
            pendingNewestMessageCount = 0
            oldestVisibleRow = -1
            newestVisibleRow = -1
            topRowFraction = 0
            return
        }

        // Rows are held newest-first, so the message highest on screen carries
        // the *highest* index and the one against the composer carries the
        // lowest. These two probes are still the visual top and bottom edges;
        // it is only the ordering of what they return that has swapped.
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

        // The oldest visible row is the one highest on screen and so the one
        // with the larger index; the newest visible row is the smaller. Both
        // probes return -1 when they land in the gap between two rows, so each
        // end takes whichever of the two actually answered.
        let newest = -1
        let oldest = -1
        if (topIndex >= 0) {
            newest = topIndex
            oldest = topIndex
        }
        if (bottomIndex >= 0) {
            newest = newest < 0 ? bottomIndex : Math.min(newest, bottomIndex)
            oldest = oldest < 0 ? bottomIndex : Math.max(oldest, bottomIndex)
        }

        if (newest >= 0) {
            newestVisibleRow = newest
        }
        if (oldest >= 0) {
            oldestVisibleRow = oldest
        }

        // Geometry decides first: within followPixelSlack of the bottom counts
        // as parked at the newest message even when the index probe disagrees
        // (it returns -1 whenever it lands in the gap between two rows, which
        // at the very bottom used to drop follow-mode outright).
        const nearBottom = distanceFromBottom() <= followPixelSlack

        if (newest >= 0) {
            // The newest message is row 0 now, so being parked at it is a test
            // against zero rather than against the end of the list.
            atNewest = nearBottom || newest === 0
            followNewest = atNewest || newest <= followRowThreshold
        } else {
            atNewest = nearBottom || list.atYEnd
            followNewest = atNewest
        }

        if (atNewest) {
            pendingNewestMessageCount = 0
        }

        if (shouldPrefetchOlder(oldest)) {
            queueOlderLoadRequest()
        }
        if (shouldPrefetchNewer(newest)) {
            queueNewerLoadRequest()
        }

        maybeMarkViewedRead()
    }

    // History lies at the far end of the list now, so approaching it is a test
    // against count - 1 rather than against zero. The two prefetch predicates
    // swapped their arithmetic when the rows turned over, and nothing else.
    function shouldPrefetchOlder(oldestRow) {
        return !openingChat
                && pendingJumpMessageId.length === 0
                && oldestRow >= 0
                && canLoadOlderMessages
                && !loadingOlderMessages
                && oldestRow >= list.count - 1 - prefetchRowThreshold
    }

    function queueOlderLoadRequest() {
        if (olderLoadRequestQueued) {
            return
        }
        olderLoadRequestQueued = true
        Qt.callLater(() => {
            olderLoadRequestQueued = false
            if (shouldPrefetchOlder(oldestVisibleRow)) {
                captureOlderViewport()
                loadOlderMessagesRequested()
            }
        })
    }

    function shouldPrefetchNewer(newestRow) {
        return !openingChat
                && pendingJumpMessageId.length === 0
                && newestRow >= 0
                && canLoadNewerMessages
                && !loadingNewerMessages
                && newestRow <= prefetchRowThreshold
    }

    function queueNewerLoadRequest() {
        if (newerLoadRequestQueued) {
            return
        }
        newerLoadRequestQueued = true
        Qt.callLater(() => {
            newerLoadRequestQueued = false
            if (shouldPrefetchNewer(newestVisibleRow)) {
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
        traceViewport("afterModelReset", "loadingMessages=" + loadingMessages)
        if (loadingMessages) {
            return
        }
        lastTopIndex = -1
        oldestVisibleRow = -1
        newestVisibleRow = -1
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

    // Handing the conversation to another pane. Anything this one was still in
    // the middle of belongs to a view nobody is looking at: a jump left pending
    // keeps programmaticScroll latched, which is what stopped the transcript
    // responding to the wheel, and a glow left half-played stays lit forever.
    onIsCurrentPaneChanged: {
        if (isCurrentPane) {
            return
        }
        if (pendingJumpMessageId.length > 0) {
            finishPendingJump()
        }
        cancelUnreadAnchorSettle()
        clearReplyGlow()
        programmaticScroll = false
    }

    onChatIdChanged: {
        openStampOwed = Whatevr.ProtocolController.perfLogging && chatId.length > 0
        clearReplyGlow()
        if (pendingJumpMessageId.length > 0) {
            finishPendingJump()
        }
        // A settle can still be holding programmaticScroll for the chat we are
        // leaving; releasing it here keeps user-scroll detection alive in the
        // chat we are entering.
        cancelUnreadAnchorSettle()
        programmaticScroll = false
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

    // Becoming visible is the first moment this pane can lay out, so it is where
    // a placement that was owed while hidden finally happens. Anything owed wins
    // over following the newest message — that fallback firing on the visibility
    // flip is what yanked a starred jump to the bottom "after a moment".
    onVisibleChanged: {
        traceViewport("onVisibleChanged")
        if (!visible) {
            return
        }
        if (pendingJumpMessageId.length > 0) {
            Qt.callLater(settlePendingJump)
        } else if (unreadAnchorMessageId.length > 0) {
            positionAtUnreadAnchor()
        } else {
            // Nothing owed and nothing unread: open at the newest message, even
            // if this pane was left scrolled up into history. A parked pane
            // keeps its viewport along with its rows, so gating this on
            // followNewest meant a chat you had read and scrolled up in
            // re-opened in the middle of last week. Coming back to a chat with
            // nothing unread in it is arriving at the present.
            Qt.callLater(scrollToNewest)
        }
    }

    onLoadingMessagesChanged: if (!loadingMessages && chatId.length > 0) Qt.callLater(afterModelReset)

    ListView {
        id: list

        objectName: "messageList"

        anchors.fill: parent
        clip: true

        // The transcript is drawn from the bottom up: row 0 is the newest
        // message and sits against the composer, and the rows climb away from
        // it into history.
        //
        // This is the whole reason the model is held newest-first, and it fixes
        // a class of problem rather than an instance of one. A ListView's
        // contentHeight is an *estimate* built from the average height of the
        // rows it has actually built, and it is revised every time another one
        // materialises. Laid out top-down, that revision lands above the reader
        // and shoves everything below it, which is why this file grew a
        // scroll-anchoring apparatus (noteRowResized, applyBottomRepin, the
        // delayed contentHeight-to-height binding, the settle timers) whose
        // entire job was to undo the shove. Laid out bottom-up, the estimate is
        // revised at the far end of the list, thousands of pixels up in history
        // where nobody is looking, and the newest message never moves because
        // it is the fixed point the layout is measured from.
        //
        // Two things stay exactly as they were, which is what makes this
        // tractable: contentY is still a plain top-down coordinate over the
        // content, so every geometric test in here (distanceFromBottom, the
        // wheel scroller's bounds, atYEnd) reads the same. What inverts is only
        // what a row *index* means, and Qt's naming is genuinely confusing about
        // it: the model's beginning (index 0, the newest message) is at the
        // geometric end, so positionViewAtBeginning() is what scrolls to the
        // bottom of the screen.
        verticalLayoutDirection: ListView.BottomToTop

        // A viewport-height change (the pinned banner appearing above, the
        // composer growing below) has to be answered differently depending on
        // where the reader is, and a bottom-up list makes both answers explicit.
        //
        // Parked at the newest message, the bottom is where they want to stay,
        // so re-pin. Scrolled up in history, the bottom is not where they are
        // looking: the layout is measured from it, so shrinking the viewport
        // slides everything they *are* looking at by the same amount. Undoing
        // that by the height delta is the one piece of scroll anchoring this
        // list still needs, and unlike the old apparatus it fires on an
        // isolated, exactly-known event rather than on every revised estimate.
        property real lastViewportHeight: 0
        onHeightChanged: {
            const shrankBy = height - lastViewportHeight
            lastViewportHeight = height

            if (!root.followNewest && !root.programmaticScroll
                    && root.pendingJumpMessageId.length === 0
                    && list.count > 0 && Math.abs(shrankBy) >= 0.5) {
                const wasProgrammatic = root.programmaticScroll
                root.programmaticScroll = true
                list.contentY += shrankBy
                root.programmaticScroll = wasProgrammatic
                return
            }

            if (root.followNewest && !root.programmaticScroll
                    && root.pendingJumpMessageId.length === 0) {
                // Re-pin synchronously so no drifted intermediate frame is
                // painted (the late pinned-banner pop-in on first open would
                // otherwise flash the viewport off the bottom); the deferred
                // scrollToNewest then settles followNewest/atNewest state.
                // Viewport-height changes are isolated events, not the burst
                // that contentHeight sees, so this one stays inline — except
                // while the chat is opening, where its own positioning runs
                // straight after and this would only add a forced layout.
                root.traceViewport("list.onHeightChanged:repin")
                if (list.count > 0 && !root.openingChat) {
                    root.programmaticScroll = true
                    list.positionViewAtBeginning()
                    Qt.callLater(() => { root.programmaticScroll = false })
                }
                Qt.callLater(root.scrollToNewest)
            }
        }

        // Once the daemon's local history is fully loaded, the strip at the
        // visual top offers pulling older messages from the phone, or states
        // that nothing older exists there.
        //
        // The list's `footer`, not its `header`, and for the same reason
        // positionViewAtBeginning() scrolls to the bottom: these two are named
        // after the ends of the *model*, and the model runs newest-first. The
        // header would draw this under the newest message, which is the one
        // place in the transcript where older history certainly is not.
        footer: Item {
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
        // Cache-buffer delegates are incubated asynchronously, so every row
        // prepared here is one fewer synchronous creation while the user is
        // scrolling (those are what stall frames). Two viewports each way, not
        // four: every row in the band is a live delegate that re-evaluates its
        // bindings on a model change. Cached text rows are a Text node plus a
        // few rectangles; the bound cost left in the band is thumbnail decodes,
        // capped per image.
        //
        // The band is closed while a chat is opening, and that is the whole
        // point. Two viewports each way is five viewports of rows, and an open
        // starts with an empty reuse pool, so leaving the band open meant
        // building every one of them before the first frame could be painted:
        // measured, 55 delegates for a window whose viewport shows 20. Opening
        // with the band shut builds the viewport, paints it, and lets the band
        // fill afterwards out of idle time, which is what "incubated
        // asynchronously" was supposed to buy in the first place.
        //
        // Bounded by its own timer as well as by openingChat, and not by
        // openingChat alone. That flag is latched by whichever of several paths
        // finishes the open, and it has been seen to stay set for seconds when
        // none of them runs; a chat scrolling with no cache band at all is a
        // far worse bargain than the one this is trying to win. The timer is
        // the guarantee: whatever else happens, the band is open a quarter of a
        // second after the chat changed.
        // Deliberately *not* shrunk while flinging, though the arithmetic says
        // it should be. A fast fling travels up to 4,000px in a frame, which
        // invalidates the whole band every frame, so on paper a smaller band
        // during a fling is less work per frame. Tried, measured, reverted: it
        // did not move the stalls, and restoring the band when the fling
        // settled built forty rows in one turn, which stalls exactly when the
        // reader has stopped and is looking. Whatever the long frames are, the
        // band is not it.
        readonly property real steadyCacheBuffer: Math.max(height * 2, Kirigami.Units.gridUnit * 60)
        cacheBuffer: (root.openingChat && cacheBandDelay.running) ? 0 : steadyCacheBuffer
        reuseItems: true

        Timer {
            id: cacheBandDelay

            interval: 250
            running: false
            // The first chat is opened by the pane being built around it, not by
            // chatId changing, and it is the one open with nothing warm behind
            // it. Started here rather than bound to chatId so that restart()
            // below is not fighting a binding for ownership of `running`.
            Component.onCompleted: if (root.chatId.length > 0) start()
        }

        Connections {
            target: root

            function onChatIdChanged() {
                if (root.chatId.length > 0) {
                    cacheBandDelay.restart()
                } else {
                    cacheBandDelay.stop()
                }
            }
        }

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

            // Every model role ChatBubble renders is declared `required` on
            // ChatBubble itself, named after the role, so ListView assigns it
            // directly in C++. What used to live here — ~60 bindings of the
            // form `String(model.x || "")` — was one JS evaluation plus a type
            // coercion per role per row, and it was why this file had the worst
            // ahead-of-time compilation coverage in the codebase (DN9).
            // Only genuine view state is passed down now.
            property bool pooledByListView: false
            readonly property bool insideViewport: !pooledByListView
                                                   && y + height >= list.contentY
                                                   && y <= list.contentY + list.height

            listWidth: list.width
            textExpanded: root.messageTextExpanded(messageId)
            readMoreTextWidth: readMoreSharedMetrics.advanceWidth
            selectionModeActive: root.selectionActive
            selected: root.selectionRevision >= 0 && root.isSelected(messageId)
            pooled: pooledByListView
            activeInViewport: insideViewport
            fastFlicking: list.fastFlicking
            unreadSeparatorCount: root.unreadAnchorMessageId.length > 0 && messageId === root.unreadAnchorMessageId
                                  ? root.unreadAnchorCount : 0
            clearSelectionGeneration: root.clearSelectionGeneration
            activeSelectionMessageId: root.activeSelectionMessageId
            onConversationFocusRequested: root.conversationFocusRequested()
            onReplyGlowRequested: root.playReplyGlow(messageDelegate)
            onMessageSelectionClaimed: messageId => root.claimMessageSelection(messageId)
            onTypeIntoComposerRequested: text => root.typeIntoComposerRequested(text)
            onReplyRequested: (messageId, senderName, text, mediaKind, mediaMimeType, outgoing) => root.replyToMessageRequested(messageId, senderName, text, mediaKind, mediaMimeType, outgoing)
            onReplyPreviewActivated: messageId => root.jumpToReplyTarget(messageId)
            onReadMoreRequested: messageId => root.openMessageContent(messageId)
            onImageActivated: (messageId, localPath) => root.imageViewRequested(messageId, localPath)
            onVideoActivated: (messageId, localPath, streamUrl, streamId, kind, durationSecs, startAt) => root.videoViewRequested(messageId, localPath, streamUrl, streamId, kind, durationSecs, startAt)
            onAlbumItemActivated: (albumMessageId, index) => root.albumViewRequested(albumMessageId, index)
            onMentionClicked: jid => root.mentionClicked(jid)
            onMentionAllClicked: root.mentionAllClicked()
            onContextMenuRequested: (posX, posY) => root.openContextMenu(messageDelegate, posX, posY)
            onReactionPickerRequested: (posX, posY) => root.openQuickReactions(messageDelegate, posX, posY)
            onReactionToggleRequested: emoji => root.reactToMessage(messageDelegate.messageId, emoji)
            onReactionDetailsRequested: root.openReactionDetails(messageDelegate)
            onPollVotersRequested: optionIndex => root.openPollVoters(messageDelegate, optionIndex)
            onEventResponsesRequested: response => root.openEventResponses(messageDelegate, response)
            onSelectionToggleRequested: root.toggleSelected(messageDelegate.messageId)
            onDaySelectionToggleRequested: root.toggleDaySelection(messageDelegate.messageId)

            // The scroll-anchoring bookkeeping that used to live here (two
            // properties, an onYChanged and an onHeightChanged on every row in
            // the chat) went with the anchoring itself: a bottom-up list holds
            // the reader still on its own. See the note by noteRowResized.
            ListView.onPooled: pooledByListView = true
            ListView.onReused: pooledByListView = false
        }

        onContentYChanged: {
            if (rowScrollBar.dragging) {
                noteThumbDrag()
            }
            // Whether we are on the bottom has to be answered from the position
            // *before* the content changes height, so it is recorded on every
            // move rather than asked for after the fact.
            root.refreshBottomPin()
            // While the chat is still opening the viewport is not the user's
            // yet — the open positions it itself — and updateScrollState()
            // costs two indexAt() probes plus an itemAtIndex(), each forcing a
            // layout pass. Nothing reads the result until the open finishes.
            if (!root.openingChat) {
                root.queueScrollStateUpdate()
            }
            root.noteScroll()
        }
        onContentHeightChanged: {
            // Content-height revisions (late row parses, thumbnails resolving
            // their intrinsic size, a card measuring its own text) move the
            // bottom of the transcript. A view that was sitting on that bottom
            // has to go with it, or the reader is left short of the newest
            // message with nothing to tell them why. pinnedToBottom is the
            // durable half of that; the post-open settle window is the other,
            // covering the moments while a chat is still placing itself and the
            // viewport has not yet come to rest anywhere.
            //
            // Coalesced through the event queue rather than run inline: rows
            // settle their heights in bursts, and positionViewAtBeginning() forces a
            // full layout every time it is called. One re-pin per frame is
            // indistinguishable on screen and turns a burst of forced layouts
            // into a single one.
            if ((root.pinnedToBottom || bottomSettleTimer.running)
                    && root.followNewest
                    && root.pendingJumpMessageId.length === 0
                    && list.count > 0) {
                root.queueBottomRepin()
            }
            if (!root.openingChat) {
                root.queueScrollStateUpdate()
            }
        }
        onMovementEnded: root.updateScrollState()
        // Touch and kinetic drags come through the Flickable rather than the
        // wheel scroller, and they are the reader taking over just the same.
        onDraggingChanged: {
            if (dragging && root.pendingJumpMessageId.length > 0) {
                root.finishPendingJump()
            }
            if (dragging) {
                root.cancelUnreadAnchorSettle()
            }
        }

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
                // A message arriving at the live edge lands at row 0, because
                // the rows are held newest-first. Older extends land at the far
                // end instead; their viewport is restored when
                // loadingOlderMessages clears.
                if (!root.openingChat && first === 0) {
                    if (root.followNewest && root.pendingJumpMessageId.length === 0) {
                        Qt.callLater(root.scrollToNewest)
                    } else {
                        // The badge answers "what did I miss", so it counts
                        // received messages only — your own sends are not news.
                        root.pendingNewestMessageCount = Math.min(
                            100, root.pendingNewestMessageCount + root.incomingRowsBetween(first, last))
                    }
                }
                // Rows older than the anchor now land at indices *above* it,
                // not below, so the test that says "history arrived behind
                // where the reader is" turns over with everything else.
                if (root.phoneHistoryAnchorActive && list.model
                        && typeof list.model.indexOf === "function"
                        && last > list.model.indexOf(root.phoneHistoryViewportAnchorId)) {
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

        function onChatExported(destPath) {
            root.showNotification(Whatevr.I18n.i18nc("@info:status chat transcript saved", "Chat exported"))
        }

        function onUnreadAnchorChanged() {
            root.traceViewport("onUnreadAnchorChanged")
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
                if (root.unreadAnchorMessageId.length > 0) {
                    // An anchor the model does not (yet) hold: fall back to the
                    // newest message rather than sitting latched in openingChat.
                    if (!root.positionAtUnreadAnchor()
                            && !root.unreadAnchorResolving) {
                        root.scrollToNewest()
                        root.openingChat = false
                    }
                } else if (root.openingChat && !root.unreadAnchorResolving) {
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

        function onMessageSent() {
            if (!Whatevr.Settings.snapToBottomOnSend || root.chatId.length === 0) {
                return
            }
            // jumpToBottom() as well as the scroll: while the window is anchored
            // mid-history the sent message is not delivered into it at all
            // (PROTOCOL.md, "Windows"), so there would be nothing to scroll to.
            // Same pair the go-to-bottom button uses.
            root.cancelUnreadAnchorSettle()
            if (root.pendingJumpMessageId.length > 0) {
                root.finishPendingJump()
            }
            kineticWheelScroller.stopKinetic()
            if (list.flicking) list.cancelFlick()
            Whatevr.ProtocolController.jumpToBottom()
            root.scrollToNewest()
        }

        function onMessageJumpReady(messageId) {
            // Broadcast to every warm pane; only the one on screen may act.
            if (!root.isCurrentPane) {
                return
            }
            // A jump started on the C++ side (showMessageInChat: starred lists,
            // global search results) never went through jumpToReplyTarget, so
            // this view has no pendingJumpMessageId and every guard below would
            // decline it — the chat opened at the newest message with no glow.
            // Adopt it here instead. This runs synchronously inside the
            // subscription's ready handler, ahead of the Qt.callLater-queued
            // afterModelReset(), which then sees the pending jump and leaves the
            // viewport alone rather than scrolling to the newest message.
            if (root.pendingJumpMessageId !== messageId) {
                root.beginProgrammaticJump(messageId)
            }
            root.jumpToLoadedMessage(messageId)
        }

        function onMessageJumpUnavailable(messageId) {
            // Same broadcast, and the same reason a parked pane must stay out of
            // it: it would be answering for rows it does not hold.
            if (!root.isCurrentPane) {
                return
            }
            // An adopted jump has nothing pending here yet, but the user still
            // asked for that message and deserves to be told it is gone.
            if (root.pendingJumpMessageId.length > 0 && root.pendingJumpMessageId !== messageId) {
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

        // The reader taking over always wins. Only a scrollbar drag used to say
        // so, so a jump still settling kept programmaticScroll latched and went
        // on re-centring the view under the wheel: the transcript read as stuck.
        onScrollStarted: {
            if (root.pendingJumpMessageId.length > 0) {
                root.finishPendingJump()
            }
            root.cancelUnreadAnchorSettle()
        }
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

        // Translated from row indices into rows on the screen here, once, so the
        // scrollbar never has to know which way the model runs. Older messages
        // are the ones above the viewport, and they are the ones with indices
        // above the oldest visible row.
        visibleSpan: root.oldestVisibleRow >= 0 && root.newestVisibleRow >= 0
                     ? Math.max(1, root.oldestVisibleRow - root.newestVisibleRow + 1)
                     : 1
        rowsAbove: root.oldestVisibleRow >= 0
                   ? Math.max(0, list.count - 1 - root.oldestVisibleRow + root.topRowFraction)
                   : 0

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

        onDragPositionRequested: rowsAbove => {
            // Back from rows-on-screen into a row index: the row wanted at the
            // visual top is the one with exactly this many older rows above it.
            const whole = Math.floor(rowsAbove)
            const index = Math.max(0, Math.min(list.count - 1, list.count - 1 - whole))
            const fraction = rowsAbove - whole
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
            // The same path as the go-to-bottom button: dragging the thumb to
            // the very bottom must also re-enter follow mode, or the view sat
            // at the newest row without following new arrivals.
            root.pendingNewestMessageCount = 0
            kineticWheelScroller.stopKinetic()
            if (list.flicking) list.cancelFlick()
            Whatevr.ProtocolController.jumpToBottom()
            root.scrollToNewest()
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

    // Voice notes chain: finishing one plays the next one down the chat, the
    // way WhatsApp does.
    Connections {
        target: Whatevr.AudioPlayer

        function onFinished(messageId) {
            if (!Whatevr.Settings.advanceVoiceMessages) {
                return
            }
            // messageListModel is the *open* chat, so without this a note
            // finishing in a chat you have since left would chain into whatever
            // voice note happens to sit below in the chat you are reading now.
            if (Whatevr.AudioPlayer.chatId !== Whatevr.ProtocolController.selectedChatId) {
                return
            }
            const next = Whatevr.ProtocolController.messageListModel.nextVoiceMessage(messageId)
            if (!next || !next.messageId || next.localPath.length === 0) {
                return
            }
            Whatevr.AudioPlayer.play(next.messageId,
                                     Whatevr.ProtocolController.localFileUrl(next.localPath),
                                     next.durationSecs,
                                     {
                                         "chat_id": Whatevr.ProtocolController.selectedChatId,
                                         "chat_name": Whatevr.ProtocolController.selectedChatName,
                                         "sender_name": next.isOutgoing ? "" : next.senderName,
                                         "avatar_path": next.isOutgoing ? "" : next.avatarPath,
                                         "file_name": "",
                                         "is_voice": true,
                                         "is_outgoing": next.isOutgoing,
                                         "waveform": next.waveform ? next.waveform : []
                                     })
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
        readonly property bool ctxIsDocument: ctxMediaKind === "document"
        readonly property bool ctxIsPlayable: ctxMediaKind === "video" || ctxMediaKind === "gif"
                                              || ctxMediaKind === "video_note" || ctxMediaKind === "voice"
                                              || ctxMediaKind === "audio"
        readonly property string ctxMediaFileName: ctxValid ? String(ctx.mediaFileName || "") : ""
        readonly property real ctxTimestampUnix: ctxValid ? Number(ctx.timestampUnix || 0) : 0
        readonly property bool ctxMediaDownloading: ctxValid && Boolean(ctx.mediaDownloading)
        readonly property string ctxMediaDownloadError: ctxValid ? String(ctx.mediaDownloadError || "") : ""
        readonly property int ctxSenderDevice: ctxValid ? Number(ctx.senderDevice || 0) : 0
        // Anything with media that is not on disk and not already coming down.
        readonly property bool ctxCanDownload: !ctxIsRevoked
                                               && !ctxHasMediaFile
                                               && !ctxMediaDownloading
                                               && ctxMediaKind.length > 0
                                               && ctxMediaKind !== "unsupported"
        readonly property bool ctxHasText: ctxText.length > 0 && !ctxIsRevoked
        readonly property bool ctxIsStarred: ctxValid && Boolean(ctx.isStarred)
        readonly property bool ctxIsPinned: ctxValid && Boolean(ctx.isPinned)
        readonly property bool ctxIsEdited: ctxValid && Boolean(ctx.isEdited)
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
            icon.name: "view-history-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu show previous versions of an edited message", "Edit history")
            visible: messageContextMenu.ctxIsEdited
            onTriggered: editHistoryDialog.openFor(messageContextMenu.ctxMessageId, messageContextMenu.ctxText)
        }

        MenuItem {
            icon.name: "mail-forward-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Forward…")
            visible: !messageContextMenu.ctxIsRevoked
            onTriggered: root.openForwardPicker([messageContextMenu.ctxMessageId])
        }

        MenuSeparator {
            // Any media at all opens the copy/open/save group below, not just
            // images: a video or a document has the same actions.
            visible: messageContextMenu.ctxHasText
                     || messageContextMenu.ctxLinks.length > 0
                     || messageContextMenu.ctxHasMediaFile
                     || messageContextMenu.ctxCanDownload
                     || messageContextMenu.ctxMediaDownloading
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
            icon.name: "edit-copy-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Copy File")
            // Puts the file itself on the clipboard, so it can be pasted into a
            // file manager or another chat app.
            visible: messageContextMenu.ctxHasMediaFile
                     && !messageContextMenu.ctxIsImage
                     && !messageContextMenu.ctxIsSticker
            onTriggered: {
                Whatevr.ProtocolController.copyFileToClipboard(messageContextMenu.ctxMediaLocalPath)
                root.showNotification(Whatevr.I18n.i18nc("@info:status", "File copied"))
            }
        }

        MenuItem {
            icon.name: "document-open-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Open")
            // Playable kinds open in the app itself; anything else is the
            // system's business.
            visible: messageContextMenu.ctxHasMediaFile
                     && !messageContextMenu.ctxIsImage
                     && !messageContextMenu.ctxIsSticker
                     && !messageContextMenu.ctxIsPlayable
            onTriggered: Whatevr.ProtocolController.openLocalFile(messageContextMenu.ctxMediaLocalPath)
        }

        MenuItem {
            icon.name: "document-open-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Open Externally")
            // For the kinds the app plays itself, opening in the desktop's own
            // player is still worth offering.
            visible: messageContextMenu.ctxHasMediaFile
                     && (messageContextMenu.ctxIsPlayable || messageContextMenu.ctxIsImage)
            onTriggered: Whatevr.ProtocolController.openLocalFile(messageContextMenu.ctxMediaLocalPath)
        }

        MenuItem {
            icon.name: "document-save-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Save As…")
            visible: messageContextMenu.ctxHasMediaFile
            onTriggered: saveMediaDialog.openFor(messageContextMenu.ctxMediaLocalPath,
                                                 messageContextMenu.ctxMediaKind,
                                                 messageContextMenu.ctxMediaFileName,
                                                 messageContextMenu.ctxTimestampUnix)
        }

        MenuItem {
            icon.name: "folder-download-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Download")
            visible: messageContextMenu.ctxCanDownload && messageContextMenu.ctxMediaDownloadError.length === 0
            onTriggered: Whatevr.ProtocolController.downloadMessageMedia(messageContextMenu.ctxMessageId)
        }

        MenuItem {
            icon.name: "view-refresh-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Retry Download")
            // A failed fetch leaves a durable error on the row, and nothing in
            // the menu used to be able to clear it.
            visible: messageContextMenu.ctxCanDownload && messageContextMenu.ctxMediaDownloadError.length > 0
            onTriggered: Whatevr.ProtocolController.downloadMessageMedia(messageContextMenu.ctxMessageId)
        }

        MenuItem {
            icon.name: "process-stop-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu", "Cancel Download")
            visible: messageContextMenu.ctxMediaDownloading
            onTriggered: Whatevr.ProtocolController.cancelMessageMediaDownload(messageContextMenu.ctxMessageId)
        }

        MenuSeparator {
            visible: messageContextMenu.ctxIsSticker
        }

        MenuItem {
            icon.name: messageContextMenu.ctxStickerFavorite ? "starred-symbolic" : "non-starred-symbolic"
            text: messageContextMenu.ctxStickerFavorite
                  ? Whatevr.I18n.i18nc("@action:inmenu", "Remove from Favorite Stickers")
                  : Whatevr.I18n.i18nc("@action:inmenu", "Add to Favorite Stickers")
            // sticker.favorite takes a cache key or a message id; the wire
            // carries no cache key for a message, so the id is what identifies
            // it. Keying off the cache key made this entry unreachable.
            visible: messageContextMenu.ctxIsSticker && messageContextMenu.ctxMessageId.length > 0
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
            onTriggered: root.openMessageInfo(messageContextMenu.ctxMessageId, messageContextMenu.ctxSenderDevice)
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

        // A document is saved under the name it was sent with, into Documents;
        // only photos belong in Pictures. The cache filename is a message id,
        // which is no use to anyone in a file manager.
        function openFor(path, kind, fileName, timestampUnix) {
            sourcePath = path
            const cacheName = path.substring(path.lastIndexOf("/") + 1)
            let base = (fileName && fileName.length > 0) ? fileName : cacheName
            if (!fileName || fileName.length === 0) {
                // Voice notes and video messages carry no name of their own, and
                // the cache filename is a message id. A dated name is what makes
                // the file findable later.
                const prefixes = {
                    "voice": "Voice message",
                    "audio": "Audio",
                    "video": "Video",
                    "gif": "GIF",
                    "video_note": "Video message",
                    "image": "Photo"
                }
                const prefix = prefixes[kind]
                if (prefix) {
                    const dot = cacheName.lastIndexOf(".")
                    const extension = dot > 0 ? cacheName.substring(dot) : ""
                    const when = new Date((timestampUnix || 0) > 0
                                          ? timestampUnix * 1000
                                          : Date.now())
                    base = prefix + " " + Qt.formatDateTime(when, "yyyy-MM-dd hh.mm.ss") + extension
                }
            }
            let location = Platform.StandardPaths.PicturesLocation
            if (kind === "video" || kind === "gif" || kind === "video_note") {
                location = Platform.StandardPaths.MoviesLocation
            } else if (kind === "voice" || kind === "audio") {
                location = Platform.StandardPaths.MusicLocation
            } else if (kind === "document") {
                location = Platform.StandardPaths.DocumentsLocation
            }
            // A configured media folder wins over the per-kind XDG default.
            const preferred = Whatevr.Settings.mediaSaveDirectory
            const directory = preferred.length > 0
                ? preferred
                : Platform.StandardPaths.writableLocation(location)
            currentFile = directory + "/" + base
            open()
        }

        onAccepted: {
            if (Whatevr.ProtocolController.saveMediaAs(sourcePath, file)) {
                root.showNotification(Whatevr.I18n.i18nc("@info:status", "File saved"))
            }
        }
    }

    Platform.FileDialog {
        id: exportChatDialog

        property string exportChatId: ""

        fileMode: Platform.FileDialog.SaveFile
        title: Whatevr.I18n.i18nc("@title:window save a chat transcript", "Export chat")

        // Official clients suggest "WhatsApp Chat with <name>.txt" into
        // Documents; the name is sanitized to one path segment.
        function openFor(chatId, chatName) {
            exportChatId = chatId
            let base = "WhatsApp Chat with " + (chatName || chatId)
            base = base.replace(/[\/\\]/g, "_").trim()
            if (base.length === 0) {
                base = "WhatsApp Chat"
            }
            const preferred = Whatevr.Settings.mediaSaveDirectory
            const directory = preferred.length > 0
                ? preferred
                : Platform.StandardPaths.writableLocation(Platform.StandardPaths.DocumentsLocation)
            currentFile = directory + "/" + base + ".txt"
            open()
        }

        onAccepted: {
            Whatevr.ProtocolController.exportChat(exportChatId, file)
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

    EditHistoryDialog {
        id: editHistoryDialog
    }

    PollVotersDialog {
        id: pollVotersDialog
    }

    EventResponsesDialog {
        id: eventResponsesDialog
    }

    MessageContentDialog {
        id: messageContentDialog
    }
}
