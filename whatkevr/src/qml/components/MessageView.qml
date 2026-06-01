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
    property bool atBottom: true
    property bool nearBottom: true
    property bool pinToBottom: true
    property bool bottomPinInProgress: false
    property bool openingChat: false
    property bool loadingOlderMessages: false
    property bool canLoadOlderMessages: false
    property real topLoadThreshold: Kirigami.Units.gridUnit * 6
    property real bottomFollowThreshold: Kirigami.Units.gridUnit * 2
    property bool pendingEndInsertShouldPin: false

    property bool preservingPrependPosition: false
    property real contentHeightBeforePrepend: 0
    property real contentYBeforePrepend: 0
    property real originYBeforePrepend: 0
    property int clearSelectionGeneration: 0

    signal loadOlderMessagesRequested()
    signal conversationFocusRequested()
    signal typeIntoComposerRequested(string text)

    function clearMessageSelection() {
        clearSelectionGeneration += 1
    }

    function maximumY() {
        return list.originY + Math.max(0, list.contentHeight - list.height)
    }

    function clampContentY(value) {
        return Math.max(list.originY, Math.min(maximumY(), value))
    }

    function pinToBottomNow() {
        bottomPinInProgress = true
        if (list.count > 0) {
            list.positionViewAtEnd()
        }
        list.contentY = maximumY()
        bottomPinFinishTimer.restart()
    }

    function scrollToBottom() {
        pinToBottomNow()
    }

    function schedulePinToBottom() {
        bottomPinInProgress = true
        pinToBottomTimer.restart()
    }

    function scheduleFinishPrependPositionPreservation() {
        finishPrependPositionTimer.restart()
    }

    function beginPrependPositionPreservation() {
        pinToBottom = false
        preservingPrependPosition = true
        contentHeightBeforePrepend = list.contentHeight
        contentYBeforePrepend = list.contentY
        originYBeforePrepend = list.originY
    }

    function restorePrependPosition() {
        if (!preservingPrependPosition) {
            return
        }

        const insertedHeight = Math.max(0, list.contentHeight - contentHeightBeforePrepend)
        const originYDelta = list.originY - originYBeforePrepend
        list.contentY = clampContentY(contentYBeforePrepend + originYDelta + insertedHeight)
    }

    function finishPrependPositionPreservation() {
        if (!preservingPrependPosition) {
            return
        }

        restorePrependPosition()
        preservingPrependPosition = false
        maybeLoadOlderMessages()
    }

    function prepareViewportForModelReset() {
        if (openingChat) {
            bottomPinInProgress = true
            return
        }

        if (!pinToBottom && !atBottom && list.count > 0) {
            beginPrependPositionPreservation()
        } else {
            bottomPinInProgress = true
        }
    }

    function finishViewportForModelReset() {
        if (preservingPrependPosition) {
            scheduleFinishPrependPositionPreservation()
        } else if (openingChat || pinToBottom || atBottom) {
            pinToBottom = true
            atBottom = true
            nearBottom = true
            schedulePinToBottom()
        }
    }

    function forceBottomPin() {
        pinToBottom = true
        atBottom = true
        nearBottom = true
        schedulePinToBottom()
    }

    function updateBottomState() {
        const distanceFromBottom = maximumY() - list.contentY
        atBottom = distanceFromBottom <= 4
        nearBottom = distanceFromBottom <= bottomFollowThreshold
    }

    onChatIdChanged: {
        pendingEndInsertShouldPin = false
        preservingPrependPosition = false
        if (chatId.length > 0) {
            openingChat = true
            forceBottomPin()
        } else {
            openingChat = false
            pinToBottom = true
            atBottom = true
            nearBottom = true
        }
    }

    function maybeLoadOlderMessages() {
        if (!canLoadOlderMessages
                || loadingOlderMessages
                || preservingPrependPosition
                || openingChat
                || bottomPinInProgress
                || pinToBottom
                || list.count === 0) {
            return
        }

        if (list.contentY <= list.originY + topLoadThreshold) {
            beginPrependPositionPreservation()
            loadOlderMessagesRequested()
            scheduleFinishPrependPositionPreservation()
        }
    }

    onLoadingOlderMessagesChanged: {
        if (!loadingOlderMessages && preservingPrependPosition) {
            scheduleFinishPrependPositionPreservation()
        }
    }

    onVisibleChanged: {
        if (visible) {
            forceBottomPin()
        }
    }

    Timer {
        id: pinToBottomTimer

        interval: 0
        repeat: false
        onTriggered: {
            if (root.pinToBottom) {
                root.pinToBottomNow()
            }
        }
    }

    Timer {
        id: finishPrependPositionTimer

        interval: 0
        repeat: false
        onTriggered: {
            if (!root.loadingOlderMessages && root.preservingPrependPosition) {
                root.finishPrependPositionPreservation()
            }
        }
    }

    Timer {
        id: bottomPinFinishTimer

        interval: 0
        repeat: false
        onTriggered: {
            if (root.pinToBottom) {
                list.contentY = root.maximumY()
            }
            root.updateBottomState()
            root.openingChat = false
            root.bottomPinInProgress = false
        }
    }

    Component.onDestruction: {
        pinToBottomTimer.stop()
        finishPrependPositionTimer.stop()
        bottomPinFinishTimer.stop()
    }

    ListView {
        id: list

        anchors.fill: parent
        clip: true

        spacing: Kirigami.Units.smallSpacing / 2
        cacheBuffer: Math.max(0, height)
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
            mediaDownloading: Whatevr.AppController.isMessageMediaDownloading(messageId)
            clearSelectionGeneration: root.clearSelectionGeneration
            onConversationFocusRequested: root.conversationFocusRequested()
            onTypeIntoComposerRequested: text => root.typeIntoComposerRequested(text)

            Connections {
                target: Whatevr.AppController

                function onMediaDownloadChanged(messageId) {
                    if (messageId === messageDelegate.messageId) {
                        messageDelegate.mediaDownloading = Whatevr.AppController.isMessageMediaDownloading(messageId)
                        if (messageDelegate.mediaDownloading) {
                            messageDelegate.mediaDownloadError = ""
                        }
                    }
                }

                function onMediaDownloadFailed(messageId, errorText) {
                    if (messageId === messageDelegate.messageId) {
                        messageDelegate.mediaDownloadError = errorText
                    }
                }
            }
        }

        onDraggingChanged: {
            if (dragging) {
                root.pinToBottom = false
            }
        }
        onContentYChanged: {
            root.updateBottomState()
            if (!root.nearBottom && !root.preservingPrependPosition && !root.bottomPinInProgress) {
                root.pinToBottom = false
            }
            root.maybeLoadOlderMessages()
        }
        onContentHeightChanged: {
            if (root.preservingPrependPosition) {
                root.restorePrependPosition()
            } else if (root.pinToBottom && contentHeight > height) {
                root.pinToBottomNow()
            }
        }
        onOriginYChanged: {
            if (root.preservingPrependPosition) {
                root.restorePrependPosition()
            }
        }
        onCountChanged: {
            if (root.preservingPrependPosition) {
                root.restorePrependPosition()
            } else if (root.pendingEndInsertShouldPin || root.pinToBottom || root.nearBottom) {
                root.pendingEndInsertShouldPin = false
                root.forceBottomPin()
            }
        }

        Connections {
            target: list.model
            ignoreUnknownSignals: true
            function onModelAboutToBeReset() {
                root.prepareViewportForModelReset()
            }
            function onModelReset() {
                root.finishViewportForModelReset()
            }
            function onRowsAboutToBeInserted(parent, first, last) {
                root.pendingEndInsertShouldPin = first >= list.count && (root.pinToBottom || root.nearBottom)
            }
            function onRowsInserted(parent, first, last) {
                if (root.pendingEndInsertShouldPin) {
                    root.forceBottomPin()
                }
                root.pendingEndInsertShouldPin = false
            }
        }

        Component.onCompleted: {
            root.forceBottomPin()
        }
    }

    Connections {
        target: Whatevr.AppController

        function onOutgoingMessageAddedToSelectedChat() {
            root.forceBottomPin()
        }
    }

    KineticWheelScroller {
        anchors.fill: list
        target: list
        wheelStep: Kirigami.Units.gridUnit * 4
        maximumVelocity: 16000
        onScrollStarted: root.pinToBottom = false
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
