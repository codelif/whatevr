pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Effects
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Item {
    id: root

    Kirigami.Theme.inherit: false
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    property string messageId: ""
    property string body: ""
    property string timeText: ""
    property string dateSeparatorText: ""
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
    property string mediaKind: ""
    property string mediaMimeType: ""
    property string mediaLocalPath: ""
    property string mediaThumbnailLocalPath: ""
    property int mediaIntrinsicWidth: 0
    property int mediaIntrinsicHeight: 0
    property bool mediaAnimated: false
    property bool pooled: false
    property bool activeInViewport: true
    property bool mediaDownloading: false
    property string mediaDownloadError: ""
    property int clearSelectionGeneration: 0
    property string activeSelectionMessageId: ""
    property string replyToMessageId: ""
    property string replyToSenderName: ""
    property string replyToText: ""
    property string replyToMediaKind: ""
    property string replyToMediaMimeType: ""
    property bool replyToOutgoing: false

    signal conversationFocusRequested()
    signal messageSelectionClaimed(string messageId)
    signal typeIntoComposerRequested(string text)
    signal replyRequested(string messageId, string senderName, string text, string mediaKind, string mediaMimeType, bool outgoing)
    signal replyPreviewActivated(string messageId)

    onClearSelectionGenerationChanged: {
        if (bodyText.visible && (activeSelectionMessageId.length === 0 || activeSelectionMessageId !== messageId)) {
            bodyText.deselect()
        }
    }

    property real listWidth: 0
    readonly property bool showDateSeparator: dateSeparatorText.length > 0
    readonly property bool hasReplyPreview: replyToMessageId.length > 0
    readonly property real dateSeparatorHeight: showDateSeparator
        ? dateSeparator.implicitHeight + Kirigami.Units.largeSpacing
        : 0
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
    readonly property real messageBaseY: dateSeparatorHeight + (senderHeaderHeight > 0 ? senderHeaderHeight + Kirigami.Units.smallSpacing / 2 : 0)
    readonly property real replyGlowPadding: Kirigami.Units.smallSpacing
    property real replyGlowOpacity: 0

    readonly property bool isSticker: mediaKind === "sticker"
                                      || (mediaKind.length === 0
                                          && mediaMimeType === "image/webp"
                                          && (mediaLocalPath.endsWith(".webp")
                                              || mediaThumbnailLocalPath.endsWith(".thumb.png")))
    readonly property bool isImage: mediaMimeType.startsWith("image/")
    readonly property bool isAnimatedSticker: isSticker && (mediaAnimated || mediaMimeType === "image/gif")
    readonly property bool isLottieSticker: isSticker && mediaMimeType === "application/was"
    readonly property bool isRenderableStickerImage: isSticker && isImage && !isLottieSticker
    readonly property bool hasLocalImage: isImage && mediaLocalPath.length > 0
    readonly property bool hasThumbnailImage: isImage && mediaThumbnailLocalPath.length > 0
    readonly property bool hasLocalSticker: isSticker
                                               && mediaLocalPath.length > 0
                                               && (!isLottieSticker || mediaLocalPath.endsWith(".json"))
    readonly property bool mediaSourceActive: !pooled
    readonly property bool animationActive: mediaSourceActive && activeInViewport
    readonly property real imageSourceScale: Math.max(1, Screen.devicePixelRatio)

    // Decode images at a stable, layout-independent resolution. Binding
    // sourceSize to the live displayed width re-decodes the image on every
    // resize frame (flashing the loading state); these caps match the largest
    // size each media kind can ever be shown at, so a resize only rescales an
    // already-decoded pixmap instead of reloading it.
    readonly property int imageDecodeWidthCap: Math.max(1, Math.ceil(Kirigami.Units.gridUnit * 28 * imageSourceScale))
    readonly property int imageDecodeHeightCap: Math.max(1, Math.ceil(Kirigami.Units.gridUnit * 24 * imageSourceScale))
    readonly property int stickerDecodeCap: Math.max(1, Math.ceil(Kirigami.Units.gridUnit * 9 * imageSourceScale))

    // Image geometry must not depend on Image.implicitWidth/implicitHeight.
    // Those values arrive after decode and would resize the delegate while the
    // ListView is already scrolling. Reserve a frame from message metadata when
    // it is present; otherwise use a stable thumbnail shape for the lifetime of
    // this delegate.
    // Images render edge-to-edge (no inner padding), so they clamp against the
    // full bubble width rather than the padded content width.
    readonly property real minImageWidth: Math.min(maxBubbleWidth, Kirigami.Units.gridUnit * 7)
    readonly property real fallbackImageWidth: Math.min(maxBubbleWidth, Kirigami.Units.gridUnit * 18)
    // Give an image that carries a caption a comfortable minimum width so the
    // caption does not wrap into a tall sliver. Aspect ratio is always preserved
    // (the slot keeps width / aspect), so this only enlarges the slot.
    readonly property real captionMinImageWidth: hasBody
        ? Math.min(maxBubbleWidth, Kirigami.Units.gridUnit * 12)
        : 0
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
    onMediaIntrinsicWidthChanged: resetReservedImageGeometry()
    onMediaIntrinsicHeightChanged: resetReservedImageGeometry()
    Component.onCompleted: resetReservedImageGeometry()

    readonly property real imageDisplayWidth: {
        if (!isImage) {
            return 0
        }

        let minW = Math.max(minImageWidth, captionMinImageWidth)
        let width = Math.min(maxBubbleWidth, Math.max(minW, reservedImageNaturalWidth))
        if (width / reservedImageAspectRatio > maxImageHeight) {
            width = maxImageHeight * reservedImageAspectRatio
        }
        return Math.max(1, Math.min(maxBubbleWidth, width))
    }

    readonly property real imageDisplayHeight: {
        if (!isImage) {
            return 0
        }

        return Math.max(1, Math.min(maxImageHeight, imageDisplayWidth / reservedImageAspectRatio))
    }

    readonly property real stickerDisplayWidth: isSticker
        ? Math.max(1, Math.min(Math.max(0, listWidth - outerMargin * 2 - senderGutterWidth),
                               Kirigami.Units.gridUnit * 9))
        : 0
    readonly property real stickerDisplayHeight: isSticker
        ? stickerDisplayWidth
        : 0

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

    readonly property real statusIconSize: Kirigami.Units.iconSizes.small * 0.82
    readonly property real statusDoubleTickOffset: statusIconSize * 0.36
    // Reserve the widest receipt footprint so delivery/read updates do not
    // resize the bubble and change ListView spacing.
    readonly property real statusAreaWidth: statusIconSize * 1.32
    readonly property real tntSpacing: Kirigami.Units.smallSpacing / 2
    readonly property real tntGap: Kirigami.Units.smallSpacing / 2
    readonly property real inlineTntGap: Kirigami.Units.smallSpacing
    readonly property real tntWidth: footerMetrics.advanceWidth
                                     + (showStatusIcon ? statusAreaWidth + tntSpacing : 0)
    readonly property real tntHeight: Math.max(footerMetrics.height, showStatusIcon ? statusIconSize : 0)
    readonly property bool hasBody: body.length > 0
    readonly property bool hasInlineMedia: isImage && !isSticker
    readonly property bool imageOnly: hasInlineMedia && !hasBody
    readonly property string widestBodyLine: widestLine(body)
    // Captions inside an image bubble wrap to the (padded) image width; plain
    // text bubbles wrap to the full available content width.
    readonly property real textWrapWidth: hasInlineMedia ? innerContentWidth : maxContentWidth
    readonly property real naturalTextWidth: Math.min(textWrapWidth, widestBodyLineMetrics.advanceWidth)
    readonly property bool bodyHasExplicitLineBreaks: body.indexOf("\n") !== -1
    readonly property bool bodyLongEnoughForInlineTnt: naturalTextWidth >= tntWidth * 1.1
    readonly property bool tntFitsInline: hasBody
                                         && !bodyHasExplicitLineBreaks
                                         && bodyLongEnoughForInlineTnt
                                         && naturalTextWidth + tntWidth + inlineTntGap <= textWrapWidth
    readonly property real inlineTntReserve: 0
    readonly property real inlineTntYOffset: Kirigami.Units.smallSpacing / 2
    readonly property real blockTntReserve: tntHeight + tntGap

    function widestLine(text) {
        let best = ""
        let bestWidth = 0
        const lines = String(text || "").split("\n")
        for (let i = 0; i < lines.length; ++i) {
            const lineWidth = bodyFontMetrics.advanceWidth(lines[i])
            if (lineWidth > bestWidth) {
                best = lines[i]
                bestWidth = lineWidth
            }
        }
        return best
    }

    function currentSenderNameForReply() {
        return root.outgoing ? Whatevr.I18n.i18nc("@label quoted own message sender", "You") : root.senderName
    }

    function requestReply() {
        if (root.messageId.length === 0) {
            return
        }
        root.triggerReplyGlow()
        root.messageSelectionClaimed(root.messageId)
        root.replyRequested(root.messageId, root.currentSenderNameForReply(), root.body, root.mediaKind, root.mediaMimeType, root.outgoing)
        root.conversationFocusRequested()
    }

    function triggerReplyGlow() {
        replyGlowAnimation.restart()
    }

    // Absolute y within contentColumn (which now spans the full bubble at y:0).
    // The reply preview and caption text are inset by innerPadding; media is
    // edge-to-edge and sits flush at the top when it is the first region.
    function contentOffsetBeforeMedia() {
        return replyPreview.visible
            ? root.innerPadding + replyPreview.height + Kirigami.Units.smallSpacing
            : 0
    }

    function contentOffsetBeforeBody() {
        if (mediaSlot.visible) {
            return mediaSlot.y + mediaSlot.height + Kirigami.Units.smallSpacing
        }
        if (replyPreview.visible) {
            return root.innerPadding + replyPreview.height + Kirigami.Units.smallSpacing
        }
        return root.innerPadding
    }

    function contentOffsetBeforeFooter() {
        if (bodyText.visible) {
            return bodyText.y + bodyText.height
        }
        if (mediaSlot.visible) {
            return mediaSlot.y + mediaSlot.height
        }
        if (replyPreview.visible) {
            return root.innerPadding + replyPreview.height + Kirigami.Units.smallSpacing
        }
        return root.innerPadding
    }

    // Natural width the reply preview wants for its content, floored so a tiny
    // quote stays legible but no longer inflates the bubble to a fixed minimum.
    readonly property real replyPreviewNaturalWidth: hasReplyPreview
        ? Math.min(maxContentWidth, Math.max(Kirigami.Units.gridUnit * 5,
                                             replyPreview.naturalContentWidth))
        : 0

    // Width of the text/reply content for non-image bubbles (image bubbles are
    // sized by the image instead — see bubbleContentWidth).
    readonly property real contentBlockWidth: {
        let w = 0
        if (body.length > 0) {
            let bodyW = naturalTextWidth
            if (tntFitsInline) {
                bodyW = Math.min(maxContentWidth, naturalTextWidth + tntWidth + inlineTntGap)
            }
            w = Math.max(w, bodyW)
        }
        if (hasReplyPreview) {
            w = Math.max(w, replyPreviewNaturalWidth)
        }
        w = Math.max(w, Math.min(maxContentWidth, tntWidth))
        return Math.max(w, hasBody ? Kirigami.Units.gridUnit * 2 : Kirigami.Units.gridUnit * 4)
    }

    // The image drives the bubble width and spans it edge-to-edge; everything
    // else (caption, reply preview, footer) wraps/elides within the padded
    // inner width.
    readonly property real bubbleContentWidth: hasInlineMedia ? imageDisplayWidth : contentBlockWidth
    readonly property real innerContentWidth: Math.max(Kirigami.Units.gridUnit * 2,
                                                       bubbleContentWidth - innerPadding * 2)
    readonly property real textRegionWidth: hasInlineMedia ? innerContentWidth : contentBlockWidth

    // Footer (time + ticks) colours. Over the image-only vignette they switch to
    // light tones for contrast; otherwise the muted theme colours are used.
    readonly property color footerTextColor: imageOnly ? "white" : Kirigami.Theme.disabledTextColor
    readonly property color statusTickColor: statusIsRead
        ? (imageOnly ? Qt.lighter(activePalette.highlight, 1.4) : activePalette.highlight)
        : (imageOnly ? "white" : Kirigami.Theme.disabledTextColor)
    readonly property color statusSingleColor: statusIsFailed
        ? Kirigami.Theme.negativeTextColor
        : (imageOnly ? "white" : Kirigami.Theme.disabledTextColor)

    // Per-corner radii for the edge-to-edge media. Top corners follow the
    // bubble's top corners; bottom corners are only rounded for image-only
    // messages (when there is a caption the image meets the text squarely).
    readonly property real bubbleCornerRadius: Kirigami.Units.cornerRadius
    readonly property real mediaTopLeftRadius: (!outgoing && !groupStart) ? bubbleCornerRadius * 0.45 : bubbleCornerRadius
    readonly property real mediaTopRightRadius: (outgoing && !groupStart) ? bubbleCornerRadius * 0.45 : bubbleCornerRadius
    readonly property real mediaBottomLeftRadius: !imageOnly ? 0 : ((!outgoing && !groupEnd) ? bubbleCornerRadius * 0.45 : bubbleCornerRadius)
    readonly property real mediaBottomRightRadius: !imageOnly ? 0 : ((outgoing && !groupEnd) ? bubbleCornerRadius * 0.45 : bubbleCornerRadius)
    readonly property real replyGlowLeft: isSticker
        ? Math.min(stickerSlot.x,
                   stickerReplyPreview.visible ? stickerReplyPreview.x : stickerSlot.x,
                   stickerFooter.x)
        : bubble.x
    readonly property real replyGlowTop: isSticker
        ? Math.min(stickerSlot.y,
                   stickerReplyPreview.visible ? stickerReplyPreview.y : stickerSlot.y,
                   stickerFooter.y)
        : bubble.y
    readonly property real replyGlowRight: isSticker
        ? Math.max(stickerSlot.x + stickerSlot.width,
                   stickerReplyPreview.visible ? stickerReplyPreview.x + stickerReplyPreview.width : stickerSlot.x + stickerSlot.width,
                   stickerFooter.x + stickerFooter.width)
        : bubble.x + bubble.width
    readonly property real replyGlowBottom: isSticker
        ? Math.max(stickerSlot.y + stickerSlot.height,
                   stickerReplyPreview.visible ? stickerReplyPreview.y + stickerReplyPreview.height : stickerSlot.y + stickerSlot.height,
                   stickerFooter.y + stickerFooter.height)
        : bubble.y + bubble.height

    width: listWidth
    height: (isSticker
        ? stickerFooter.y + stickerFooter.height
        : bubble.y + bubble.height) + (groupEnd ? Kirigami.Units.smallSpacing : Kirigami.Units.smallSpacing / 4)

    SystemPalette {
        id: activePalette
        colorGroup: SystemPalette.Active
    }

    HoverHandler {
        id: rowHoverHandler

        acceptedDevices: PointerDevice.Mouse | PointerDevice.TouchPad
    }

    TapHandler {
        acceptedButtons: Qt.LeftButton
        onDoubleTapped: {
            if (root.messageId.length > 0) {
                root.requestReply()
            }
        }
    }

    SequentialAnimation {
        id: replyGlowAnimation

        PropertyAction {
            target: root
            property: "replyGlowOpacity"
            value: 0
        }
        NumberAnimation {
            target: root
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
            target: root
            property: "replyGlowOpacity"
            from: 1
            to: 0
            duration: Kirigami.Units.longDuration
            easing.type: Easing.OutCubic
        }
    }

    TextMetrics {
        id: bodyMetrics
        text: root.body
        font: bodyText.font
    }

    TextMetrics {
        id: widestBodyLineMetrics
        text: root.widestBodyLine
        font: bodyText.font
    }

    FontMetrics {
        id: bodyFontMetrics
        font: bodyText.font
    }

    TextMetrics {
        id: footerMetrics
        text: root.timeText
        font: timeLabel.font
    }

    Kirigami.ShadowedRectangle {
        id: bubble

        visible: !root.isSticker

        readonly property real bubbleRadius: Kirigami.Units.cornerRadius

        x: root.outgoing
           ? root.width - width - root.outerMargin
           : root.outerMargin + root.senderGutterWidth
        y: root.messageBaseY
        // Image bubbles are exactly the image width (edge-to-edge); text bubbles
        // add inner padding on both sides. Height comes from the self-padding
        // content region.
        width: root.bubbleContentWidth + (root.hasInlineMedia ? 0 : root.innerPadding * 2)
        height: contentColumn.height

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

            x: 0
            y: 0
            width: bubble.width
            height: {
                // Image-only: media is flush to the bubble bottom (footer is
                // overlaid on it), so the height ends at the media bottom.
                if (root.imageOnly) {
                    return mediaSlot.y + mediaSlot.height
                }
                let bottom = 0
                if (bodyText.visible) {
                    bottom = root.tntFitsInline
                        ? bodyText.y + bodyText.height
                        : footerSlot.y + footerSlot.height
                } else {
                    bottom = footerSlot.y + footerSlot.height
                }
                return bottom + root.innerPadding
            }

            ReplyPreview {
                id: replyPreview

                visible: root.hasReplyPreview
                x: root.innerPadding
                y: root.innerPadding
                width: root.textRegionWidth
                senderName: root.replyToSenderName
                body: root.replyToText
                mediaKind: root.replyToMediaKind
                mediaMimeType: root.replyToMediaMimeType
                targetMessageId: root.replyToMessageId
                outgoing: root.replyToOutgoing
                fillColor: Qt.alpha(Kirigami.Theme.textColor, root.outgoing ? 0.06 : 0.045)
                borderColor: Qt.alpha(Kirigami.Theme.textColor, 0.07)
                onActivated: messageId => root.replyPreviewActivated(messageId)
            }

            Item {
                id: mediaSlot

                visible: root.isImage && !root.isSticker
                x: 0
                y: root.contentOffsetBeforeMedia()
                width: root.imageDisplayWidth
                height: visible ? root.imageDisplayHeight : 0
                clip: true

                Kirigami.ShadowedRectangle {
                    id: mediaBackground

                    anchors.fill: parent
                    corners.topLeftRadius: root.mediaTopLeftRadius
                    corners.topRightRadius: root.mediaTopRightRadius
                    corners.bottomLeftRadius: root.mediaBottomLeftRadius
                    corners.bottomRightRadius: root.mediaBottomRightRadius
                    color: Qt.alpha(Kirigami.Theme.textColor, 0.06)
                    border.color: Qt.alpha(Kirigami.Theme.textColor, 0.12)
                    border.width: 1
                }

                Image {
                    id: thumb

                    anchors.fill: parent
                    visible: !root.hasLocalImage && root.hasThumbnailImage
                    opacity: status === Image.Ready ? 0.78 : 0
                    source: root.mediaSourceActive && mediaSlot.visible && visible ? Qt.resolvedUrl("file://" + root.mediaThumbnailLocalPath) : ""
                    fillMode: Image.PreserveAspectCrop
                    asynchronous: true
                    cache: true
                    smooth: true
                    sourceSize.width: root.imageDecodeWidthCap
                    sourceSize.height: root.imageDecodeHeightCap

                    layer.enabled: root.activeInViewport && visible && status === Image.Ready
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
                    source: root.mediaSourceActive && mediaSlot.visible && root.hasLocalImage ? Qt.resolvedUrl("file://" + root.mediaLocalPath) : ""
                    fillMode: Image.PreserveAspectFit
                    asynchronous: true
                    cache: false
                    smooth: true
                    sourceSize.width: root.imageDecodeWidthCap
                    sourceSize.height: root.imageDecodeHeightCap

                    layer.enabled: root.activeInViewport && visible && status === Image.Ready
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

                Kirigami.ShadowedRectangle {
                    id: imageMask

                    anchors.fill: parent
                    corners.topLeftRadius: root.mediaTopLeftRadius
                    corners.topRightRadius: root.mediaTopRightRadius
                    corners.bottomLeftRadius: root.mediaBottomLeftRadius
                    corners.bottomRightRadius: root.mediaBottomRightRadius
                    color: "black"
                    visible: false
                    layer.enabled: root.activeInViewport && mediaSlot.visible
                }

                // Dark scrim behind the time+ticks overlaid on image-only
                // messages. A uniform radius is fine here: the top corners sit in
                // the transparent part of the gradient, so only the rounded
                // bottom corners are visible and they line up with the image.
                Rectangle {
                    id: mediaScrim

                    visible: root.imageOnly
                    anchors.left: parent.left
                    anchors.right: parent.right
                    anchors.bottom: parent.bottom
                    height: Math.min(parent.height, Kirigami.Units.gridUnit * 2.4)
                    radius: Math.max(root.mediaBottomLeftRadius, root.mediaBottomRightRadius)
                    gradient: Gradient {
                        GradientStop { position: 0.0; color: "transparent" }
                        GradientStop { position: 1.0; color: Qt.rgba(0, 0, 0, 0.5) }
                    }
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

                    Kirigami.ShadowedRectangle {
                        anchors.fill: parent
                        corners.topLeftRadius: root.mediaTopLeftRadius
                        corners.topRightRadius: root.mediaTopRightRadius
                        corners.bottomLeftRadius: root.mediaBottomLeftRadius
                        corners.bottomRightRadius: root.mediaBottomRightRadius
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
                            text: Whatevr.I18n.i18nc("@action:button", "Load image")
                            enabled: root.messageId.length > 0
                            onClicked: {
                                Whatevr.AppController.downloadMessageMedia(root.messageId)
                                root.conversationFocusRequested()
                            }
                        }

                        Label {
                            anchors.horizontalCenter: parent.horizontalCenter
                            width: parent.width
                            visible: img.status === Image.Error && root.hasLocalImage
                            text: Whatevr.I18n.i18nc("@info", "Image could not be displayed")
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

            TextEdit {
                id: bodyText

                visible: root.body.length > 0
                x: root.innerPadding
                y: root.contentOffsetBeforeBody()
                width: root.textRegionWidth
                text: root.body
                readOnly: true
                selectByMouse: true
                selectByKeyboard: true
                persistentSelection: true
                wrapMode: TextEdit.Wrap
                textFormat: TextEdit.PlainText
                color: Kirigami.Theme.textColor

                onSelectedTextChanged: {
                    if (selectedText.length > 0) {
                        root.messageSelectionClaimed(root.messageId)
                    }
                }

                Keys.onPressed: event => {
                    if (event.modifiers & (Qt.ControlModifier | Qt.AltModifier | Qt.MetaModifier)) {
                        return
                    }
                    if (event.text.length === 0 || event.text.charCodeAt(0) < 0x20) {
                        return
                    }

                    root.typeIntoComposerRequested(event.text)
                    event.accepted = true
                }
            }

            Item {
                id: footerSlot

                // Image-only: overlay on the media bottom-right (over the
                // vignette). Otherwise sit at the right inner edge, inline with
                // the last text line or on its own row.
                x: root.imageOnly
                   ? mediaSlot.x + mediaSlot.width - width - Kirigami.Units.smallSpacing
                   : Math.max(0, parent.width - root.innerPadding - width)
                y: {
                    if (root.imageOnly) {
                        return mediaSlot.y + mediaSlot.height - height - Kirigami.Units.smallSpacing
                    }
                    const off = root.contentOffsetBeforeFooter()
                    if (bodyText.visible) {
                        return root.tntFitsInline ? off - height + root.inlineTntReserve + root.inlineTntYOffset : off + root.tntGap
                    }
                    return Math.max(0, off)
                }
                width: root.tntWidth
                height: root.tntHeight

                Item {
                    id: statusArea
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    visible: root.showStatusIcon
                    implicitWidth: root.statusAreaWidth
                    implicitHeight: root.statusIconSize
                    width: implicitWidth
                    height: implicitHeight

                    // Single icon (clock, single checkmark, error)
                    Kirigami.Icon {
                        id: singleIcon
                        anchors.centerIn: parent
                        visible: !root.statusIsDoubleTick
                        source: root.statusSingleIcon
                        implicitWidth: root.statusIconSize
                        implicitHeight: root.statusIconSize
                        color: root.statusSingleColor
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
                            color: root.statusTickColor
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
                            color: root.statusTickColor
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
                    anchors.right: statusArea.visible ? statusArea.left : parent.right
                    anchors.rightMargin: statusArea.visible ? root.tntSpacing : 0
                    anchors.verticalCenter: parent.verticalCenter
                    text: root.timeText
                    color: root.footerTextColor
                    font.pointSize: Kirigami.Theme.smallFont.pointSize * 0.72
                }
            }
        }
    }

    ReplyPreview {
        id: stickerReplyPreview

        visible: root.isSticker && root.hasReplyPreview
        x: root.outgoing
           ? root.width - width - root.outerMargin
           : root.outerMargin + root.senderGutterWidth
        y: root.messageBaseY
        width: Math.min(Math.max(0, root.width - root.outerMargin * 2 - root.senderGutterWidth),
                        Math.max(Kirigami.Units.gridUnit * 5, stickerReplyPreview.naturalContentWidth))
        senderName: root.replyToSenderName
        body: root.replyToText
        mediaKind: root.replyToMediaKind
        mediaMimeType: root.replyToMediaMimeType
        targetMessageId: root.replyToMessageId
        outgoing: root.replyToOutgoing
        fillColor: Qt.alpha(Kirigami.Theme.backgroundColor, 0.78)
        borderColor: Qt.alpha(Kirigami.Theme.textColor, 0.08)
        onActivated: messageId => root.replyPreviewActivated(messageId)
    }

    Item {
        id: stickerSlot

        visible: root.isSticker
        x: root.outgoing
           ? root.width - width - root.outerMargin
           : root.outerMargin + root.senderGutterWidth
        y: root.messageBaseY + (stickerReplyPreview.visible ? stickerReplyPreview.height + Kirigami.Units.smallSpacing : 0)
        width: root.stickerDisplayWidth
        height: root.stickerDisplayHeight

        Image {
            id: stickerThumb

            anchors.fill: parent
            visible: root.isSticker && !root.hasLocalSticker && root.hasThumbnailImage
            opacity: status === Image.Ready ? 0.7 : 0
            source: root.mediaSourceActive && stickerSlot.visible && visible ? Qt.resolvedUrl("file://" + root.mediaThumbnailLocalPath) : ""
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            cache: false
            smooth: true
            sourceSize.width: root.stickerDecodeCap
            sourceSize.height: root.stickerDecodeCap
        }

        Image {
            id: staticSticker

            anchors.fill: parent
            visible: root.isRenderableStickerImage && root.hasLocalSticker && !root.isAnimatedSticker
            opacity: status === Image.Ready ? 1 : 0
            source: root.mediaSourceActive && stickerSlot.visible && visible ? Qt.resolvedUrl("file://" + root.mediaLocalPath) : ""
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            cache: false
            smooth: true
            sourceSize.width: root.stickerDecodeCap
            sourceSize.height: root.stickerDecodeCap
        }

        AnimatedImage {
            id: animatedSticker

            anchors.fill: parent
            visible: root.isRenderableStickerImage && root.hasLocalSticker && root.isAnimatedSticker
            playing: root.animationActive && visible && status === AnimatedImage.Ready
            source: root.mediaSourceActive && stickerSlot.visible && visible ? Qt.resolvedUrl("file://" + root.mediaLocalPath) : ""
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            cache: false
            smooth: true
            sourceSize.width: root.stickerDecodeCap
            sourceSize.height: root.stickerDecodeCap
        }

        Whatevr.RlottieSticker {
            id: lottieSticker

            anchors.fill: parent
            visible: root.isLottieSticker && root.hasLocalSticker
            source: root.mediaSourceActive && stickerSlot.visible && visible ? Qt.resolvedUrl("file://" + root.mediaLocalPath) : ""
            playing: root.animationActive && visible
            renderScale: 2.5
        }

        Item {
            id: stickerOverlay

            anchors.fill: parent
            visible: !root.hasLocalSticker
                     || root.mediaDownloading
                     || stickerThumb.status === Image.Loading
                     || staticSticker.status === Image.Loading
                     || animatedSticker.status === AnimatedImage.Loading
                     || lottieSticker.status === Whatevr.RlottieSticker.Loading
                     || staticSticker.status === Image.Error
                     || animatedSticker.status === AnimatedImage.Error
                     || lottieSticker.status === Whatevr.RlottieSticker.Error
                     || root.mediaDownloadError.length > 0

            Rectangle {
                anchors.centerIn: parent
                width: stickerOverlayColumn.width + Kirigami.Units.largeSpacing
                height: stickerOverlayColumn.height + Kirigami.Units.smallSpacing * 2
                radius: Kirigami.Units.cornerRadius
                visible: false
                color: Qt.alpha(Kirigami.Theme.backgroundColor, 0.72)
                border.color: Qt.alpha(Kirigami.Theme.textColor, 0.10)
            }

            Column {
                id: stickerOverlayColumn

                anchors.centerIn: parent
                width: Math.max(0, parent.width - Kirigami.Units.largeSpacing * 1.5)
                spacing: Kirigami.Units.smallSpacing

                BusyIndicator {
                    anchors.horizontalCenter: parent.horizontalCenter
                    visible: root.mediaDownloading
                             || (!root.hasLocalSticker && root.hasThumbnailImage && stickerThumb.status === Image.Loading)
                             || (root.hasLocalSticker && (staticSticker.status === Image.Loading
                                                          || animatedSticker.status === AnimatedImage.Loading
                                                          || lottieSticker.status === Whatevr.RlottieSticker.Loading))
                    running: visible
                    implicitWidth: Kirigami.Units.gridUnit * 1.75
                    implicitHeight: implicitWidth
                }

                Button {
                    anchors.horizontalCenter: parent.horizontalCenter
                    visible: !root.hasLocalSticker && !root.mediaDownloading
                    flat: true
                    icon.name: "folder-download-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button", "Load sticker")
                    enabled: root.messageId.length > 0
                    onClicked: {
                        Whatevr.AppController.downloadMessageMedia(root.messageId)
                        root.conversationFocusRequested()
                    }
                }

                Label {
                    anchors.horizontalCenter: parent.horizontalCenter
                    width: parent.width
                    visible: (staticSticker.status === Image.Error
                              || animatedSticker.status === AnimatedImage.Error
                              || lottieSticker.status === Whatevr.RlottieSticker.Error) && root.hasLocalSticker
                    text: Whatevr.I18n.i18nc("@info", "Sticker could not be displayed")
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

    Rectangle {
        id: stickerFooter

        visible: root.isSticker
        x: root.outgoing ? stickerSlot.x + stickerSlot.width - width : stickerSlot.x
        y: stickerSlot.y + stickerSlot.height + Kirigami.Units.smallSpacing / 2
        width: stickerFooterRow.implicitWidth + Kirigami.Units.smallSpacing
        height: Math.max(stickerFooterRow.implicitHeight + Kirigami.Units.smallSpacing / 2, Kirigami.Units.gridUnit * 1.15)
        radius: Kirigami.Units.cornerRadius
        color: root.outgoing
               ? Qt.alpha(activePalette.highlight, 0.30)
               : Qt.alpha(Kirigami.Theme.backgroundColor, 0.76)
        border.color: Qt.alpha(Kirigami.Theme.textColor, root.outgoing ? 0.05 : 0.12)

        Row {
            id: stickerFooterRow

            anchors.centerIn: parent
            spacing: Kirigami.Units.smallSpacing / 2

            Label {
                text: root.timeText
                color: Kirigami.Theme.disabledTextColor
                font.pointSize: Kirigami.Theme.smallFont.pointSize * 0.8
            }

            Item {
                visible: root.showStatusIcon
                width: root.statusAreaWidth
                height: root.statusIconSize

                Kirigami.Icon {
                    id: stickerSingleIcon
                    anchors.centerIn: parent
                    visible: !root.statusIsDoubleTick
                    source: root.statusSingleIcon
                    implicitWidth: root.statusIconSize
                    implicitHeight: root.statusIconSize
                    color: root.statusIsFailed ? Kirigami.Theme.negativeTextColor : Kirigami.Theme.disabledTextColor
                    isMask: true
                }
                Kirigami.Icon {
                    anchors.centerIn: parent
                    anchors.horizontalCenterOffset: 0.75
                    visible: stickerSingleIcon.visible && root.statusSingleIcon === "checkmark"
                    source: stickerSingleIcon.source
                    implicitWidth: stickerSingleIcon.implicitWidth
                    implicitHeight: stickerSingleIcon.implicitHeight
                    color: stickerSingleIcon.color
                    isMask: true
                }

                Item {
                    anchors.fill: parent
                    visible: root.statusIsDoubleTick

                    Kirigami.Icon {
                        id: firstStickerTick
                        x: 0
                        anchors.verticalCenter: parent.verticalCenter
                        source: "checkmark"
                        implicitWidth: root.statusIconSize
                        implicitHeight: root.statusIconSize
                        color: root.statusIsRead ? activePalette.highlight : Kirigami.Theme.disabledTextColor
                        isMask: true
                    }
                    Kirigami.Icon {
                        x: firstStickerTick.x + 0.75
                        anchors.verticalCenter: parent.verticalCenter
                        source: firstStickerTick.source
                        implicitWidth: firstStickerTick.implicitWidth
                        implicitHeight: firstStickerTick.implicitHeight
                        color: firstStickerTick.color
                        isMask: true
                    }
                    Kirigami.Icon {
                        id: secondStickerTick
                        x: root.statusDoubleTickOffset
                        anchors.verticalCenter: parent.verticalCenter
                        source: "checkmark"
                        implicitWidth: root.statusIconSize
                        implicitHeight: root.statusIconSize
                        color: root.statusIsRead ? activePalette.highlight : Kirigami.Theme.disabledTextColor
                        isMask: true
                    }
                    Kirigami.Icon {
                        x: secondStickerTick.x + 0.75
                        anchors.verticalCenter: parent.verticalCenter
                        source: secondStickerTick.source
                        implicitWidth: secondStickerTick.implicitWidth
                        implicitHeight: secondStickerTick.implicitHeight
                        color: secondStickerTick.color
                        isMask: true
                    }
                }
            }
        }
    }

    Item {
        id: replyGlowOverlay

        readonly property real innerMargin: Math.max(1, Math.round(Kirigami.Units.smallSpacing / 2))

        visible: root.replyGlowOpacity > 0
        opacity: root.replyGlowOpacity
        x: Math.round(root.replyGlowLeft - root.replyGlowPadding)
        y: Math.round(root.replyGlowTop - root.replyGlowPadding)
        z: 7
        width: Math.max(0, Math.round(root.replyGlowRight - root.replyGlowLeft + root.replyGlowPadding * 2))
        height: Math.max(0, Math.round(root.replyGlowBottom - root.replyGlowTop + root.replyGlowPadding * 2))

        Rectangle {
            id: replyGlowOuter

            anchors.fill: parent
            radius: Kirigami.Units.cornerRadius + root.replyGlowPadding
            color: Qt.alpha(Kirigami.Theme.highlightColor, 0.06)
            border.color: Qt.alpha(Kirigami.Theme.highlightColor, 0.72)
            border.width: Math.max(2, Math.round(Kirigami.Units.smallSpacing / 2))
        }

        Rectangle {
            anchors.fill: parent
            anchors.margins: replyGlowOverlay.innerMargin
            radius: Math.max(0, replyGlowOuter.radius - replyGlowOverlay.innerMargin)
            color: "transparent"
            border.color: Qt.alpha(Kirigami.Theme.highlightColor, 0.28)
            border.width: 1
        }
    }

    ToolButton {
        id: replyButton

        readonly property real visualX: root.isSticker ? stickerSlot.x : bubble.x
        readonly property real visualY: root.isSticker ? stickerSlot.y : bubble.y
        readonly property real visualWidth: root.isSticker ? stickerSlot.width : bubble.width
        readonly property real visualHeight: root.isSticker ? stickerSlot.height : bubble.height
        readonly property real desiredX: root.outgoing
                                         ? visualX - width - Kirigami.Units.smallSpacing
                                         : visualX + visualWidth + Kirigami.Units.smallSpacing

        visible: root.messageId.length > 0 && !root.pooled
        enabled: opacity > 0.01
        opacity: (rowHoverHandler.hovered || hovered || pressed) ? 1 : 0
        x: Math.round(Math.max(root.outerMargin,
                               Math.min(root.width - root.outerMargin - width, desiredX)))
        y: Math.round(visualY + Math.max(0, visualHeight - height) / 2)
        z: 8
        width: Math.round(Math.max(Kirigami.Units.iconSizes.smallMedium + Kirigami.Units.smallSpacing,
                                   Math.min(Kirigami.Units.gridUnit * 1.45,
                                            visualHeight - Kirigami.Units.smallSpacing)))
        height: width
        icon.name: "edit-undo"
        icon.width: Kirigami.Units.iconSizes.smallMedium
        icon.height: icon.width
        text: Whatevr.I18n.i18nc("@action:button", "Reply")
        display: AbstractButton.IconOnly
        focusPolicy: Qt.NoFocus
        hoverEnabled: true
        onClicked: root.requestReply()

        contentItem: Item {
            Kirigami.Icon {
                anchors.centerIn: parent
                source: replyButton.icon.name
                width: replyButton.icon.width
                height: replyButton.icon.height
                color: Kirigami.Theme.textColor
                isMask: true
            }
        }

        background: Rectangle {
            radius: width / 2
            color: Qt.alpha(Kirigami.Theme.backgroundColor, replyButton.hovered || replyButton.pressed ? 0.98 : 0.9)
            border.color: Qt.alpha(Kirigami.Theme.textColor, replyButton.hovered || replyButton.pressed ? 0.24 : 0.14)
            border.width: 1
        }

        Behavior on opacity {
            NumberAnimation {
                duration: Kirigami.Units.shortDuration
                easing.type: Easing.OutCubic
            }
        }
    }

    Label {
        id: senderHeader

        visible: root.showSenderHeader && root.senderName.length > 0
        x: bubble.x + root.innerPadding / 2
        y: root.dateSeparatorHeight + Math.max(0, (root.senderHeaderHeight - height) / 2)
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
        y: root.dateSeparatorHeight + Math.max(0, (root.senderHeaderHeight - height) / 2)
        width: root.senderAvatarSize
        height: root.senderAvatarSize
        avatarLocalPath: root.senderAvatarLocalPath
        initials: root.senderInitials
        backgroundColor: Qt.alpha(foregroundColor, 0.12)
    }

    DateSeparatorPill {
        id: dateSeparator

        visible: root.showDateSeparator
        text: root.dateSeparatorText
        x: Math.round((root.width - width) / 2)
        y: Kirigami.Units.largeSpacing / 2
    }

}
