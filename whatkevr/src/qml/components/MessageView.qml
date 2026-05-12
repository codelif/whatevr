import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami

Item {
    id: root

    property alias model: list.model
    property bool atBottom: true
    property bool pinToBottom: true
    property bool loadingOlderMessages: false
    property bool canLoadOlderMessages: false
    property real topLoadThreshold: Kirigami.Units.gridUnit * 6

    property bool preservingPrependPosition: false
    property real contentHeightBeforePrepend: 0
    property real contentYBeforePrepend: 0

    signal loadOlderMessagesRequested()

    function maximumY() {
        return list.originY + Math.max(0, list.contentHeight - list.height)
    }

    function pinToBottomNow() {
        if (list.count > 0) {
            list.positionViewAtEnd()
        }
        list.contentY = maximumY()
    }

    function scrollToBottom() {
        pinToBottomNow()
    }

    function maybeLoadOlderMessages() {
        if (!canLoadOlderMessages || loadingOlderMessages || preservingPrependPosition || list.count === 0) {
            return
        }

        if (list.contentY <= list.originY + topLoadThreshold) {
            preservingPrependPosition = true
            contentHeightBeforePrepend = list.contentHeight
            contentYBeforePrepend = list.contentY
            loadOlderMessagesRequested()
        }
    }

    onLoadingOlderMessagesChanged: {
        if (!loadingOlderMessages && preservingPrependPosition) {
            Qt.callLater(() => {
                const delta = list.contentHeight - contentHeightBeforePrepend
                list.contentY = Math.max(list.originY, contentYBeforePrepend + Math.max(0, delta))
                preservingPrependPosition = false
                maybeLoadOlderMessages()
            })
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

        onDraggingChanged: {
            if (dragging) {
                root.pinToBottom = false
            }
        }
        onContentYChanged: {
            const distanceFromBottom = root.maximumY() - contentY
            root.atBottom = distanceFromBottom <= 4
            root.maybeLoadOlderMessages()
        }
        onContentHeightChanged: {
            if (!root.preservingPrependPosition && root.pinToBottom && contentHeight > height) {
                root.pinToBottomNow()
            }
        }
        onCountChanged: {
            if (root.atBottom) {
                root.pinToBottom = true
                Qt.callLater(() => {
                    if (root.pinToBottom) root.pinToBottomNow()
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
                    if (root.pinToBottom) root.pinToBottomNow()
                })
            }
        }

        Component.onCompleted: {
            root.pinToBottom = true
            Qt.callLater(() => { root.pinToBottomNow() })
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
                text: i18nc("@info", "Loading older messages")
                font.pointSize: Kirigami.Theme.smallFont.pointSize
                color: Kirigami.Theme.disabledTextColor
            }
        }
    }
}
