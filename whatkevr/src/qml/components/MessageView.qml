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

    // How close (in rows) to the newest message we must be to keep following it.
    property int followRowThreshold: 2
    // Start fetching older history once the topmost visible row is within this
    // many rows of the oldest loaded message, so the next page usually arrives
    // before the user reaches the edge.
    property int prefetchRowThreshold: 24

    property int clearSelectionGeneration: 0
    property string activeSelectionMessageId: ""

    signal loadOlderMessagesRequested()
    signal conversationFocusRequested()
    signal typeIntoComposerRequested(string text)

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
            list.positionViewAtBeginning()
        }
    }

    // Recompute followNewest and fire predictive history prefetch. Cheap: two
    // indexAt probes, no allocations, safe to call on every contentY change.
    function updateScrollState() {
        if (list.count === 0) {
            followNewest = true
            return
        }

        const cx = list.width / 2
        const topIndex = list.indexAt(cx, list.contentY + 1)
        const bottomIndex = list.indexAt(cx, list.contentY + Math.max(1, list.height - 1))

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
            followNewest = lo <= followRowThreshold
        }

        if (!openingChat
                && hi >= 0
                && canLoadOlderMessages
                && !loadingOlderMessages
                && hi >= list.count - 1 - prefetchRowThreshold) {
            loadOlderMessagesRequested()
        }
    }

    function afterModelReset() {
        scrollToNewest()
        openingChat = false
        followNewest = true
        Qt.callLater(updateScrollState)
    }

    onChatIdChanged: {
        followNewest = true
        if (chatId.length === 0) {
            openingChat = false
        } else {
            openingChat = true
            Qt.callLater(scrollToNewest)
        }
    }

    onVisibleChanged: {
        if (visible && followNewest) {
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
            timeText: String(model.timeText || "")
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
            mediaDownloading: Boolean(model.mediaDownloading)
            mediaDownloadError: String(model.mediaDownloadError || "")
            clearSelectionGeneration: root.clearSelectionGeneration
            activeSelectionMessageId: root.activeSelectionMessageId
            onConversationFocusRequested: root.conversationFocusRequested()
            onMessageSelectionClaimed: messageId => root.claimMessageSelection(messageId)
            onTypeIntoComposerRequested: text => root.typeIntoComposerRequested(text)

            ListView.onPooled: {
                pooledByListView = true
            }

            ListView.onReused: {
                pooledByListView = false
            }
        }

        onContentYChanged: root.updateScrollState()
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
                if (first === 0 && root.followNewest) {
                    Qt.callLater(root.scrollToNewest)
                }
            }
        }

        Component.onCompleted: Qt.callLater(root.scrollToNewest)
    }

    Connections {
        target: Whatevr.AppController

        function onOutgoingMessageAddedToSelectedChat() {
            root.followNewest = true
            Qt.callLater(root.scrollToNewest)
        }
    }

    KineticWheelScroller {
        anchors.fill: list
        target: list
        wheelStep: Kirigami.Units.gridUnit * 4
        maximumVelocity: 16000
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
        visible: root.loadingOlderMessages
        z: 10

        Row {
            id: loadingOlderIndicator
            anchors.centerIn: parent
            spacing: Kirigami.Units.smallSpacing

            BusyIndicator {
                running: root.loadingOlderMessages
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
}
