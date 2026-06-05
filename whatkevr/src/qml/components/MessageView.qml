pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Item {
    id: root

    Kirigami.Theme.colorSet: Kirigami.Theme.View

    property string chatId: ""
    property alias model: list.model
    property bool loadingOlderMessages: false
    property bool showLoadingOlderMessages: false
    property bool canLoadOlderMessages: false

    // The list is inverted (newest at index 0, rendered bottom-to-top). Loading
    // older history therefore appends at the *end* of the model, which can never
    // shift the anchored viewport, so no scroll-position restoration is needed.
    //
    // followNewest: true while the newest message is in view, so freshly arrived
    // messages keep the view pinned to the bottom. Detection is index-based
    // (orientation-agnostic): the newest message is always index 0 and the
    // oldest is always the highest index, regardless of the physical edge they
    // map to.
    property bool openingChat: false
    property bool followNewest: true
    property bool atNewest: true
    property int pendingNewestMessageCount: 0

    // How close (in rows) to the newest message we must be to keep following it.
    property int followRowThreshold: 2
    // Start fetching older history once the topmost visible row is within this
    // many rows of the oldest loaded message, so the next page usually arrives
    // before the user reaches the edge.
    property int prefetchRowThreshold: 24

    property int clearSelectionGeneration: 0
    property string activeSelectionMessageId: ""

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
    // Set while we move the viewport ourselves (chat open, scroll-to-newest) so
    // those programmatic jumps don't flash the floating date pill.
    property bool programmaticScroll: false
    property real lastScrollY: 0
    property string pendingJumpMessageId: ""
    property double pendingJumpDeadlineMs: 0

    signal loadOlderMessagesRequested()
    signal conversationFocusRequested()
    signal typeIntoComposerRequested(string text)
    signal replyToMessageRequested(string messageId, string senderName, string text, string mediaKind, string mediaMimeType, bool outgoing)

    onLoadingOlderMessagesChanged: {
        if (loadingOlderMessages) {
            loadingOlderMessagesDelayTimer.restart()
            return
        }

        loadingOlderMessagesDelayTimer.stop()
        showLoadingOlderMessages = false
    }

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

    function scrollToNewest() {
        if (list.count > 0) {
            programmaticScroll = true
            list.positionViewAtBeginning()
            floatingDateActive = false
            floatingDateIdleTimer.stop()
            Qt.callLater(() => { root.programmaticScroll = false })
        }
        pendingNewestMessageCount = 0
        followNewest = true
        atNewest = true
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
        list.cancelFlick()
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

    function itemIsVisible(item) {
        return item !== null
               && !item.pooled
               && item.messageId === pendingJumpMessageId
               && item.y + item.height > list.contentY
               && item.y < list.contentY + list.height
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

        item.triggerReplyGlow()
        finishPendingJump()
    }

    function jumpToReplyTarget(messageId) {
        if (messageId.length === 0) {
            showReferencedMessageUnavailable()
            return
        }
        beginProgrammaticJump(messageId)
        Whatevr.AppController.jumpToMessage(messageId)
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

        const currentItem = list.itemAtIndex(index)
        if (itemIsVisible(currentItem)) {
            currentItem.triggerReplyGlow()
            finishPendingJump()
            return
        }

        programmaticScroll = true
        floatingDateActive = false
        floatingDateIdleTimer.stop()
        kineticWheelScroller.stopKinetic()
        list.cancelFlick()
        list.positionViewAtIndex(index, ListView.Center)
        list.forceLayout()
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
            atNewest = lo === 0
            followNewest = lo <= followRowThreshold
        } else {
            atNewest = list.atYBeginning
            followNewest = list.atYBeginning
        }

        if (atNewest) {
            pendingNewestMessageCount = 0
        }

        if (!openingChat
                && pendingJumpMessageId.length === 0
                && hi >= 0
                && canLoadOlderMessages
                && !loadingOlderMessages
                && hi >= list.count - 1 - prefetchRowThreshold) {
            loadOlderMessagesRequested()
        }
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
        if (floatingDateText.length > 0) {
            floatingDateActive = true
            floatingDateIdleTimer.restart()
        }
    }

    function afterModelReset() {
        lastTopIndex = -1
        if (pendingJumpMessageId.length === 0) {
            scrollToNewest()
        } else {
            programmaticScroll = true
            floatingDateActive = false
            floatingDateIdleTimer.stop()
        }
        floatingDateActive = false
        floatingDateIdleTimer.stop()
        openingChat = false
        if (pendingJumpMessageId.length === 0) {
            followNewest = true
            atNewest = true
            pendingNewestMessageCount = 0
        }
        Qt.callLater(updateScrollState)
    }

    onChatIdChanged: {
        if (pendingJumpMessageId.length > 0) {
            finishPendingJump()
        }
        pendingNewestMessageCount = 0
        atNewest = true
        followNewest = true
        if (chatId.length === 0) {
            openingChat = false
        } else {
            openingChat = true
            Qt.callLater(scrollToNewest)
        }
    }

    onVisibleChanged: {
        if (visible && followNewest && pendingJumpMessageId.length === 0) {
            Qt.callLater(scrollToNewest)
        }
    }

    ListView {
        id: list

        anchors.fill: parent
        clip: true

        // Newest at the bottom; older history stacks upward off the top edge.
        verticalLayoutDirection: ListView.BottomToTop

        spacing: Kirigami.Units.smallSpacing / 2
        // Generous cache so fast flicks through history stay populated.
        cacheBuffer: Math.max(height * 0.6, Kirigami.Units.gridUnit * 40)
        reuseItems: true

        // True while flinging faster than ~1.25 viewport-heights per second.
        // Delegates use this to hold off full-resolution media decoding so the
        // scroll stays smooth; it drops back to false shortly after the fling
        // slows, at which point media upgrades to full-res.
        property bool fastFlicking: false
        readonly property real fastFlickThreshold: Math.max(Kirigami.Units.gridUnit * 60, height * 1.25)
        onVerticalVelocityChanged: {
            if (Math.abs(verticalVelocity) > fastFlickThreshold) {
                fastFlicking = true
                flickSettleTimer.restart()
            }
        }

        Timer {
            id: flickSettleTimer
            interval: 90
            onTriggered: list.fastFlicking = false
        }

        flickableDirection: Flickable.VerticalFlick
        boundsBehavior: Flickable.StopAtBounds
        boundsMovement: Flickable.StopAtBounds
        acceptedButtons: Qt.NoButton
        flickDeceleration: 4000
        maximumFlickVelocity: 8000

        ScrollBar.vertical: DiscreetScrollBar {}

        delegate: ChatBubble {
            id: messageDelegate

            required property var model
            property bool pooledByListView: false
            readonly property bool insideViewport: !pooledByListView
                                                   && y + height >= list.contentY
                                                   && y <= list.contentY + list.height

            listWidth: list.width
            messageId: String(model.messageId || "")
            body: String(model.text || "")
            layoutBody: String(model.layoutText || "")
            emojiOnlyCount: Number(model.emojiOnlyCount || 0)
            hasRichText: Boolean(model.hasRichText)
            richText: String(model.richText || "")
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
            mediaIntrinsicWidth: Number(model.mediaWidth || 0)
            mediaIntrinsicHeight: Number(model.mediaHeight || 0)
            mediaAnimated: Boolean(model.mediaAnimated)
            pooled: pooledByListView
            activeInViewport: insideViewport
            fastFlicking: list.fastFlicking
            mediaDownloading: Boolean(model.mediaDownloading)
            mediaDownloadError: String(model.mediaDownloadError || "")
            replyToMessageId: String(model.replyToMessageId || "")
            replyToSenderName: String(model.replyToSenderName || "")
            replyToText: String(model.replyToText || "")
            replyToMediaKind: String(model.replyToMediaKind || "")
            replyToMediaMimeType: String(model.replyToMediaMimeType || "")
            replyToOutgoing: Boolean(model.replyToIsOutgoing)
            clearSelectionGeneration: root.clearSelectionGeneration
            activeSelectionMessageId: root.activeSelectionMessageId
            onConversationFocusRequested: root.conversationFocusRequested()
            onMessageSelectionClaimed: messageId => root.claimMessageSelection(messageId)
            onTypeIntoComposerRequested: text => root.typeIntoComposerRequested(text)
            onReplyRequested: (messageId, senderName, text, mediaKind, mediaMimeType, outgoing) => root.replyToMessageRequested(messageId, senderName, text, mediaKind, mediaMimeType, outgoing)
            onReplyPreviewActivated: messageId => root.jumpToReplyTarget(messageId)

            ListView.onPooled: {
                pooledByListView = true
            }

            ListView.onReused: {
                pooledByListView = false
            }
        }

        onContentYChanged: {
            root.updateScrollState()
            root.noteScroll()
        }
        onContentHeightChanged: root.updateScrollState()
        onMovementEnded: root.updateScrollState()

        Connections {
            target: list.model
            ignoreUnknownSignals: true
            function onModelReset() {
                // Chat switches and structural reloads land here; show the newest
                // message. Older-history appends do not reset the model.
                Qt.callLater(root.afterModelReset)
            }
            function onRowsInserted(parent, first, last) {
                // first === 0 means a new newest message arrived at the bottom.
                // Older history is appended at the end (first > 0) and must not
                // move the viewport.
                if (first === 0) {
                    if (root.followNewest && root.pendingJumpMessageId.length === 0) {
                        Qt.callLater(root.scrollToNewest)
                    } else {
                        root.pendingNewestMessageCount = Math.min(100, root.pendingNewestMessageCount + last - first + 1)
                    }
                }
            }
        }

        Component.onCompleted: Qt.callLater(root.scrollToNewest)
    }

    Connections {
        target: Whatevr.AppController

        function onOutgoingMessageAddedToSelectedChat() {
            if (root.pendingJumpMessageId.length > 0) {
                return
            }
            root.followNewest = true
            Qt.callLater(root.scrollToNewest)
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

    KineticWheelScroller {
        id: kineticWheelScroller

        anchors.fill: list
        target: list
        wheelStep: Kirigami.Units.gridUnit * 4
        maximumVelocity: 16000
    }

    AbstractButton {
        id: goToBottomButton

        readonly property bool hasPendingNewestMessages: root.pendingNewestMessageCount > 0

        anchors.right: parent.right
        anchors.bottom: parent.bottom
        anchors.margins: Kirigami.Units.largeSpacing
        width: Kirigami.Units.gridUnit * 2.25
        height: width
        visible: list.count > 0 && !root.atNewest
        z: kineticWheelScroller.z + 1
        hoverEnabled: true
        focusPolicy: Qt.NoFocus

        Accessible.name: hasPendingNewestMessages
                         ? Whatevr.I18n.i18nc("@action:button", "Go to bottom, %1 new messages", root.displayedPendingNewestMessageCount())
                         : Whatevr.I18n.i18nc("@action:button", "Go to bottom")

        onClicked: {
            root.pendingNewestMessageCount = 0
            kineticWheelScroller.stopKinetic()
            list.cancelFlick()
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
}
