import QtQuick
import QtQuick.Controls
import QtQuick.Effects
import org.kde.kirigami as Kirigami

Item {
    id: root

    Kirigami.Theme.inherit: false
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    property string messageId: ""
    property string body: ""
    property string timeText: ""
    property int status: 0
    property bool outgoing: false
    property string senderName: ""
    property string senderAvatarLocalPath: ""
    property string senderInitials: "?"
    property bool showSenderHeader: false
    property bool showSenderAvatar: false
    property bool showSenderGutter: false
    property bool groupStart: true
    property bool groupEnd: true
    property string mediaMimeType: ""
    property string mediaLocalPath: ""
    property string mediaThumbnailLocalPath: ""
    property int mediaIntrinsicWidth: 0
    property int mediaIntrinsicHeight: 0
    property bool mediaDownloading: false
    property string mediaDownloadError: ""

    property real listWidth: 0
    readonly property real outerMargin: Kirigami.Units.largeSpacing
    readonly property real innerPadding: Kirigami.Units.largeSpacing
    readonly property real senderAvatarSize: Kirigami.Units.gridUnit * 1.65
    readonly property real senderGutterWidth: showSenderGutter ? senderAvatarSize + Kirigami.Units.smallSpacing : 0
    readonly property real senderHeaderHeight: showSenderHeader
        ? Math.max(senderAvatarSize, senderHeader.implicitHeight)
        : 0
    readonly property real maxBubbleWidth: Math.max(Kirigami.Units.gridUnit * 4,
                                                    Math.min(Math.max(0, listWidth - outerMargin * 2 - senderGutterWidth),
                                                              Kirigami.Units.gridUnit * 28))
    readonly property real maxContentWidth: Math.max(Kirigami.Units.gridUnit * 4, maxBubbleWidth - innerPadding * 2)

    readonly property bool isImage: mediaMimeType.startsWith("image/")
    readonly property bool hasLocalImage: isImage && mediaLocalPath.length > 0
    readonly property bool hasThumbnailImage: isImage && mediaThumbnailLocalPath.length > 0

    // Image geometry must not depend on Image.implicitWidth/implicitHeight.
    // Those values arrive after decode and would resize the delegate while the
    // ListView is already scrolling. Reserve a frame from message metadata when
    // it is present; otherwise use a stable thumbnail shape for the lifetime of
    // this delegate.
    readonly property real minImageWidth: Math.min(maxContentWidth, Kirigami.Units.gridUnit * 7)
    readonly property real fallbackImageWidth: Math.min(maxContentWidth, Kirigami.Units.gridUnit * 18)
    readonly property real maxImageHeight: Math.max(Kirigami.Units.gridUnit * 8,
                                                    Math.min(Math.max(0, listWidth) * 0.72,
                                                             Kirigami.Units.gridUnit * 24))
    readonly property real fallbackImageAspectRatio: 16 / 10
    property real reservedImageAspectRatio: fallbackImageAspectRatio
    property real reservedImageNaturalWidth: fallbackImageWidth

    function normalisedImageAspectRatio(width, height) {
        if (width <= 0 || height <= 0) {
            return fallbackImageAspectRatio
        }

        // Keep broken metadata usable without flattening normal portrait,
        // landscape, screenshot, or panorama images into the same shape.
        return Math.max(0.25, Math.min(width / height, 4.0))
    }

    function resetReservedImageGeometry() {
        reservedImageAspectRatio = normalisedImageAspectRatio(mediaIntrinsicWidth, mediaIntrinsicHeight)
        reservedImageNaturalWidth = mediaIntrinsicWidth > 0 ? mediaIntrinsicWidth : fallbackImageWidth
    }

    onMessageIdChanged: resetReservedImageGeometry()
    onMediaMimeTypeChanged: resetReservedImageGeometry()
    Component.onCompleted: resetReservedImageGeometry()

    readonly property real imageDisplayWidth: {
        if (!isImage) {
            return 0
        }

        let width = Math.min(maxContentWidth, Math.max(minImageWidth, reservedImageNaturalWidth))
        if (width / reservedImageAspectRatio > maxImageHeight) {
            width = maxImageHeight * reservedImageAspectRatio
        }
        return Math.max(1, Math.min(maxContentWidth, width))
    }

    readonly property real imageDisplayHeight: {
        if (!isImage) {
            return 0
        }

        return Math.max(1, Math.min(maxImageHeight, imageDisplayWidth / reservedImageAspectRatio))
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
    readonly property real statusDoubleTickOffset: statusIconSize * 0.4
    // Double-tick is wider: two icons overlapping
    readonly property real statusAreaWidth: statusIsDoubleTick
        ? statusIconSize * 1.4
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
    height: bubble.y + bubble.height + (groupEnd ? Kirigami.Units.smallSpacing : Kirigami.Units.smallSpacing / 4)

    SystemPalette {
        id: activePalette
        colorGroup: SystemPalette.Active
    }

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

        readonly property real bubbleRadius: Kirigami.Units.cornerRadius

        x: root.outgoing
           ? root.width - width - root.outerMargin
           : root.outerMargin + root.senderGutterWidth
        y: root.senderHeaderHeight > 0 ? root.senderHeaderHeight + Kirigami.Units.smallSpacing / 2 : 0
        width: root.contentBlockWidth + root.innerPadding * 2
        height: contentColumn.height + root.innerPadding * 2

        corners.topLeftRadius: !root.outgoing && !root.groupStart ? bubbleRadius * 0.45 : bubbleRadius
        corners.topRightRadius: root.outgoing && !root.groupStart ? bubbleRadius * 0.45 : bubbleRadius
        corners.bottomLeftRadius: !root.outgoing && !root.groupEnd ? bubbleRadius * 0.45 : bubbleRadius
        corners.bottomRightRadius: root.outgoing && !root.groupEnd ? bubbleRadius * 0.45 : bubbleRadius

        color: root.outgoing
                ? Qt.alpha(activePalette.highlight, 0.30)
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
                clip: true

                Rectangle {
                    id: mediaBackground

                    anchors.fill: parent
                    radius: Kirigami.Units.cornerRadius
                    color: Qt.alpha(Kirigami.Theme.textColor, 0.06)
                    border.color: Qt.alpha(Kirigami.Theme.textColor, 0.12)
                }

                Image {
                    id: thumb

                    anchors.fill: parent
                    visible: !root.hasLocalImage && root.hasThumbnailImage
                    opacity: status === Image.Ready ? 0.78 : 0
                    source: visible ? Qt.resolvedUrl("file://" + root.mediaThumbnailLocalPath) : ""
                    fillMode: Image.PreserveAspectCrop
                    asynchronous: true
                    cache: true
                    smooth: true
                    sourceSize.width: Math.max(1, Math.ceil(width))
                    sourceSize.height: Math.max(1, Math.ceil(height))

                    layer.enabled: visible && status === Image.Ready
                    layer.effect: MultiEffect {
                        blurEnabled: true
                        blurMax: 12
                        blur: 0.35
                        saturation: 0.75
                        maskEnabled: true
                        maskSource: imageMask
                    }

                    Behavior on opacity {
                        NumberAnimation {
                            duration: Kirigami.Units.shortDuration
                            easing.type: Easing.OutCubic
                        }
                    }
                }

                Image {
                    id: img

                    anchors.fill: parent
                    visible: root.hasLocalImage
                    opacity: status === Image.Ready ? 1 : 0
                    source: root.hasLocalImage ? Qt.resolvedUrl("file://" + root.mediaLocalPath) : ""
                    fillMode: Image.PreserveAspectFit
                    asynchronous: true
                    cache: true
                    smooth: true
                    sourceSize.width: Math.max(1, Math.ceil(width))
                    sourceSize.height: Math.max(1, Math.ceil(height))

                    layer.enabled: visible && status === Image.Ready
                    layer.effect: MultiEffect {
                        maskEnabled: true
                        maskSource: imageMask
                    }

                    Behavior on opacity {
                        NumberAnimation {
                            duration: Kirigami.Units.shortDuration
                            easing.type: Easing.OutCubic
                        }
                    }
                }

                Rectangle {
                    id: imageMask

                    anchors.fill: parent
                    radius: Kirigami.Units.cornerRadius
                    visible: false
                    layer.enabled: true
                }

                Item {
                    id: imageOverlay

                    anchors.fill: parent
                    visible: !root.hasLocalImage
                             || root.mediaDownloading
                             || thumb.status === Image.Loading
                             || img.status === Image.Loading
                             || img.status === Image.Error
                             || root.mediaDownloadError.length > 0

                    Rectangle {
                        anchors.fill: parent
                        radius: mediaBackground.radius
                        color: Qt.alpha(Kirigami.Theme.backgroundColor, root.hasLocalImage || root.hasThumbnailImage ? 0.34 : 0.0)
                    }

                    Column {
                        anchors.centerIn: parent
                        width: Math.max(0, parent.width - Kirigami.Units.largeSpacing * 2)
                        spacing: Kirigami.Units.smallSpacing

                        BusyIndicator {
                            anchors.horizontalCenter: parent.horizontalCenter
                            visible: root.mediaDownloading
                                     || (!root.hasLocalImage && root.hasThumbnailImage && thumb.status === Image.Loading)
                                     || (root.hasLocalImage && img.status === Image.Loading)
                            running: visible
                            implicitWidth: Kirigami.Units.gridUnit * 2
                            implicitHeight: Kirigami.Units.gridUnit * 2
                        }

                        Button {
                            anchors.horizontalCenter: parent.horizontalCenter
                            visible: !root.hasLocalImage && !root.mediaDownloading
                            icon.name: "folder-download-symbolic"
                            text: i18nc("@action:button", "Load image")
                            enabled: root.messageId.length > 0
                            onClicked: AppController.downloadMessageMedia(root.messageId)
                        }

                        Label {
                            anchors.horizontalCenter: parent.horizontalCenter
                            width: parent.width
                            visible: img.status === Image.Error && root.hasLocalImage
                            text: i18nc("@info", "Image could not be displayed")
                            color: Kirigami.Theme.negativeTextColor
                            font.pointSize: Kirigami.Theme.smallFont.pointSize
                            wrapMode: Text.Wrap
                            horizontalAlignment: Text.AlignHCenter
                        }

                        Label {
                            anchors.horizontalCenter: parent.horizontalCenter
                            width: parent.width
                            visible: !root.mediaDownloading && root.mediaDownloadError.length > 0
                            text: root.mediaDownloadError
                            color: Kirigami.Theme.negativeTextColor
                            font.pointSize: Kirigami.Theme.smallFont.pointSize
                            wrapMode: Text.Wrap
                            horizontalAlignment: Text.AlignHCenter
                        }
                    }
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
                    Kirigami.Icon {
                        anchors.centerIn: parent
                        anchors.horizontalCenterOffset: 0.75
                        visible: singleIcon.visible && root.statusSingleIcon === "checkmark"
                        source: singleIcon.source
                        implicitWidth: singleIcon.implicitWidth
                        implicitHeight: singleIcon.implicitHeight
                        color: singleIcon.color
                        isMask: true
                    }

                    // Double tick (delivered / read)
                    Item {
                        id: doubleTick
                        anchors.fill: parent
                        visible: root.statusIsDoubleTick

                        Kirigami.Icon {
                            id: firstStatusTick

                            x: 0
                            anchors.verticalCenter: parent.verticalCenter
                            source: "checkmark"
                            implicitWidth: root.statusIconSize
                            implicitHeight: root.statusIconSize
                            color: root.statusIsRead
                                   ? activePalette.highlight
                                   : Kirigami.Theme.disabledTextColor
                            isMask: true
                        }
                        Kirigami.Icon {
                            x: firstStatusTick.x + 0.75
                            anchors.verticalCenter: parent.verticalCenter
                            source: firstStatusTick.source
                            implicitWidth: firstStatusTick.implicitWidth
                            implicitHeight: firstStatusTick.implicitHeight
                            color: firstStatusTick.color
                            isMask: true
                        }
                        Kirigami.Icon {
                            id: secondStatusTick

                            x: root.statusDoubleTickOffset
                            anchors.verticalCenter: parent.verticalCenter
                            source: "checkmark"
                            implicitWidth: root.statusIconSize
                            implicitHeight: root.statusIconSize
                            color: root.statusIsRead
                                   ? activePalette.highlight
                                   : Kirigami.Theme.disabledTextColor
                            isMask: true
                        }
                        Kirigami.Icon {
                            x: secondStatusTick.x + 0.75
                            anchors.verticalCenter: parent.verticalCenter
                            source: secondStatusTick.source
                            implicitWidth: secondStatusTick.implicitWidth
                            implicitHeight: secondStatusTick.implicitHeight
                            color: secondStatusTick.color
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

    Label {
        id: senderHeader

        visible: root.showSenderHeader && root.senderName.length > 0
        x: bubble.x + root.innerPadding / 2
        y: Math.max(0, (root.senderHeaderHeight - height) / 2)
        width: Math.max(0, root.width - x - root.outerMargin)
        text: root.senderName
        elide: Text.ElideRight
        maximumLineCount: 1
        color: Qt.alpha(Kirigami.Theme.textColor, 0.72)
        font.weight: Font.DemiBold
        font.pointSize: Kirigami.Theme.smallFont.pointSize * 0.92
    }

    AvatarImage {
        id: senderAvatar

        visible: root.showSenderHeader
        x: root.outerMargin + Math.max(0, root.senderGutterWidth - width) / 2
        y: Math.max(0, (root.senderHeaderHeight - height) / 2)
        width: root.senderAvatarSize
        height: root.senderAvatarSize
        avatarLocalPath: root.senderAvatarLocalPath
        initials: root.senderInitials
        backgroundColor: Qt.alpha(foregroundColor, 0.12)
    }
}
