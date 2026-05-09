import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami

Item {
    id: root

    property alias model: list.model
    property bool atBottom: true

    function scrollToBottom() {
        if (list.count > 0) {
            list.positionViewAtEnd()
        }
    }

    ListView {
        id: list

        anchors.fill: parent
        clip: true

        spacing: Kirigami.Units.smallSpacing / 2
        cacheBuffer: Math.max(0, height)
        reuseItems: true

        flickableDirection: Flickable.VerticalFlick

        ScrollBar.vertical: ScrollBar {
            policy: ScrollBar.AlwaysOn
        }

        Kirigami.WheelHandler {
            target: list
            filterMouseEvents: true
            keyNavigationEnabled: true
        }

        delegate: ChatBubble {
            messageId: String(model.id || "")
            body: String(model.text || "")
            timeText: String(model.timeText || "")
            statusText: String(model.statusText || "")
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

        onContentYChanged: {
            const distanceFromBottom = (contentHeight - height) - contentY
            root.atBottom = distanceFromBottom <= Kirigami.Units.gridUnit * 2
        }

        onCountChanged: {
            if (root.atBottom) {
                Qt.callLater(positionViewAtEnd)
            }
        }

        Component.onCompleted: {
            Qt.callLater(positionViewAtEnd)
        }
    }
}
