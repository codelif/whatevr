import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami

Item {
    id: root

    property alias model: list.model
    property bool atBottom: true
    property bool pinToBottom: true

    // Saved before a prepend to restore scroll position afterwards
    property real _prependSavedHeight: 0

    function scrollToBottom() {
        if (list.count > 0) {
            list.positionViewAtEnd()
            list.contentY = list.contentHeight - list.height
        }
    }

    BusyIndicator {
        id: loadMoreIndicator
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.top: parent.top
        anchors.topMargin: Kirigami.Units.smallSpacing
        running: AppController.messagesLoadingMore
        visible: running
        z: 1
    }

    ListView {
        id: list

        anchors.fill: parent
        clip: true

        spacing: Kirigami.Units.smallSpacing / 2
        cacheBuffer: 100000
        reuseItems: false

        flickableDirection: Flickable.VerticalFlick
        boundsBehavior: Flickable.StopAtBounds
        boundsMovement: Flickable.StopAtBounds
        flickDeceleration: 4000
        maximumFlickVelocity: 8000

        ScrollBar.vertical: ScrollBar {
            policy: ScrollBar.AlwaysOn
        }

        Kirigami.WheelHandler {
            target: list
            filterMouseEvents: true
            keyNavigationEnabled: true
        }

        delegate: ChatBubble {
            listWidth: list.width
            messageId: String(model.id || "")
            body: String(model.text || "")
            timeText: String(model.timeText || "")
            status: Number(model.status || 0)
            outgoing: Boolean(model.isOutgoing)
            mediaMimeType: String(model.mediaMimeType || "")
            mediaLocalPath: String(model.mediaLocalPath || "")
            mediaIntrinsicWidth: Number(model.mediaWidth || 0)
            mediaIntrinsicHeight: Number(model.mediaHeight || 0)
            mediaDownloading: AppController.isMessageMediaDownloading(messageId)

            Connections {
                target: AppController

                function onMediaDownloadChanged(messageId) {
                    if (messageId === parent.messageId) {
                        parent.mediaDownloading = AppController.isMessageMediaDownloading(messageId)
                        if (parent.mediaDownloading) {
                            parent.mediaDownloadError = ""
                        }
                    }
                }

                function onMediaDownloadFailed(messageId, errorText) {
                    if (messageId === parent.messageId) {
                        parent.mediaDownloadError = errorText
                    }
                }
            }
        }

        onMovementStarted: root.pinToBottom = false
        onContentYChanged: {
            const distanceFromBottom = (contentHeight - height) - contentY
            root.atBottom = distanceFromBottom <= 4

            // Trigger loading older messages when near the top
            if (contentY <= height * 0.2 && AppController.messagesHaveMore && !AppController.messagesLoadingMore) {
                AppController.loadMoreMessages()
            }
        }
        onContentHeightChanged: {
            if (root.pinToBottom && contentHeight > height) {
                contentY = contentHeight - height
            }
        }
        onCountChanged: {
            if (root.atBottom) {
                root.pinToBottom = true
                Qt.callLater(() => {
                    if (root.pinToBottom) contentY = contentHeight - height
                })
            }
        }

        Connections {
            target: list.model
            ignoreUnknownSignals: true
            function onModelReset() {
                root.pinToBottom = true
                root.atBottom = true
                Qt.callLater(() => {
                    if (root.pinToBottom) list.contentY = list.contentHeight - list.height
                })
            }
        }

        Connections {
            target: AppController
            function onMoreMessagesWillLoad() {
                root._prependSavedHeight = list.contentHeight
            }
            function onMoreMessagesLoaded() {
                // Restore scroll position so the previously visible messages stay in place
                Qt.callLater(() => {
                    const delta = list.contentHeight - root._prependSavedHeight
                    if (delta > 0) {
                        list.contentY += delta
                    }
                    root._prependSavedHeight = 0
                })
            }
        }

        Component.onCompleted: {
            root.pinToBottom = true
            Qt.callLater(() => { list.contentY = list.contentHeight - list.height })
        }
    }
}
