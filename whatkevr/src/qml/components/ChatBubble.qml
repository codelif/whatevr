pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Item {
    id: root

    Kirigami.Theme.inherit: false
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    property string messageId: ""
    property string body: ""
    property string layoutBody: ""
    property string replyPreviewBody: ""
    property int emojiOnlyCount: 0
    property bool hasRichText: false
    property string richText: ""
    property bool textTruncated: false
    property bool textExpanded: false
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
    // True while the list is being flung fast. Full-resolution media decoding is
    // held off during a fling so the cheap, already-cached thumbnail carries the
    // scroll; it upgrades to full-res the instant scrolling settles.
    property bool fastFlicking: false
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
    // Unwrapped widest/last line widths of the displayed body, measured in C++
    // (model roles). Replaces per-delegate TextMetrics + JS line splitting.
    property real widestLineWidth: 0
    property real lastLineWidth: 0
    // Advance width of the "Read more" label, measured once in MessageView and
    // shared by all delegates.
    property real readMoreTextWidth: 0
    // Latches true on first hover so the reply button is only ever instantiated
    // for rows the pointer actually visits; reset when the delegate is reused.
    property bool hoverLatched: false

    signal conversationFocusRequested()
    signal messageSelectionClaimed(string messageId)
    signal typeIntoComposerRequested(string text)
    signal replyRequested(string messageId, string senderName, string text, string mediaKind, string mediaMimeType, bool outgoing)
    signal replyPreviewActivated(string messageId)
    signal readMoreRequested(string messageId)

    onClearSelectionGenerationChanged: {
        if (bodyTextLoader.item && (activeSelectionMessageId.length === 0 || activeSelectionMessageId !== messageId)) {
            bodyTextLoader.item.deselect()
        }
    }

    property real listWidth: 0
    readonly property bool showDateSeparator: dateSeparatorText.length > 0
    readonly property bool hasReplyPreview: replyToMessageId.length > 0
    readonly property real dateSeparatorHeight: showDateSeparator
        ? dateSeparatorLoader.height + Kirigami.Units.largeSpacing
        : 0
    readonly property real outerMargin: Kirigami.Units.largeSpacing
    readonly property real innerPadding: Kirigami.Units.largeSpacing
    readonly property real senderAvatarSize: Kirigami.Units.gridUnit * 1.65
    readonly property real senderGutterWidth: showSenderGutter ? senderAvatarSize + Kirigami.Units.smallSpacing : 0
    readonly property real senderHeaderHeight: showSenderHeader
        ? Math.max(senderAvatarSize, senderHeaderLoader.item ? senderHeaderLoader.item.labelImplicitHeight : 0)
        : 0
    readonly property real maxBubbleWidth: Math.max(Kirigami.Units.gridUnit * 4,
                                                    Math.min(Math.max(0, listWidth - outerMargin * 2 - senderGutterWidth),
                                                              Kirigami.Units.gridUnit * 28))
    readonly property real maxContentWidth: Math.max(Kirigami.Units.gridUnit * 4, maxBubbleWidth - innerPadding * 2)
    readonly property real bodyPointSize: Kirigami.Theme.defaultFont.pointSize > 0
        ? Kirigami.Theme.defaultFont.pointSize
        : 10
    readonly property real messageBaseY: dateSeparatorHeight + (senderHeaderHeight > 0 ? senderHeaderHeight + Kirigami.Units.smallSpacing / 2 : 0)
    readonly property real replyGlowPadding: Kirigami.Units.smallSpacing
    property real replyGlowOpacity: 0

    readonly property bool isSticker: mediaKind === "sticker"
                                      || (mediaKind.length === 0
                                          && mediaMimeType === "image/webp"
                                          && (mediaLocalPath.endsWith(".webp")
                                              || mediaThumbnailLocalPath.endsWith(".thumb.png")))
    readonly property bool isImage: mediaMimeType.startsWith("image/")
    // 1-3 emoji-only messages render large and frameless, like stickers. The single
    // emoji case is biggest; size steps down as the count rises.
    readonly property bool isJumboEmoji: emojiOnlyCount > 0 && emojiOnlyCount <= 3
    readonly property bool frameless: isSticker || isJumboEmoji
    readonly property real jumboEmojiPixelSize: Kirigami.Units.gridUnit
        * (emojiOnlyCount === 1 ? 2.8 : emojiOnlyCount === 2 ? 2.2 : 1.8)
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
    // The placeholder thumbnail is decoded tiny on purpose: bilinear upscaling of
    // a low-resolution pixmap is the blur-up effect, so no blur shader is needed.
    readonly property int thumbnailDecodeCap: Math.max(16, Math.ceil(Kirigami.Units.gridUnit * 1.5))
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

        // Keep the slot aspect identical to the source aspect. Size caps below
        // bound the rendered area; clamping here would stretch panoramas.
        return width / height
    }

    function decodeWidthForAspect(maxWidth, maxHeight, aspectRatio) {
        if (aspectRatio <= 0) {
            return Math.max(1, maxWidth)
        }

        let width = maxWidth
        if (width / aspectRatio > maxHeight) {
            width = maxHeight * aspectRatio
        }
        return Math.max(1, Math.ceil(width))
    }

    function decodeHeightForAspect(maxWidth, maxHeight, aspectRatio) {
        if (aspectRatio <= 0) {
            return Math.max(1, maxHeight)
        }

        if (maxWidth / aspectRatio > maxHeight) {
            return Math.max(1, Math.ceil(maxHeight))
        }
        return Math.max(1, Math.ceil(maxWidth / aspectRatio))
    }

    function resetReservedImageGeometry() {
        reservedImageAspectRatio = normalisedImageAspectRatio(mediaIntrinsicWidth, mediaIntrinsicHeight)
        reservedImageNaturalWidth = mediaIntrinsicWidth > 0 ? mediaIntrinsicWidth : fallbackImageWidth
    }

    readonly property int imageDecodeWidth: decodeWidthForAspect(imageDecodeWidthCap, imageDecodeHeightCap, reservedImageAspectRatio)
    readonly property int imageDecodeHeight: decodeHeightForAspect(imageDecodeWidthCap, imageDecodeHeightCap, reservedImageAspectRatio)
    readonly property int thumbnailDecodeWidth: decodeWidthForAspect(thumbnailDecodeCap, thumbnailDecodeCap, reservedImageAspectRatio)
    readonly property int thumbnailDecodeHeight: decodeHeightForAspect(thumbnailDecodeCap, thumbnailDecodeCap, reservedImageAspectRatio)

    onMessageIdChanged: {
        resetReservedImageGeometry()
        hoverLatched = false
    }
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
        case 2: return root.tickSource             // sent (single tick)
        case 5: return "dialog-error-symbolic"      // failed
        default: return ""
        }
    }
    readonly property bool showStatusIcon: outgoing && (statusIsDoubleTick || statusSingleIcon.length > 0)

    readonly property real footerTimePointSize: Kirigami.Theme.smallFont.pointSize * 0.72
    readonly property real statusIconSize: Math.max(1, Math.round(Kirigami.Units.iconSizes.small * 0.82))
    readonly property real statusDoubleTickOffset: Math.max(1, Math.round(statusIconSize * 0.36))
    // Reserve the widest receipt footprint so delivery/read updates do not
    // resize the bubble and change ListView spacing.
    readonly property real statusAreaWidth: statusIconSize + statusDoubleTickOffset
    readonly property real tntSpacing: Math.max(1, Math.round(Kirigami.Units.smallSpacing / 2))
    readonly property real tntGap: Math.max(1, Math.round(Kirigami.Units.smallSpacing / 2))
    readonly property real inlineTntGap: Kirigami.Units.smallSpacing
    readonly property real framelessFooterHPadding: Math.max(1, Math.round(Kirigami.Units.smallSpacing / 2))
    readonly property real framelessFooterVPadding: Math.max(1, Math.round(Kirigami.Units.smallSpacing / 4))
    readonly property real bodyTopInsetCorrection: Math.max(1, Math.round(Kirigami.Units.smallSpacing / 2))
    // Shared tight footer inset; matches the image-overlay footer the design
    // follows, so text/sticker footers hug their corner the same way.
    readonly property real footerInset: Kirigami.Units.smallSpacing
    // Bold filled checkmark bundled with the app (Breeze's is too thin).
    readonly property url tickSource: "qrc:/data/icons/checkmark-bold.svg"
    readonly property real tntWidth: Math.ceil(footerMetrics.advanceWidth
                                               + (showStatusIcon ? statusAreaWidth + tntSpacing : 0))
    readonly property real tntHeight: Math.ceil(Math.max(footerMetrics.height, showStatusIcon ? statusIconSize : 0))
    readonly property bool hasBody: body.length > 0
    readonly property bool showReadMore: textTruncated && !textExpanded && hasBody
    readonly property string readMoreLabelText: Whatevr.I18n.i18nc("@action:button expand long message", "Read more")
    readonly property bool hasInlineMedia: isImage && !isSticker
    readonly property bool imageOnly: hasInlineMedia && !hasBody
    // Captions inside an image bubble wrap to the (padded) image width; plain
    // text bubbles wrap to the full available content width.
    readonly property real textWrapWidth: hasInlineMedia ? innerContentWidth : maxContentWidth
    readonly property real naturalTextWidth: Math.min(textWrapWidth, widestLineWidth)
    readonly property real naturalLastLineWidth: Math.min(textWrapWidth, lastLineWidth)
    readonly property bool canReserveInlineTntWidth: hasBody
                                                   && naturalLastLineWidth + inlineTntGap + tntWidth <= textWrapWidth
    readonly property rect bodyEndCursorRect: {
        const edit = bodyTextLoader.item
        if (!edit) {
            return Qt.rect(0, 0, 0, 0)
        }
        const bodyWidth = edit.width
        const bodyHeight = edit.height
        const bodyLength = edit.length
        return bodyWidth > 0 && bodyHeight > 0
            ? edit.positionToRectangle(bodyLength)
            : Qt.rect(0, 0, 0, 0)
    }
    readonly property bool tntFitsInline: !showReadMore
                                         && hasBody
                                         && bodyTextLoader.item !== null
                                         && bodyEndCursorRect.x + inlineTntGap + tntWidth <= bodyTextLoader.width
    readonly property real inlineTntReserve: 0
    readonly property real inlineTntYOffset: Kirigami.Units.smallSpacing / 2
    readonly property real blockTntReserve: tntHeight + tntGap

    function currentSenderNameForReply() {
        return root.outgoing ? Whatevr.I18n.i18nc("@label quoted own message sender", "You") : root.senderName
    }

    function requestReply() {
        if (root.messageId.length === 0) {
            return
        }
        root.triggerReplyGlow()
        root.messageSelectionClaimed(root.messageId)
        root.replyRequested(root.messageId, root.currentSenderNameForReply(), root.replyPreviewBody.length > 0 ? root.replyPreviewBody : root.body, root.mediaKind, root.mediaMimeType, root.outgoing)
        root.conversationFocusRequested()
    }

    function triggerReplyGlow() {
        replyGlowAnimation.restart()
    }

    // Absolute y within contentColumn (which now spans the full bubble at y:0).
    // The reply preview and caption text are inset by innerPadding; media is
    // edge-to-edge and sits flush at the top when it is the first region.
    function contentOffsetBeforeMedia() {
        return root.hasReplyPreview
            ? root.innerPadding + replyPreviewLoader.height + Kirigami.Units.smallSpacing
            : 0
    }

    function contentOffsetBeforeBody() {
        if (mediaSlot.visible) {
            return mediaSlot.y + mediaSlot.height + Kirigami.Units.smallSpacing
        }
        if (root.hasReplyPreview) {
            return root.innerPadding + replyPreviewLoader.height + Kirigami.Units.smallSpacing - root.bodyTopInsetCorrection
        }
        return root.innerPadding - root.bodyTopInsetCorrection
    }

    function contentOffsetBeforeFooter() {
        if (root.showReadMore) {
            return readMoreLoader.y + readMoreLoader.height
        }
        if (root.hasBody) {
            return bodyTextLoader.y + bodyTextLoader.height
        }
        if (mediaSlot.visible) {
            return mediaSlot.y + mediaSlot.height
        }
        if (root.hasReplyPreview) {
            return root.innerPadding + replyPreviewLoader.height + Kirigami.Units.smallSpacing
        }
        return root.innerPadding
    }

    // Natural width the reply preview wants for its content, floored so a tiny
    // quote stays legible but no longer inflates the bubble to a fixed minimum.
    readonly property real replyPreviewNaturalWidth: hasReplyPreview
        ? Math.min(maxContentWidth, Math.max(Kirigami.Units.gridUnit * 5,
                                             replyPreviewLoader.item ? replyPreviewLoader.item.naturalContentWidth : 0))
        : 0

    // Width of the text/reply content for non-image bubbles (image bubbles are
    // sized by the image instead — see bubbleContentWidth).
    readonly property real contentBlockWidth: {
        let w = 0
        if (body.length > 0) {
            let bodyW = naturalTextWidth
            if (canReserveInlineTntWidth) {
                bodyW = Math.min(maxContentWidth,
                                 Math.max(naturalTextWidth,
                                          naturalLastLineWidth + inlineTntGap + tntWidth))
            }
            w = Math.max(w, bodyW)
        }
        if (hasReplyPreview) {
            w = Math.max(w, replyPreviewNaturalWidth)
        }
        if (showReadMore) {
            w = Math.max(w, Math.min(maxContentWidth, readMoreTextWidth + Kirigami.Units.smallSpacing * 2))
        }
        w = Math.max(w, Math.min(maxContentWidth, tntWidth))
        return Math.max(w, hasBody ? Kirigami.Units.gridUnit * 2 : Kirigami.Units.gridUnit * 4)
    }

    // The image drives the bubble width and spans it edge-to-edge; everything
    // else (caption, reply preview, footer) wraps/elides within the padded
    // inner width.
    readonly property real bubbleContentWidth: hasInlineMedia ? imageDisplayWidth : contentBlockWidth
    // Padded inner width for caption/reply content inside an image bubble. Derived
    // straight from imageDisplayWidth rather than bubbleContentWidth so it never
    // reaches back into contentBlockWidth → naturalTextWidth → textWrapWidth. That
    // chain, combined with textWrapWidth reading this value, closes a binding loop
    // while hasInlineMedia flips on delegate reuse. Image bubbles are the only
    // consumers (textWrapWidth/textRegionWidth media branches), and there
    // bubbleContentWidth === imageDisplayWidth, so the value is unchanged.
    readonly property real innerContentWidth: Math.max(Kirigami.Units.gridUnit * 2,
                                                       imageDisplayWidth - innerPadding * 2)
    readonly property real textRegionWidth: hasInlineMedia ? innerContentWidth : contentBlockWidth

    // Footer (time + ticks) colours. Over the image-only vignette they switch to
    // light tones for contrast; otherwise the muted theme colours are used.
    readonly property color footerTextColor: imageOnly ? "white" : Kirigami.Theme.disabledTextColor
    readonly property color statusTickColor: statusIsRead
        ? (imageOnly ? Qt.lighter(Kirigami.Theme.highlightColor, 1.4) : Kirigami.Theme.highlightColor)
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
    readonly property real replyGlowLeft: frameless
        ? Math.min(stickerSlot.x,
                   stickerReplyPreviewLoader.active ? stickerReplyPreviewLoader.x : stickerSlot.x,
                   stickerFooter.x)
        : bubble.x
    readonly property real replyGlowTop: frameless
        ? Math.min(stickerSlot.y,
                   stickerReplyPreviewLoader.active ? stickerReplyPreviewLoader.y : stickerSlot.y,
                   stickerFooter.y)
        : bubble.y
    readonly property real replyGlowRight: frameless
        ? Math.max(stickerSlot.x + stickerSlot.width,
                   stickerReplyPreviewLoader.active ? stickerReplyPreviewLoader.x + stickerReplyPreviewLoader.width : stickerSlot.x + stickerSlot.width,
                   stickerFooter.x + stickerFooter.width)
        : bubble.x + bubble.width
    readonly property real replyGlowBottom: frameless
        ? Math.max(stickerSlot.y + stickerSlot.height,
                   stickerReplyPreviewLoader.active ? stickerReplyPreviewLoader.y + stickerReplyPreviewLoader.height : stickerSlot.y + stickerSlot.height,
                   stickerFooter.y + stickerFooter.height)
        : bubble.y + bubble.height

    width: listWidth
    height: (frameless
        ? stickerFooter.y + stickerFooter.height
        : bubble.y + bubble.height) + (groupEnd ? Kirigami.Units.smallSpacing : Kirigami.Units.smallSpacing / 4)

    HoverHandler {
        id: rowHoverHandler

        acceptedDevices: PointerDevice.Mouse | PointerDevice.TouchPad
        onHoveredChanged: {
            if (hovered) {
                root.hoverLatched = true
            }
        }
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
        id: footerMetrics
        text: root.timeText
        font.pointSize: root.footerTimePointSize
    }

    Kirigami.ShadowedRectangle {
        id: bubble

        visible: !root.frameless

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
                ? Qt.alpha(Kirigami.Theme.highlightColor, 0.30)
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
                if (root.hasBody) {
                    bottom = root.tntFitsInline
                        ? bodyTextLoader.y + bodyTextLoader.height
                        : footerSlot.y + footerSlot.height
                } else {
                    bottom = footerSlot.y + footerSlot.height
                }
                return bottom + root.footerInset
            }

            Loader {
                id: replyPreviewLoader

                active: root.hasReplyPreview
                x: root.innerPadding
                y: root.innerPadding
                width: root.textRegionWidth

                sourceComponent: ReplyPreview {
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
            }

            Item {
                id: mediaSlot

                visible: root.isImage && !root.isSticker
                x: 0
                y: root.contentOffsetBeforeMedia()
                width: root.imageDisplayWidth
                height: visible ? root.imageDisplayHeight : 0
                clip: true

                // Lazily instantiate the image stack (shader-effect images,
                // backdrop, loading overlay) only for image messages. Text and
                // sticker delegates skip it entirely — this is the bulk of the
                // per-delegate node cost behind scroll-time instantiation spikes.
                Loader {
                    anchors.fill: parent
                    active: mediaSlot.visible
                    sourceComponent: Component {
                      Item {
                        anchors.fill: parent

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

                // Low-resolution placeholder, drawn with rounded corners in a
                // single shader pass. The tiny decode cap upscales into the
                // blur-up look without a blur shader.
                RoundedImage {
                    id: roundedThumb

                    anchors.fill: parent
                    // Stay up as the blur-up placeholder until the full image has
                    // decoded, so a fast fling (which holds off the full-res
                    // decode) always has the cheap thumbnail to show.
                    visible: root.hasThumbnailImage && (!root.hasLocalImage || img.status !== Image.Ready)
                    opacity: thumb.status === Image.Ready ? 0.78 : 0
                    source: thumb
                    topLeftRadius: root.mediaTopLeftRadius
                    topRightRadius: root.mediaTopRightRadius
                    bottomRightRadius: root.mediaBottomRightRadius
                    bottomLeftRadius: root.mediaBottomLeftRadius

                    Image {
                        id: thumb

                        anchors.fill: parent
                        visible: false
                        source: root.mediaSourceActive && mediaSlot.visible && roundedThumb.visible ? Qt.resolvedUrl("file://" + root.mediaThumbnailLocalPath) : ""
                        asynchronous: true
                        cache: true
                        sourceSize.width: root.thumbnailDecodeWidth
                        sourceSize.height: root.thumbnailDecodeHeight
                    }

                    Behavior on opacity {
                        NumberAnimation {
                            duration: Kirigami.Units.shortDuration
                            easing.type: Easing.OutCubic
                        }
                    }
                }

                // Full-resolution image. Sampled straight from the (hidden) Image
                // texture provider, so there is no layer/mask/FBO to allocate or
                // tear down as the delegate scrolls through the viewport.
                RoundedImage {
                    id: roundedImg

                    anchors.fill: parent
                    visible: root.hasLocalImage
                    opacity: img.status === Image.Ready ? 1 : 0
                    source: img
                    topLeftRadius: root.mediaTopLeftRadius
                    topRightRadius: root.mediaTopRightRadius
                    bottomRightRadius: root.mediaBottomRightRadius
                    bottomLeftRadius: root.mediaBottomLeftRadius

                    Image {
                        id: img

                        anchors.fill: parent
                        visible: false
                        // Hold the full-res decode while flinging (unless it is
                        // already decoded), letting the thumbnail carry the scroll.
                        source: root.mediaSourceActive && mediaSlot.visible && root.hasLocalImage
                                && (!root.fastFlicking || status === Image.Ready)
                                ? Qt.resolvedUrl("file://" + root.mediaLocalPath) : ""
                        asynchronous: true
                        cache: true
                        sourceSize.width: root.imageDecodeWidth
                        sourceSize.height: root.imageDecodeHeight
                    }

                    Behavior on opacity {
                        NumberAnimation {
                            duration: Kirigami.Units.shortDuration
                            easing.type: Easing.OutCubic
                        }
                    }
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
                    }
                }
            }

            // Text-only and media-only delegates split the cost: the TextEdit
            // (and its document) is only built when there is body text.
            Loader {
                id: bodyTextLoader

                active: root.hasBody
                x: root.innerPadding
                y: root.contentOffsetBeforeBody()
                width: root.textRegionWidth

                sourceComponent: TextEdit {
                    id: bodyText

                    property string currentHoveredLink: ""

                    // Formatted WhatsApp markdown and inline-enlarged emoji use RichText;
                    // plain messages stay on the cheaper PlainText path. Apply format and
                    // text together, clearing the old document first, so pooled delegates
                    // cannot carry rich-document font state into normal messages.
                    function syncContent() {
                        text = ""
                        if (root.hasRichText) {
                            textFormat = TextEdit.RichText
                            text = root.richText
                        } else {
                            textFormat = TextEdit.PlainText
                            text = root.body
                        }
                    }

                    readOnly: true
                    selectByMouse: true
                    selectByKeyboard: true
                    persistentSelection: true
                    wrapMode: TextEdit.Wrap
                    color: Kirigami.Theme.textColor
                    font.family: Kirigami.Theme.defaultFont.family
                    font.pointSize: root.bodyPointSize
                    font.weight: Font.Normal
                    onLinkActivated: link => Qt.openUrlExternally(link)
                    onLinkHovered: link => currentHoveredLink = link

                    HoverHandler {
                        cursorShape: bodyText.currentHoveredLink.length > 0 ? Qt.PointingHandCursor : Qt.IBeamCursor
                    }

                    Component.onCompleted: syncContent()

                    Connections {
                        target: root
                        function onHasRichTextChanged() { bodyText.syncContent() }
                        function onRichTextChanged() { bodyText.syncContent() }
                        function onBodyChanged() { bodyText.syncContent() }
                    }

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
            }

            Loader {
                id: readMoreLoader

                active: root.showReadMore
                x: root.innerPadding
                y: root.hasBody ? bodyTextLoader.y + bodyTextLoader.height + Kirigami.Units.smallSpacing / 2 : root.contentOffsetBeforeBody()

                sourceComponent: AbstractButton {
                    id: readMoreButton

                    width: Math.ceil(root.readMoreTextWidth + Kirigami.Units.smallSpacing * 2)
                    height: readMoreLabel.implicitHeight + Kirigami.Units.smallSpacing
                    text: root.readMoreLabelText
                    hoverEnabled: true
                    focusPolicy: Qt.NoFocus
                    onClicked: {
                        root.readMoreRequested(root.messageId)
                        root.conversationFocusRequested()
                    }

                    contentItem: Label {
                        id: readMoreLabel

                        text: readMoreButton.text
                        color: readMoreButton.hovered || readMoreButton.pressed ? Kirigami.Theme.highlightColor : Kirigami.Theme.linkColor
                        font.pointSize: Kirigami.Theme.smallFont.pointSize
                        font.weight: Font.DemiBold
                        verticalAlignment: Text.AlignVCenter
                        horizontalAlignment: Text.AlignLeft
                    }

                    background: Rectangle {
                        color: readMoreButton.hovered || readMoreButton.pressed ? Qt.alpha(Kirigami.Theme.highlightColor, 0.08) : "transparent"
                        radius: Kirigami.Units.cornerRadius
                    }
                }
            }

            Item {
                id: footerSlot

                // Image-only: overlay on the media bottom-right (over the
                // vignette). Otherwise sit at the right inner edge, inline with
                // the last text line or on its own row.
                x: root.imageOnly
                   ? mediaSlot.x + mediaSlot.width - width - root.footerInset
                   : Math.max(0, parent.width - root.footerInset - width)
                y: {
                    if (root.imageOnly) {
                        return mediaSlot.y + mediaSlot.height - height - root.footerInset
                    }
                    const off = root.contentOffsetBeforeFooter()
                    if (root.hasBody) {
                        return root.tntFitsInline
                            ? bodyTextLoader.y + root.bodyEndCursorRect.y + root.bodyEndCursorRect.height - height + root.inlineTntYOffset
                            : off + root.tntGap
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
                        x: Math.round((parent.width - width) / 2)
                        anchors.verticalCenter: parent.verticalCenter
                        visible: !root.statusIsDoubleTick
                        source: root.statusSingleIcon
                        width: root.statusIconSize
                        height: root.statusIconSize
                        color: root.statusSingleColor
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
                            source: root.tickSource
                            width: root.statusIconSize
                            height: root.statusIconSize
                            color: root.statusTickColor
                            isMask: true
                        }
                        Kirigami.Icon {
                            id: secondStatusTick

                            x: root.statusDoubleTickOffset
                            anchors.verticalCenter: parent.verticalCenter
                            source: root.tickSource
                            width: root.statusIconSize
                            height: root.statusIconSize
                            color: root.statusTickColor
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
                    width: Math.ceil(footerMetrics.advanceWidth)
                    horizontalAlignment: Text.AlignRight
                    font.pointSize: root.footerTimePointSize
                }
            }
        }
    }

    Loader {
        id: stickerReplyPreviewLoader

        active: root.frameless && root.hasReplyPreview
        x: root.outgoing
           ? root.width - width - root.outerMargin
           : root.outerMargin + root.senderGutterWidth
        y: root.messageBaseY
        width: Math.min(Math.max(0, root.width - root.outerMargin * 2 - root.senderGutterWidth),
                        Math.max(Kirigami.Units.gridUnit * 5, item ? item.naturalContentWidth : 0))

        sourceComponent: ReplyPreview {
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
    }

    Item {
        id: stickerSlot

        visible: root.frameless
        x: root.outgoing
           ? root.width - width - root.outerMargin
           : root.outerMargin + root.senderGutterWidth
        y: root.messageBaseY + (stickerReplyPreviewLoader.active ? stickerReplyPreviewLoader.height + Kirigami.Units.smallSpacing : 0)
        width: root.isJumboEmoji ? jumboEmoji.implicitWidth : root.stickerDisplayWidth
        height: root.isJumboEmoji ? jumboEmoji.implicitHeight : root.stickerDisplayHeight

        Text {
            id: jumboEmoji

            anchors.centerIn: parent
            visible: root.isJumboEmoji
            text: root.body
            font.family: Whatevr.AppController.emojiModel.emojiFontFamily
            font.pixelSize: root.jumboEmojiPixelSize
            horizontalAlignment: Text.AlignHCenter
            textFormat: Text.PlainText
        }

        // The sticker renderers — an AnimatedImage and a custom Lottie paint
        // item are the heaviest — are built only for sticker messages. Text,
        // image and jumbo-emoji delegates never instantiate them. jumboEmoji
        // stays outside so the slot can size to it.
        Loader {
            anchors.fill: parent
            active: root.isSticker
            sourceComponent: Component {
              Item {
                anchors.fill: parent

                // Whether the actual sticker (whichever renderer applies) is on
                // screen. Drives the thumbnail placeholder during a fast fling.
                readonly property bool stickerContentReady: root.isLottieSticker
                    ? lottieSticker.status === Whatevr.RlottieSticker.Ready
                    : root.isAnimatedSticker
                        ? animatedSticker.status === AnimatedImage.Ready
                        : staticSticker.status === Image.Ready

        Image {
            id: stickerThumb

            anchors.fill: parent
            // Hold the thumbnail until the real sticker is showing, so a fling
            // (which defers the full sticker decode) still has a placeholder.
            visible: root.isSticker && root.hasThumbnailImage && !parent.stickerContentReady
            opacity: status === Image.Ready ? 0.7 : 0
            source: root.mediaSourceActive && stickerSlot.visible && visible ? Qt.resolvedUrl("file://" + root.mediaThumbnailLocalPath) : ""
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            cache: true
            smooth: true
            sourceSize.width: root.stickerDecodeCap
            sourceSize.height: root.stickerDecodeCap
        }

        Image {
            id: staticSticker

            anchors.fill: parent
            visible: root.isRenderableStickerImage && root.hasLocalSticker && !root.isAnimatedSticker
            opacity: status === Image.Ready ? 1 : 0
            // Defer the decode while flinging unless it is already decoded.
            source: root.mediaSourceActive && stickerSlot.visible && visible
                    && (!root.fastFlicking || status === Image.Ready)
                    ? Qt.resolvedUrl("file://" + root.mediaLocalPath) : ""
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            cache: true
            smooth: true
            sourceSize.width: root.stickerDecodeCap
            sourceSize.height: root.stickerDecodeCap
        }

        AnimatedImage {
            id: animatedSticker

            anchors.fill: parent
            visible: root.isRenderableStickerImage && root.hasLocalSticker && root.isAnimatedSticker
            playing: root.animationActive && visible && status === AnimatedImage.Ready
            // Defer the (heavy, multi-frame) decode while flinging unless ready.
            source: root.mediaSourceActive && stickerSlot.visible && visible
                    && (!root.fastFlicking || status === AnimatedImage.Ready)
                    ? Qt.resolvedUrl("file://" + root.mediaLocalPath) : ""
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
            // Rasterise at device resolution (capped) rather than a fixed 2.5×;
            // the sticker is ~144 px so 2× is already crisp and halves the
            // per-frame raster cost on 1× displays.
            renderScale: Math.max(1, Math.min(2, Screen.devicePixelRatio))
        }

        Item {
            id: stickerOverlay

            anchors.fill: parent
            visible: root.isSticker
                     && (!root.hasLocalSticker
                     || root.mediaDownloading
                     || stickerThumb.status === Image.Loading
                     || staticSticker.status === Image.Loading
                     || animatedSticker.status === AnimatedImage.Loading
                     || lottieSticker.status === Whatevr.RlottieSticker.Loading
                     || staticSticker.status === Image.Error
                     || animatedSticker.status === AnimatedImage.Error
                     || lottieSticker.status === Whatevr.RlottieSticker.Error
                     || root.mediaDownloadError.length > 0)

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
            }
        }
    }

    Rectangle {
        id: stickerFooter

        visible: root.frameless
        x: Math.round(Math.max(root.outerMargin + root.senderGutterWidth,
                               Math.min(root.width - root.outerMargin - width,
                                        stickerSlot.x + stickerSlot.width - width)))
        y: Math.round(stickerSlot.y + stickerSlot.height + Kirigami.Units.smallSpacing / 2)
        width: root.tntWidth + root.framelessFooterHPadding * 2
        height: root.tntHeight + root.framelessFooterVPadding * 2
        radius: Kirigami.Units.cornerRadius
        color: root.outgoing
               ? Qt.alpha(Kirigami.Theme.highlightColor, 0.30)
               : Qt.alpha(Kirigami.Theme.backgroundColor, 0.76)
        border.color: Qt.alpha(Kirigami.Theme.textColor, root.outgoing ? 0.05 : 0.12)

        // The footer content (status ticks + time label) is only built for
        // frameless rows; the Rectangle itself stays so geometry consumers
        // (root.height, reply glow) keep working before/without the content.
        Loader {
            active: root.frameless
            x: root.framelessFooterHPadding
            y: Math.round((parent.height - height) / 2)
            width: root.tntWidth
            height: root.tntHeight

            sourceComponent: Item {
            id: stickerFooterContent

            anchors.fill: parent

            Item {
                id: stickerStatusArea

                anchors.right: parent.right
                anchors.verticalCenter: parent.verticalCenter
                visible: root.showStatusIcon
                width: root.statusAreaWidth
                height: root.statusIconSize

                Kirigami.Icon {
                    id: stickerSingleIcon
                    x: Math.round((parent.width - width) / 2)
                    anchors.verticalCenter: parent.verticalCenter
                    visible: !root.statusIsDoubleTick
                    source: root.statusSingleIcon
                    width: root.statusIconSize
                    height: root.statusIconSize
                    color: root.statusSingleColor
                    isMask: true
                }

                Item {
                    anchors.fill: parent
                    visible: root.statusIsDoubleTick

                    Kirigami.Icon {
                        id: firstStickerTick
                        x: 0
                        anchors.verticalCenter: parent.verticalCenter
                        source: root.tickSource
                        width: root.statusIconSize
                        height: root.statusIconSize
                        color: root.statusTickColor
                        isMask: true
                    }

                    Kirigami.Icon {
                        id: secondStickerTick
                        x: root.statusDoubleTickOffset
                        anchors.verticalCenter: parent.verticalCenter
                        source: root.tickSource
                        width: root.statusIconSize
                        height: root.statusIconSize
                        color: root.statusTickColor
                        isMask: true
                    }
                }
            }

            Label {
                anchors.right: stickerStatusArea.visible ? stickerStatusArea.left : parent.right
                anchors.rightMargin: stickerStatusArea.visible ? root.tntSpacing : 0
                anchors.verticalCenter: parent.verticalCenter
                text: root.timeText
                color: Kirigami.Theme.disabledTextColor
                width: Math.ceil(footerMetrics.advanceWidth)
                horizontalAlignment: Text.AlignRight
                font.pointSize: root.footerTimePointSize
            }
            }
        }
    }

    // Instantiated only while the jump-to-reply glow animation is running.
    Loader {
        active: root.replyGlowOpacity > 0
        x: Math.round(root.replyGlowLeft - root.replyGlowPadding)
        y: Math.round(root.replyGlowTop - root.replyGlowPadding)
        z: 7
        width: Math.max(0, Math.round(root.replyGlowRight - root.replyGlowLeft + root.replyGlowPadding * 2))
        height: Math.max(0, Math.round(root.replyGlowBottom - root.replyGlowTop + root.replyGlowPadding * 2))

        sourceComponent: Item {
            id: replyGlowOverlay

            readonly property real innerMargin: Math.max(1, Math.round(Kirigami.Units.smallSpacing / 2))

            anchors.fill: parent
            opacity: root.replyGlowOpacity

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
    }

    // Built lazily on first hover of the row (hoverLatched): scrolling never
    // pays for the button, only the rows the pointer actually visits do.
    Loader {
        anchors.fill: parent
        active: root.hoverLatched && root.messageId.length > 0 && !root.pooled
        z: 8

        sourceComponent: Item {
            ToolButton {
                id: replyButton

                readonly property real visualX: root.frameless ? stickerSlot.x : bubble.x
                readonly property real visualY: root.frameless ? stickerSlot.y : bubble.y
                readonly property real visualWidth: root.frameless ? stickerSlot.width : bubble.width
                readonly property real visualHeight: root.frameless ? stickerSlot.height : bubble.height
                readonly property real desiredX: root.outgoing
                                                 ? visualX - width - Kirigami.Units.smallSpacing
                                                 : visualX + visualWidth + Kirigami.Units.smallSpacing

                enabled: opacity > 0.01
                opacity: (rowHoverHandler.hovered || hovered || pressed) ? 1 : 0
                x: Math.round(Math.max(root.outerMargin,
                                       Math.min(root.width - root.outerMargin - width, desiredX)))
                y: Math.round(visualY + Math.max(0, visualHeight - height) / 2)
                width: Math.round(Math.max(Kirigami.Units.iconSizes.smallMedium + Kirigami.Units.smallSpacing,
                                           Math.min(Kirigami.Units.gridUnit * 1.45,
                                                    visualHeight - Kirigami.Units.smallSpacing)))
                height: width
                icon.name: "edit-undo"
                // Set both dimensions to the constant directly; binding icon.height to
                // icon.width loops through the control's implicit-size machinery.
                icon.width: Kirigami.Units.iconSizes.smallMedium
                icon.height: Kirigami.Units.iconSizes.smallMedium
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
        }
    }

    // Sender header (group chats, first message of a sender group) is loaded
    // only when shown; senderHeaderHeight reads labelImplicitHeight from the
    // synchronously loaded item, so row height settles in the same frame.
    Loader {
        id: senderHeaderLoader

        anchors.fill: parent
        active: root.showSenderHeader

        sourceComponent: Item {
            readonly property real labelImplicitHeight: senderHeader.implicitHeight

            anchors.fill: parent

            Label {
                id: senderHeader

                visible: root.senderName.length > 0
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
                x: root.outerMargin + Math.max(0, root.senderGutterWidth - width) / 2
                y: root.dateSeparatorHeight + Math.max(0, (root.senderHeaderHeight - height) / 2)
                width: root.senderAvatarSize
                height: root.senderAvatarSize
                avatarLocalPath: root.senderAvatarLocalPath
                initials: root.senderInitials
                backgroundColor: Qt.alpha(foregroundColor, 0.12)
            }
        }
    }

    // Day-separator pill, loaded only for rows that start a day. The loader
    // auto-sizes to the pill, so dateSeparatorHeight is valid right after the
    // synchronous load.
    Loader {
        id: dateSeparatorLoader

        active: root.showDateSeparator
        x: Math.round((root.width - width) / 2)
        y: Kirigami.Units.largeSpacing / 2

        sourceComponent: DateSeparatorPill {
            text: root.dateSeparatorText
        }
    }

}
