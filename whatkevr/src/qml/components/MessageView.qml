import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami

Item {
    id: root

    property alias model: list.model
    property bool atBottom: true
    property bool pinToBottom: true

    function scrollToBottom() {
        if (list.count > 0) {
            list.positionViewAtEnd()
            list.contentY = list.contentHeight - list.height
        }
    }

    ListView {
        id: list

        anchors.fill: parent
        clip: true

        spacing: Kirigami.Units.smallSpacing / 2
        cacheBuffer: Math.max(0, height * 2)
        reuseItems: false

        flickableDirection: Flickable.VerticalFlick
        boundsBehavior: Flickable.StopAtBounds
        boundsMovement: Flickable.StopAtBounds
        flickDeceleration: 4000
        maximumFlickVelocity: 8000

        ScrollBar.vertical: ScrollBar {
            policy: ScrollBar.AlwaysOn
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

        Component.onCompleted: {
            root.pinToBottom = true
            Qt.callLater(() => { list.contentY = list.contentHeight - list.height })
        }
    }

    KineticWheelScroller {
        anchors.fill: list
        target: list
        wheelStep: Kirigami.Units.gridUnit * 4
        maximumVelocity: 16000
        onScrollStarted: root.pinToBottom = false
    }
}
