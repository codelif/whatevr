import QtQuick
import QtQuick.Controls
import QtQuick.Effects
import org.kde.kirigami as Kirigami

Item {
    id: root

    property string messageId: ""
    property string body: ""
    property string timeText: ""
    property int status: 0
    property bool outgoing: false
    property string mediaMimeType: ""
    property string mediaLocalPath: ""
    property int mediaIntrinsicWidth: 0
    property int mediaIntrinsicHeight: 0
    property bool mediaDownloading: false
    property string mediaDownloadError: ""

    property real listWidth: 0
    readonly property real outerMargin: Kirigami.Units.largeSpacing
    readonly property real innerPadding: Kirigami.Units.largeSpacing
    readonly property real maxBubbleWidth: Math.min(listWidth * 0.7, Kirigami.Units.gridUnit * 26)
    readonly property real maxContentWidth: Math.max(Kirigami.Units.gridUnit * 4, maxBubbleWidth - innerPadding * 2)

    readonly property bool isImage: mediaMimeType.startsWith("image/")
    readonly property bool hasLocalImage: isImage && mediaLocalPath.length > 0

    readonly property real imageDisplayWidth: {
        if (!isImage) {
            return 0
        }
        const capH = Kirigami.Units.gridUnit * 16
        let intrW = mediaIntrinsicWidth
        let intrH = mediaIntrinsicHeight
        if ((intrW <= 0 || intrH <= 0) && img.implicitWidth > 0 && img.implicitHeight > 0) {
            intrW = img.implicitWidth
            intrH = img.implicitHeight
        }
        if (intrW > 0 && intrH > 0) {
            let w = Math.min(maxContentWidth, intrW)
            const h = w * (intrH / intrW)
            if (h > capH) {
                w = capH * (intrW / intrH)
            }
            return Math.max(w, Kirigami.Units.gridUnit * 8)
        }
        return Math.min(maxContentWidth, Kirigami.Units.gridUnit * 18)
    }
    readonly property real imageDisplayHeight: {
        if (!isImage) {
            return 0
        }
        let intrW = mediaIntrinsicWidth
        let intrH = mediaIntrinsicHeight
        if ((intrW <= 0 || intrH <= 0) && img.implicitWidth > 0 && img.implicitHeight > 0) {
            intrW = img.implicitWidth
            intrH = img.implicitHeight
        }
        if (intrW > 0 && intrH > 0) {
            return imageDisplayWidth * (intrH / intrW)
        }
        return Kirigami.Units.gridUnit * 11
    }

    // Status icon logic based on enum values from proto:
    // 0=UNSPECIFIED, 1=PENDING, 2=SENT, 3=DELIVERED, 4=READ, 5=FAILED
    readonly property bool statusIsFailed: status === 5
    readonly property bool statusIsRead: status === 4
    readonly property bool statusIsDoubleTick: status === 3 || status === 4  // delivered or read
    readonly property string statusSingleIcon: {
        switch (status) {
        case 1: return "clock"                    // pending / sending
        case 2: return "checkmark"                 // sent (single tick)
        case 5: return "dialog-error-symbolic"      // failed
        default: return ""
        }
    }
    readonly property bool showStatusIcon: outgoing && (statusIsDoubleTick || statusSingleIcon.length > 0)

    readonly property real statusIconSize: Kirigami.Units.iconSizes.small
    // Double-tick is wider: two icons overlapping
    readonly property real statusAreaWidth: statusIsDoubleTick
        ? statusIconSize * 1.5
        : statusIconSize

    readonly property real contentBlockWidth: {
        let w = 0
        if (isImage) {
            w = Math.max(w, imageDisplayWidth)
        }
        if (body.length > 0) {
            w = Math.max(w, Math.min(maxContentWidth, bodyMetrics.advanceWidth))
        }
        // Footer width: time text + optional status icon
        let footerW = footerMetrics.advanceWidth + Kirigami.Units.smallSpacing
        if (root.showStatusIcon) {
            footerW += root.statusAreaWidth + Kirigami.Units.smallSpacing
        }
        w = Math.max(w, Math.min(maxContentWidth, footerW))
        return Math.max(w, Kirigami.Units.gridUnit * 4)
    }

    width: listWidth
    height: bubble.height + Kirigami.Units.smallSpacing / 2

    TextMetrics {
        id: bodyMetrics
        text: root.body
        font: bodyText.font
    }

    TextMetrics {
        id: footerMetrics
        text: root.timeText
        font: timeLabel.font
    }

    Kirigami.ShadowedRectangle {
        id: bubble

        readonly property real bigRadius: Kirigami.Units.largeSpacing * 1.5
        readonly property real smallRadius: Kirigami.Units.smallSpacing / 2

        x: root.outgoing
           ? root.width - width - root.outerMargin
           : root.outerMargin
        y: 0
        width: root.contentBlockWidth + root.innerPadding * 2
        height: contentColumn.height + root.innerPadding * 2

        corners.topLeftRadius: bigRadius
        corners.topRightRadius: bigRadius
        corners.bottomLeftRadius: root.outgoing ? bigRadius : smallRadius
        corners.bottomRightRadius: root.outgoing ? smallRadius : bigRadius

        color: root.outgoing
               ? Qt.alpha(Kirigami.Theme.highlightColor, 0.30)
               : Kirigami.Theme.backgroundColor
        border.color: Qt.alpha(Kirigami.Theme.textColor, root.outgoing ? 0.05 : 0.12)
        border.width: 1

        Item {
            id: contentColumn

            x: root.innerPadding
            y: root.innerPadding
            width: root.contentBlockWidth
            height: {
                let h = 0
                if (mediaSlot.visible) {
                    h += mediaSlot.height
                }
                if (bodyText.visible) {
                    if (h > 0) h += Kirigami.Units.smallSpacing
                    h += bodyText.height
                }
                if (h > 0) h += Kirigami.Units.smallSpacing / 2
                h += footerSlot.height
                return h
            }

            Item {
                id: mediaSlot

                visible: root.isImage
                x: (root.contentBlockWidth - width) / 2
                y: 0
                width: root.imageDisplayWidth
                height: visible ? root.imageDisplayHeight : 0

                Rectangle {
                    anchors.fill: parent
                    visible: !root.hasLocalImage
                    color: Qt.alpha(Kirigami.Theme.textColor, 0.06)
                    radius: Kirigami.Units.smallSpacing
                    border.color: Qt.alpha(Kirigami.Theme.textColor, 0.12)

                    Column {
                        anchors.centerIn: parent
                        spacing: Kirigami.Units.smallSpacing

                        BusyIndicator {
                            anchors.horizontalCenter: parent.horizontalCenter
                            visible: root.mediaDownloading
                            running: visible
                            implicitWidth: Kirigami.Units.gridUnit * 2
                            implicitHeight: Kirigami.Units.gridUnit * 2
                        }

                        Button {
                            anchors.horizontalCenter: parent.horizontalCenter
                            visible: !root.mediaDownloading
                            icon.name: "folder-download-symbolic"
                            text: i18nc("@action:button", "Load image")
                            enabled: root.messageId.length > 0
                            onClicked: AppController.downloadMessageMedia(root.messageId)
                        }

                        Label {
                            anchors.horizontalCenter: parent.horizontalCenter
                            width: Math.min(implicitWidth, mediaSlot.width - Kirigami.Units.largeSpacing * 2)
                            visible: !root.mediaDownloading && root.mediaDownloadError.length > 0
                            text: root.mediaDownloadError
                            color: Kirigami.Theme.negativeTextColor
                            font.pointSize: Kirigami.Theme.smallFont.pointSize
                            wrapMode: Text.Wrap
                            horizontalAlignment: Text.AlignHCenter
                        }
                    }
                }

                Image {
                    id: img
                    anchors.fill: parent
                    visible: root.hasLocalImage
                    source: root.hasLocalImage ? Qt.resolvedUrl("file://" + root.mediaLocalPath) : ""
                    fillMode: Image.PreserveAspectFit
                    asynchronous: true
                    cache: true
                    smooth: true

                    layer.enabled: visible
                    layer.effect: MultiEffect {
                        maskEnabled: true
                        maskSource: imageMask
                    }
                }

                Rectangle {
                    id: imageMask

                    anchors.fill: parent
                    radius: Kirigami.Units.smallSpacing
                    visible: false
                    layer.enabled: true
                }
            }

            Label {
                id: bodyText

                visible: root.body.length > 0
                x: 0
                y: mediaSlot.visible ? mediaSlot.height + Kirigami.Units.smallSpacing : 0
                width: root.contentBlockWidth
                text: root.body
                wrapMode: Text.Wrap
                textFormat: Text.PlainText
                color: Kirigami.Theme.textColor
            }

            Item {
                id: footerSlot

                x: 0
                y: {
                    let off = 0
                    if (mediaSlot.visible) off += mediaSlot.height
                    if (bodyText.visible) {
                        if (off > 0) off += Kirigami.Units.smallSpacing
                        off += bodyText.height
                    }
                    if (off > 0) off += Kirigami.Units.smallSpacing / 2
                    return off
                }
                width: root.contentBlockWidth
                height: Math.max(timeLabel.implicitHeight, statusArea.visible ? statusArea.implicitHeight : 0)

                Item {
                    id: statusArea
                    anchors.right: parent.right
                    anchors.verticalCenter: timeLabel.verticalCenter
                    visible: root.showStatusIcon
                    implicitWidth: root.statusAreaWidth
                    implicitHeight: root.statusIconSize

                    // Single icon (clock, single checkmark, error)
                    Kirigami.Icon {
                        id: singleIcon
                        anchors.centerIn: parent
                        visible: !root.statusIsDoubleTick
                        source: root.statusSingleIcon
                        implicitWidth: root.statusIconSize
                        implicitHeight: root.statusIconSize
                        color: root.statusIsFailed
                               ? Kirigami.Theme.negativeTextColor
                               : Kirigami.Theme.disabledTextColor
                        isMask: true
                    }

                    // Double tick (delivered / read)
                    Item {
                        id: doubleTick
                        anchors.fill: parent
                        visible: root.statusIsDoubleTick

                        Kirigami.Icon {
                            x: 0
                            anchors.verticalCenter: parent.verticalCenter
                            source: "checkmark"
                            implicitWidth: root.statusIconSize
                            implicitHeight: root.statusIconSize
                            color: root.statusIsRead
                                   ? Kirigami.Theme.highlightColor
                                   : Kirigami.Theme.disabledTextColor
                            isMask: true
                        }
                        Kirigami.Icon {
                            x: root.statusIconSize * 0.5
                            anchors.verticalCenter: parent.verticalCenter
                            source: "checkmark"
                            implicitWidth: root.statusIconSize
                            implicitHeight: root.statusIconSize
                            color: root.statusIsRead
                                   ? Kirigami.Theme.highlightColor
                                   : Kirigami.Theme.disabledTextColor
                            isMask: true
                        }
                    }
                }

                Label {
                    id: timeLabel
                    anchors.left: parent.left
                    text: root.timeText
                    color: Kirigami.Theme.disabledTextColor
                    font.pointSize: Kirigami.Theme.smallFont.pointSize * 0.8
                }
            }
        }
    }
}
