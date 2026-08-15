pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Item {
    id: root

    Kirigami.Theme.inherit: false
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    // Always-active highlight comes from the app-wide Whatevr.Palette singleton
    // (see qml/Palette.qml): the value is identical for every row, so a
    // per-delegate SystemPalette was one wasted QObject per message (DN9).

    // ---- Model roles ----
    //
    // These are `required` and named exactly after ProtocolMessageModel's role
    // names, so ListView assigns them straight from the model in C++. They used
    // to be plain properties fed by ~60 `String(model.x || "")` bindings in
    // MessageView's delegate block: one JS evaluation and one type coercion per
    // role per row, and the single biggest reason qmlcachegen could not compile
    // that block (DN9).
    required property string messageId
    required property string timeText
    required property string dateSeparatorText
    required property int status
    required property bool isOutgoing
    required property string senderName
    required property string senderAvatarLocalPath
    required property string senderInitials
    required property bool showSenderHeader
    required property bool showSenderAvatar
    required property bool showSenderGutter
    required property bool groupStart
    required property bool groupEnd
    required property string mediaKind
    required property string mediaMimeType
    required property string mediaLocalPath
    required property string mediaThumbnailLocalPath
    required property string mediaCacheKey
    required property int mediaWidth
    required property int mediaHeight
    required property bool mediaAnimated
    required property double mediaSizeBytes
    required property int mediaDurationSecs
    required property string mediaFileName
    required property int mediaPageCount
    required property var mediaWaveform
    required property bool mediaPlayed
    required property bool isRevoked
    required property bool isEdited
    required property bool isStarred
    required property bool isPinned
    required property bool mediaDownloading
    required property string mediaDownloadError
    // Download progress 0..1 while bytes are streaming; -1 when the total size
    // is unknown (falls back to the indeterminate spinner).
    required property real mediaDownloadProgress
    required property string replyToMessageId
    required property string replyToSenderName
    required property string replyToText
    required property string replyToMediaKind
    required property string replyToMediaMimeType
    required property bool replyToIsOutgoing
    // Unwrapped widest/last line widths of the displayed body, measured in C++
    // (model roles). Replaces per-delegate TextMetrics + JS line splitting.
    required property real widestLineWidth
    required property real lastLineWidth
    // Reactions on this message: list of {emoji, senderId, senderName, fromMe}
    // maps (ProtocolMessageModel ReactionsRole).
    required property var reactions

    // Raw text roles. A long message is delivered twice — the full text and a
    // truncated preview — and which one is shown depends on `textExpanded`,
    // which is view state rather than model data. The choice used to be made in
    // MessageView; it is made here now so the roles can arrive untouched.
    required property string text
    required property string textPreview
    required property string layoutText
    required property string layoutTextPreview
    required property bool hasRichText
    required property bool previewHasRichText
    required property string richText
    required property string previewRichText
    required property int emojiOnlyCount
    required property bool textTruncated

    // ---- Derived display text ----
    readonly property string body: textExpanded
        ? text
        : (textPreview.length > 0 ? textPreview : text)
    readonly property string layoutBody: textExpanded
        ? layoutText
        : (layoutTextPreview.length > 0 ? layoutTextPreview : layoutText)
    readonly property string replyPreviewBody: textPreview.length > 0 ? textPreview : text
    readonly property bool displayHasRichText: textExpanded ? hasRichText : previewHasRichText
    readonly property string displayRichText: textExpanded ? richText : previewRichText
    // A truncated preview never renders as jumbo emoji: the count describes the
    // whole message, not the fragment being shown.
    readonly property int displayEmojiOnlyCount: textExpanded || !textTruncated ? emojiOnlyCount : 0

    // ---- View state (not model roles) ----
    property bool textExpanded: false
    // Multi-message selection (top-bar actions). While the mode is active a
    // covering handler turns every click into a toggle and swallows the
    // bubble's normal interactions (links, text selection, reply).
    property bool selectionModeActive: false
    property bool selected: false
    property bool pooled: false
    property bool activeInViewport: true
    // True while the list is being flung fast. Full-resolution media decoding is
    // held off during a fling so the cheap, already-cached thumbnail carries the
    // scroll; it upgrades to full-res the instant scrolling settles.
    property bool fastFlicking: false
    // Number shown in the "N unread messages" divider above this message; 0
    // hides the divider. Set only on the unread-anchor row.
    property int unreadSeparatorCount: 0
    property int clearSelectionGeneration: 0
    property string activeSelectionMessageId: ""
    // Advance width of the "Read more" label, measured once in MessageView and
    // shared by all delegates.
    property real readMoreTextWidth: 0
    // Latches true on first hover so the reply button is only ever instantiated
    // for rows the pointer actually visits; reset when the delegate is reused.
    // Hovers reported while the list flings past the idle pointer don't count
    // (see rowHoverHandler) — those rows were never really visited.
    property bool hoverLatched: false
    // Same latch for the text-selection surface: plain-text bodies render with
    // a cheap Text element; the TextEdit (QTextDocument) that provides mouse
    // selection is only built once the pointer genuinely visits the row.
    property bool selectionLatched: false

    signal conversationFocusRequested()
    // Asks the view to run its shared reply-glow animation against this row.
    signal replyGlowRequested()
    signal messageSelectionClaimed(string messageId)
    signal typeIntoComposerRequested(string text)
    signal replyRequested(string messageId, string senderName, string text, string mediaKind, string mediaMimeType, bool outgoing)
    // Open the quick-reaction bar / picker for this message. Position is in this
    // delegate's coordinates; the view maps it.
    signal reactionPickerRequested(real posX, real posY)
    // Toggle the viewer's own reaction with this emoji (chip click).
    signal reactionToggleRequested(string emoji)
    // Open the reactor list dialog for this message's reactions.
    signal reactionDetailsRequested()
    signal replyPreviewActivated(string messageId)
    signal readMoreRequested(string messageId)
    // A downloaded message photo was clicked: open it full screen.
    signal imageActivated(string localPath)
    // A video, GIF or video note asked to open full screen. The path and
    // duration ride along so the viewer needs no lookup, and startAt carries
    // the second the inline copy had reached, so opening full screen continues
    // a clip instead of restarting it.
    signal videoActivated(string messageId, string localPath, string streamUrl, string streamId, string kind, int durationSecs, real startAt)
    // An @-mention link was clicked: open contact info for the JID, or the
    // group info dialog for an @all / @everyone mention.
    signal mentionClicked(string jid)
    signal mentionAllClicked()
    // Position is in this delegate's coordinates; the view maps it.
    signal contextMenuRequested(real posX, real posY)
    signal selectionToggleRequested()
    // Clicking this row's date-separator pill while in selection mode toggles
    // the whole day's selection.
    signal daySelectionToggleRequested()

    onClearSelectionGenerationChanged: {
        if (activeSelectionMessageId.length !== 0 && activeSelectionMessageId === messageId) {
            return
        }
        // Rich bodies select on the body TextEdit itself; plain bodies select
        // on the on-demand overlay (absent until the row was hovered).
        if (selectionEditLoader.item) {
            selectionEditLoader.item.deselect()
        } else if (bodyTextLoader.item && root.displayHasRichText) {
            bodyTextLoader.item.deselect()
        }
    }

    property real listWidth: 0
    readonly property bool showDateSeparator: dateSeparatorText.length > 0
    readonly property bool hasReplyPreview: replyToMessageId.length > 0
    readonly property bool canReply: messageId.length > 0
                                     && !isRevoked
                                     && (body.length > 0
                                         || mediaKind.length > 0
                                         || mediaMimeType.length > 0
                                         || mediaLocalPath.length > 0
                                         || mediaThumbnailLocalPath.length > 0
                                         || mediaCacheKey.length > 0)
    readonly property real dateSeparatorHeight: showDateSeparator
        ? dateSeparatorLoader.height + Kirigami.Units.largeSpacing
        : 0
    readonly property bool showUnreadSeparator: unreadSeparatorCount > 0
    readonly property real unreadSeparatorHeight: showUnreadSeparator
        ? unreadSeparatorLoader.height + Kirigami.Units.largeSpacing
        : 0
    readonly property real outerMargin: Kirigami.Units.largeSpacing
    // Bubble padding follows the appearance density setting (live).
    readonly property real densityScale: Whatevr.Settings.density === 0 ? 0.7
        : (Whatevr.Settings.density === 2 ? 1.3 : 1.0)
    readonly property real innerPadding: Math.round(Kirigami.Units.largeSpacing * densityScale)
    readonly property real senderAvatarSize: Kirigami.Units.gridUnit * 1.65
    readonly property real senderGutterWidth: showSenderGutter ? senderAvatarSize + Kirigami.Units.smallSpacing : 0
    readonly property real senderHeaderHeight: showSenderHeader
        ? Math.max(senderAvatarSize, senderHeaderLoader.item ? senderHeaderLoader.item.labelImplicitHeight : 0)
        : 0
    readonly property real maxBubbleWidth: Math.max(Kirigami.Units.gridUnit * 4,
                                                    Math.min(Math.max(0, listWidth - outerMargin * 2 - senderGutterWidth),
                                                              Kirigami.Units.gridUnit * 28))
    readonly property real maxContentWidth: Math.max(Kirigami.Units.gridUnit * 4, maxBubbleWidth - innerPadding * 2)
    // Honour the appearance setting (point size; 0 = follow the system font).
    readonly property real bodyPointSize: Whatevr.Settings.messageFontSize > 0
        ? Whatevr.Settings.messageFontSize
        : (Kirigami.Theme.defaultFont.pointSize > 0
            ? Kirigami.Theme.defaultFont.pointSize
            : 10)
    readonly property real messageBaseY: dateSeparatorHeight + unreadSeparatorHeight + (senderHeaderHeight > 0 ? senderHeaderHeight + Kirigami.Units.smallSpacing / 2 : 0)
    readonly property real replyGlowPadding: Kirigami.Units.smallSpacing
    property real replyGlowOpacity: 0

    readonly property bool isSticker: mediaKind === "sticker"
                                      || (mediaKind.length === 0
                                          && mediaMimeType === "image/webp"
                                          && (mediaLocalPath.endsWith(".webp")
                                              || mediaThumbnailLocalPath.endsWith(".thumb.png")))
    readonly property bool isImage: mediaMimeType.startsWith("image/")
    // Kinds that render like a photo: they span the bubble edge to edge and
    // drive its width.
    readonly property bool isVideo: mediaKind === "video"
    readonly property bool isGif: mediaKind === "gif"
    readonly property bool isVideoNote: mediaKind === "video_note"
    readonly property bool isPlayableVideo: isVideo || isGif || isVideoNote
    // Kinds that render as a fixed-height row inside the padded content, more
    // like a line of text than a picture.
    readonly property bool isVoice: mediaKind === "voice"
    readonly property bool isAudioFile: mediaKind === "audio"
    readonly property bool isDocument: mediaKind === "document"
    readonly property bool isAttachmentBlock: isVoice || isAudioFile || isDocument
    // Real message whose payload the app can't render yet (document, voice
    // note, poll, ...). The daemon puts a short label in the body text; the
    // row renders like a revoked tombstone and never offers a download.
    readonly property bool isUnsupported: mediaKind === "unsupported"
    // 1-3 emoji-only messages render large and frameless, like stickers. The single
    // emoji case is biggest; size steps down as the count rises.
    readonly property bool isJumboEmoji: displayEmojiOnlyCount > 0 && displayEmojiOnlyCount <= 3
    // Rows that draw no plate: a bare slot over the wallpaper with a floating
    // time/ticks pill under it. A video note is one of these, the same way
    // WhatsApp draws a round instant video: a circle on the wallpaper, no box.
    readonly property bool frameless: isSticker || isJumboEmoji || isVideoNote
    readonly property real jumboEmojiPixelSize: Kirigami.Units.gridUnit
        * (displayEmojiOnlyCount === 1 ? 2.8 : displayEmojiOnlyCount === 2 ? 2.2 : 1.8)
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

    // ---- Visibility-based media auto-download ----
    // Media is fetched lazily, when a message scrolls into view, rather than
    // eagerly on the daemon when it arrives. Whether it auto-fetches is gated by
    // the per-kind "auto-download" preferences; with the toggle off the user
    // downloads manually via the in-bubble button.
    readonly property bool mediaIsLocal: isSticker ? hasLocalSticker : mediaLocalPath.length > 0
    readonly property bool hasDownloadableMedia: !mediaIsLocal && !isUnsupported
        && (mediaMimeType.length > 0 || mediaCacheKey.length > 0 || mediaKind.length > 0)
    // The user's ceiling, defaulting to 16 MiB: big enough for a voice note, a
    // photo or a short clip, small enough that a scroll past a long video does
    // not commit the connection. 0 means no limit.
    readonly property real autoDownloadSizeCeiling: {
        const configured = Whatevr.ProtocolController.appPreferences.auto_download_max_bytes
        return configured === undefined ? 16 * 1024 * 1024 : configured
    }
    readonly property bool autoDownloadWanted: {
        if (!hasDownloadableMedia)
            return false;
        const prefs = Whatevr.ProtocolController.appPreferences;
        // Above this, nothing fetches itself: a 200 MB video is the user's
        // decision, not a scroll's.
        if (autoDownloadSizeCeiling > 0 && mediaSizeBytes > autoDownloadSizeCeiling)
            return false;
        if (isSticker)
            return prefs.auto_download_stickers ?? false;
        if (isImage)
            return prefs.auto_download_photos ?? false;
        if (isPlayableVideo)
            return prefs.auto_download_videos ?? false;
        if (isVoice || isAudioFile)
            return prefs.auto_download_audio ?? false;
        if (isDocument)
            return prefs.auto_download_documents ?? false;
        if (mediaMimeType.startsWith("video/"))
            return prefs.auto_download_videos ?? false;
        if (mediaMimeType.startsWith("audio/"))
            return prefs.auto_download_audio ?? false;
        return prefs.auto_download_documents ?? false;
    }
    // Latches once a download is kicked off for the current message so viewport
    // churn doesn't re-fire it. Reset when the delegate is reused (see the
    // onMessageIdChanged handler further down).
    property bool autoDownloadTriggered: false

    function maybeAutoDownloadMedia() {
        if (autoDownloadTriggered || messageId.length === 0)
            return;
        // Skip while flinging so a fast scroll doesn't spray download requests;
        // it retries when the fling settles (onFastFlickingChanged).
        if (!activeInViewport || fastFlicking)
            return;
        if (!autoDownloadWanted || mediaDownloading || mediaDownloadError.length > 0)
            return;
        autoDownloadTriggered = true;
        Whatevr.ProtocolController.downloadMessageMedia(messageId);
    }

    onActiveInViewportChanged: maybeAutoDownloadMedia()
    onAutoDownloadWantedChanged: maybeAutoDownloadMedia()

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
        reservedImageAspectRatio = normalisedImageAspectRatio(mediaWidth, mediaHeight)
        reservedImageNaturalWidth = mediaWidth > 0 ? mediaWidth : fallbackImageWidth
    }

    readonly property real videoNoteDiameter: Math.min(maxBubbleWidth, Kirigami.Units.gridUnit * 11)
    // Voice notes, audio files and documents are rows, not pictures: a fixed
    // height and a comfortable width that does not depend on decode.
    readonly property real attachmentBlockWidth: Math.min(maxContentWidth, Kirigami.Units.gridUnit * 17)
    // Two lines: the waveform (or the filename) and the line under it that now
    // carries the timestamp too, so the block no longer reserves a third.
    readonly property real attachmentBlockHeight: isDocument
        ? Kirigami.Units.gridUnit * 2.9
        : Kirigami.Units.gridUnit * 2.6

    readonly property int imageDecodeWidth: decodeWidthForAspect(imageDecodeWidthCap, imageDecodeHeightCap, reservedImageAspectRatio)
    readonly property int imageDecodeHeight: decodeHeightForAspect(imageDecodeWidthCap, imageDecodeHeightCap, reservedImageAspectRatio)
    readonly property int thumbnailDecodeWidth: decodeWidthForAspect(thumbnailDecodeCap, thumbnailDecodeCap, reservedImageAspectRatio)
    readonly property int thumbnailDecodeHeight: decodeHeightForAspect(thumbnailDecodeCap, thumbnailDecodeCap, reservedImageAspectRatio)

    onMessageIdChanged: {
        resetReservedImageGeometry()
        hoverLatched = false
        selectionLatched = false
        autoDownloadTriggered = false
        // The glow lives on a shared animation now, so a row recycled mid-glow
        // would otherwise keep the leftover opacity of the row it replaced.
        replyGlowOpacity = 0
        maybeAutoDownloadMedia()
    }
    onMediaMimeTypeChanged: resetReservedImageGeometry()
    onMediaKindChanged: resetReservedImageGeometry()
    onMediaWidthChanged: resetReservedImageGeometry()
    onMediaHeightChanged: resetReservedImageGeometry()
    Component.onCompleted: {
        resetReservedImageGeometry()
        maybeAutoDownloadMedia()
    }

    readonly property real imageDisplayWidth: {
        if (!hasInlineMedia) {
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
        if (!hasInlineMedia) {
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
    readonly property bool showStatusIcon: isOutgoing && (statusIsDoubleTick || statusSingleIcon.length > 0)

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
    // A small pencil shown left of the timestamp when the message was edited.
    readonly property bool showEditMark: isEdited && !isRevoked
    readonly property real editMarkSize: Math.max(1, Math.round(footerMetrics.height * 0.92))
    readonly property real editMarkReserve: showEditMark ? editMarkSize + tntSpacing : 0
    // A small star shown left of the edit mark / timestamp when the message is
    // starred (mirrors the edit-mark reserve so the footer width stays correct).
    readonly property bool showStarMark: isStarred && !isRevoked
    readonly property real starMarkSize: Math.max(1, Math.round(footerMetrics.height * 0.92))
    readonly property real starMarkReserve: showStarMark ? starMarkSize + tntSpacing : 0
    // A small pin shown left of the star mark when the message is pinned.
    readonly property bool showPinMark: isPinned && !isRevoked
    readonly property real pinMarkSize: Math.max(1, Math.round(footerMetrics.height * 0.92))
    readonly property real pinMarkReserve: showPinMark ? pinMarkSize + tntSpacing : 0
    readonly property real tntWidth: Math.ceil(footerMetrics.advanceWidth
                                               + editMarkReserve
                                               + starMarkReserve
                                               + pinMarkReserve
                                               + (showStatusIcon ? statusAreaWidth + tntSpacing : 0))
    readonly property real tntHeight: Math.ceil(Math.max(footerMetrics.height, showStatusIcon ? statusIconSize : 0,
                                                         showEditMark ? editMarkSize : 0,
                                                         showStarMark ? starMarkSize : 0,
                                                         showPinMark ? pinMarkSize : 0))
    readonly property bool hasBody: body.length > 0
    readonly property bool showReadMore: textTruncated && !textExpanded && hasBody
    readonly property string readMoreLabelText: Whatevr.I18n.i18nc("@action:button expand long message", "Read more")
    // Kinds that fill the bubble edge to edge and drive its width. Video notes
    // are not among them: they are a frameless slot of their own (see
    // FramelessBubble), sized by videoNoteDiameter rather than by media
    // metadata.
    readonly property bool hasInlineMedia: (isImage || isVideo || isGif) && !isSticker
    // Media flush to the bubble edges with the footer overlaid on it.
    readonly property bool imageOnly: hasInlineMedia && !hasBody
    // Captions inside an image bubble wrap to the (padded) image width; plain
    // text bubbles wrap to the full available content width.
    readonly property real textWrapWidth: hasInlineMedia ? innerContentWidth : maxContentWidth
    readonly property real naturalTextWidth: Math.min(textWrapWidth, widestLineWidth)
    readonly property real naturalLastLineWidth: Math.min(textWrapWidth, lastLineWidth)
    readonly property bool canReserveInlineTntWidth: hasBody
                                                   && naturalLastLineWidth + inlineTntGap + tntWidth <= textWrapWidth
    // Where the time and ticks land. A row that already draws a line with room
    // to spare takes them on that line; only a row with nothing to share pays
    // for one of its own. Each predicate names the line it rides.
    //
    // After the last line of body text. Both body components (Text and
    // TextEdit) expose the end of their last laid-out line through the same
    // lastLine* interface (see bodyTextLoader).
    readonly property bool tntFitsAfterBody: !showReadMore
                                         && hasBody
                                         && bodyTextLoader.item !== null
                                         && bodyTextLoader.item.lastLineEndX + inlineTntGap + tntWidth <= bodyTextLoader.width
    // After a "Read more" button, which is a short label on a line of its own.
    readonly property bool tntFitsAfterReadMore: showReadMore
                                         && readMoreTextWidth + Kirigami.Units.smallSpacing * 2
                                            + inlineTntGap + tntWidth <= textRegionWidth
    // On the bottom line a voice note, audio file or document already draws
    // (elapsed time, file size, page count). The block keeps tntReserveWidth
    // clear at its right end for exactly this.
    readonly property bool tntFitsInAttachment: isAttachmentBlock && !hasBody
    // Space an attachment block leaves at the end of its bottom line so the
    // footer has somewhere to sit without overlapping the block's own text.
    readonly property real tntReserveWidth: tntFitsInAttachment ? tntWidth + inlineTntGap : 0
    readonly property real inlineTntReserve: 0
    readonly property real inlineTntYOffset: Kirigami.Units.smallSpacing / 2
    readonly property real blockTntReserve: tntHeight + tntGap

    function currentSenderNameForReply() {
        return root.isOutgoing ? Whatevr.I18n.i18nc("@label quoted own message sender", "You") : root.senderName
    }

    function requestReply() {
        if (!root.canReply) {
            return
        }
        root.triggerReplyGlow()
        root.messageSelectionClaimed(root.messageId)
        root.replyRequested(root.messageId, root.currentSenderNameForReply(), root.replyPreviewBody.length > 0 ? root.replyPreviewBody : root.body, root.mediaKind, root.mediaMimeType, root.isOutgoing)
        root.conversationFocusRequested()
    }

    // The glow itself runs on a single animation shared by the whole list (see
    // MessageView.playReplyGlow): only one row can be glowing at a time, so
    // giving every row its own five-object animation chain was pure overhead
    // (DN9). Kept as a function because MessageView calls it on the delegate
    // item directly after a jump settles.
    function triggerReplyGlow() {
        root.replyGlowRequested()
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
        if (isAttachmentBlock) {
            w = Math.max(w, attachmentBlockWidth)
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
        ? (imageOnly ? Qt.lighter(Whatevr.Palette.highlight, 1.4) : Whatevr.Palette.highlight)
        : (imageOnly ? "white" : Kirigami.Theme.disabledTextColor)
    readonly property color statusSingleColor: statusIsFailed
        ? Kirigami.Theme.negativeTextColor
        : (imageOnly ? "white" : Kirigami.Theme.disabledTextColor)

    // Per-corner radii for the edge-to-edge media. Top corners follow the
    // bubble's top corners; bottom corners are only rounded for image-only
    // messages (when there is a caption the image meets the text squarely).
    readonly property real bubbleCornerRadius: Kirigami.Units.cornerRadius
    readonly property real mediaTopLeftRadius: (!isOutgoing && !groupStart) ? bubbleCornerRadius * 0.45 : bubbleCornerRadius
    readonly property real mediaTopRightRadius: (isOutgoing && !groupStart) ? bubbleCornerRadius * 0.45 : bubbleCornerRadius
    readonly property real mediaBottomLeftRadius: !imageOnly ? 0 : ((!isOutgoing && !groupEnd) ? bubbleCornerRadius * 0.45 : bubbleCornerRadius)
    readonly property real mediaBottomRightRadius: !imageOnly ? 0 : ((isOutgoing && !groupEnd) ? bubbleCornerRadius * 0.45 : bubbleCornerRadius)
    // Loader.item is declared as QObject, so the frameless subtree is read
    // through a typed handle: that keeps the geometry bindings below statically
    // checked and lets qmlcachegen compile them rather than interpreting them.
    readonly property FramelessBubble framelessBubble: framelessLoader.item as FramelessBubble

    // Outer bounds of everything the row draws, used to place the jump glow and
    // the reaction band. On frameless rows these come from the frameless
    // subtree (which only exists for those rows — see framelessLoader); every
    // other row is just the bubble.
    readonly property real replyGlowLeft: framelessBubble ? framelessBubble.contentLeft : bubble.x
    readonly property real replyGlowTop: framelessBubble ? framelessBubble.contentTop : bubble.y
    readonly property real replyGlowRight: framelessBubble ? framelessBubble.contentRight : bubble.x + bubble.width
    readonly property real replyGlowBottom: framelessBubble ? framelessBubble.contentBottom : bubble.y + bubble.height

    // Bounds of the row's visual body — the bubble, or the sticker slot on
    // frameless rows. Shared by the selection check circle and the hover reply
    // button, which both sit in the free space beside it.
    readonly property real visualX: framelessBubble ? framelessBubble.slotX : bubble.x
    readonly property real visualY: framelessBubble ? framelessBubble.slotY : bubble.y
    readonly property real visualWidth: framelessBubble ? framelessBubble.slotWidth : bubble.width
    readonly property real visualHeight: framelessBubble ? framelessBubble.slotHeight : bubble.height

    readonly property bool hasReactions: reactions !== undefined && reactions !== null && reactions.length > 0
    // The reaction chip row sits in its own band below the bubble; reserve its
    // full height plus the gap above it so it never clips into the next row.
    readonly property real reactionRowReserve: hasReactions
        ? Math.round(reactionRowLoader.height + Kirigami.Units.smallSpacing / 2)
        : 0

    width: listWidth
    height: (framelessBubble
        ? framelessBubble.bottomEdge
        : bubble.y + bubble.height) + reactionRowReserve + (groupEnd ? Kirigami.Units.smallSpacing : Kirigami.Units.smallSpacing / 4)

    HoverHandler {
        id: rowHoverHandler

        acceptedDevices: PointerDevice.Mouse | PointerDevice.TouchPad
        onHoveredChanged: {
            // Ignore hovers caused by rows flying past an idle pointer during
            // a fast fling — latching there would instantiate the reply button
            // and selection TextEdit for every row that crosses the cursor.
            if (hovered && !root.fastFlicking) {
                root.hoverLatched = true
                root.selectionLatched = true
            }
        }
    }

    onFastFlickingChanged: {
        // A fling that ends with the pointer resting on this row counts as a
        // genuine visit; latch now instead of waiting for the next move.
        if (!fastFlicking && rowHoverHandler.hovered) {
            hoverLatched = true
            selectionLatched = true
        }
        if (!fastFlicking) {
            maybeAutoDownloadMedia()
        }
    }

    TapHandler {
        acceptedButtons: Qt.LeftButton
        enabled: !root.selectionModeActive
        onDoubleTapped: {
            if (root.canReply) {
                root.requestReply()
            }
        }
    }

    // Ctrl+click starts (or extends) multi-selection, WhatsApp-Web style.
    TapHandler {
        acceptedButtons: Qt.LeftButton
        acceptedModifiers: Qt.ControlModifier
        onTapped: {
            if (root.messageId.length > 0) {
                root.selectionToggleRequested()
            }
        }
    }

    // Right-click context menu. A MouseArea (not a TapHandler) so the press is
    // consumed before the text-selection TextEdits see it.
    MouseArea {
        anchors.fill: parent
        acceptedButtons: Qt.RightButton
        // This area sits on top of the whole row (z:9) so it consumes the
        // right-press before the body TextEdits — but a MouseArea also owns the
        // item cursor for everything beneath it. So it has to resolve the cursor
        // itself: pointing hand over a body link/mention, I-beam over body text,
        // arrow elsewhere. hoverEnabled drives the per-position re-evaluation.
        hoverEnabled: true
        cursorShape: {
            const item = bodyTextLoader.item
            if (item) {
                const p = mapToItem(item, mouseX, mouseY)
                if (p.x >= 0 && p.y >= 0 && p.x <= item.width && p.y <= item.height) {
                    if (root.displayHasRichText && item.linkAt(p.x, p.y)) {
                        return Qt.PointingHandCursor
                    }
                    return Qt.IBeamCursor
                }
            }
            // Media is clickable, and owning the row's cursor without knowing
            // that made every photo, clip and document feel inert.
            if (mediaSlot.visible) {
                const m = mapToItem(mediaSlot, mouseX, mouseY)
                if (m.x >= 0 && m.y >= 0 && m.x <= mediaSlot.width && m.y <= mediaSlot.height) {
                    return Qt.PointingHandCursor
                }
            }
            return Qt.ArrowCursor
        }
        z: 9
        onPressed: mouse => {
            if (root.messageId.length === 0) {
                mouse.accepted = false
                return
            }
            root.contextMenuRequested(mouse.x, mouse.y)
            mouse.accepted = true
        }
    }

    // Everything multi-select mode needs — the covering click surface, the row
    // tint and the check circle — is built only while that mode is on. It used
    // to be three permanently instantiated (and normally invisible) subtrees on
    // every row, worth roughly seven objects each time (DN9).
    Loader {
        id: selectionChromeLoader

        anchors.fill: parent
        active: root.selectionModeActive

        sourceComponent: Item {
            anchors.fill: parent

            // Selection-mode click surface: every left click toggles this
            // message and nothing underneath (links, reply button, image
            // buttons) reacts.
            MouseArea {
                anchors.fill: parent
                acceptedButtons: Qt.LeftButton
                z: 10
                cursorShape: Qt.PointingHandCursor
                onClicked: root.selectionToggleRequested()
            }

            // Selection tint over the message row, excluding the date-pill
            // region at the top so the day separator is never highlighted.
            Rectangle {
                anchors.fill: parent
                anchors.topMargin: root.dateSeparatorHeight
                z: 6
                visible: root.selected
                color: Qt.alpha(Kirigami.Theme.highlightColor, 0.14)
                radius: Kirigami.Units.cornerRadius
            }

            // Selection check circle in the free space opposite the bubble
            // (mirrors the hover reply button's placement), so nothing shifts.
            Rectangle {
                id: selectionCheck

                readonly property real desiredX: root.isOutgoing
                                                 ? root.visualX - width - Kirigami.Units.smallSpacing
                                                 : root.visualX + root.visualWidth + Kirigami.Units.smallSpacing

                z: 11
                x: Math.round(Math.max(root.outerMargin,
                                       Math.min(root.width - root.outerMargin - width, desiredX)))
                y: Math.round(root.visualY + Math.max(0, root.visualHeight - height) / 2)
                width: Kirigami.Units.iconSizes.smallMedium + Kirigami.Units.smallSpacing
                height: width
                radius: width / 2
                color: root.selected ? Kirigami.Theme.highlightColor : Qt.alpha(Kirigami.Theme.backgroundColor, 0.92)
                border.color: root.selected ? Kirigami.Theme.highlightColor : Qt.alpha(Kirigami.Theme.textColor, 0.38)
                border.width: 1

                Behavior on color {
                    ColorAnimation {
                        duration: Kirigami.Units.shortDuration
                        easing.type: Easing.OutCubic
                    }
                }

                Kirigami.Icon {
                    anchors.centerIn: parent
                    visible: root.selected
                    source: root.tickSource
                    width: Math.round(parent.width * 0.62)
                    height: width
                    color: Kirigami.Theme.highlightedTextColor
                    isMask: true
                }
            }
        }
    }

    TextMetrics {
        id: footerMetrics
        text: root.timeText
        font.pointSize: root.footerTimePointSize
    }

    // Published so FramelessBubble can size its own time label without
    // reaching into this file's ids.
    readonly property real footerTimeWidth: Math.ceil(footerMetrics.advanceWidth)

    Kirigami.ShadowedRectangle {
        id: bubble

        // Frameless rows draw nothing here and build their content in
        // FramelessBubble instead. This is a plain `visible`, so it takes the
        // whole content column with it: nothing that a frameless row still
        // needs may live inside this rectangle.
        visible: !root.frameless

        readonly property real bubbleRadius: Kirigami.Units.cornerRadius

        x: root.isOutgoing
           ? root.width - width - root.outerMargin
           : root.outerMargin + root.senderGutterWidth
        y: root.messageBaseY
        // Image bubbles are exactly the image width (edge-to-edge); text bubbles
        // add inner padding on both sides. Height comes from the self-padding
        // content region.
        width: root.bubbleContentWidth + (root.hasInlineMedia ? 0 : root.innerPadding * 2)
        height: contentColumn.height

        corners.topLeftRadius: !root.isOutgoing && !root.groupStart ? bubbleRadius * 0.45 : bubbleRadius
        corners.topRightRadius: root.isOutgoing && !root.groupStart ? bubbleRadius * 0.45 : bubbleRadius
        corners.bottomLeftRadius: !root.isOutgoing && !root.groupEnd ? bubbleRadius * 0.45 : bubbleRadius
        corners.bottomRightRadius: root.isOutgoing && !root.groupEnd ? bubbleRadius * 0.45 : bubbleRadius

        // Opaque so the wallpaper doodle never shows through. The outgoing tint
        // is the old translucent highlight composited onto the theme background,
        // preserving the prior look on the default wallpaper while staying solid.
        color: root.isOutgoing
                ? Qt.tint(Kirigami.Theme.backgroundColor, Qt.alpha(Whatevr.Palette.highlight, 0.30))
                : Kirigami.Theme.backgroundColor
        border.color: Qt.alpha(Kirigami.Theme.textColor, root.isOutgoing ? 0.05 : 0.12)
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
                // An inline footer sits on a line some other region owns, so
                // that region is what the bubble ends after.
                if (root.tntFitsInAttachment) {
                    return mediaSlot.y + mediaSlot.height + root.innerPadding
                }
                let bottom = 0
                if (root.tntFitsAfterBody) {
                    bottom = bodyTextLoader.y + bodyTextLoader.height
                } else if (root.tntFitsAfterReadMore) {
                    bottom = readMoreLoader.y + readMoreLoader.height
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
                    outgoing: root.replyToIsOutgoing
                    fillColor: Qt.alpha(Kirigami.Theme.textColor, root.isOutgoing ? 0.06 : 0.045)
                    borderColor: Qt.alpha(Kirigami.Theme.textColor, 0.07)
                    onActivated: messageId => root.replyPreviewActivated(messageId)
                }
            }

            Item {
                id: mediaSlot

                visible: (root.hasInlineMedia || root.isAttachmentBlock) && !root.isSticker
                x: root.isAttachmentBlock ? root.innerPadding : 0
                y: root.contentOffsetBeforeMedia() + (root.isAttachmentBlock ? root.innerPadding : 0)
                width: root.isAttachmentBlock ? root.attachmentBlockWidth : root.imageDisplayWidth
                height: visible ? (root.isAttachmentBlock ? root.attachmentBlockHeight : root.imageDisplayHeight) : 0
                clip: !root.isAttachmentBlock

                // Lazily instantiate the image stack (shader-effect images,
                // backdrop, loading overlay) only for image messages. Text and
                // sticker delegates skip it entirely — this is the bulk of the
                // per-delegate node cost behind scroll-time instantiation spikes.
                // One loader per media family, each gated on its own kind, so a
                // voice note never instantiates the image stack and a photo
                // never instantiates a player.
                Loader {
                    anchors.fill: parent
                    active: mediaSlot.visible && root.isImage
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
                    //
                    // Held until the full image is *fully* opaque, not until it
                    // reports Ready: the image below fades in over
                    // shortDuration, so cutting on Ready left the bubble showing
                    // its empty plate for the whole of that fade. That is the
                    // blink a completed download used to end with.
                    visible: root.hasThumbnailImage && (!root.hasLocalImage || roundedImg.opacity < 1)
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

                        // Latched readiness, set from onStatusChanged rather than
                        // read off `status` inside the source binding (which would
                        // make source depend on its own load state and loop). Reset
                        // when the underlying file changes on delegate reuse.
                        property bool everDecoded: false
                        readonly property string targetPath: root.mediaLocalPath
                        onTargetPathChanged: everDecoded = false
                        onStatusChanged: if (status === Image.Ready) everDecoded = true

                        anchors.fill: parent
                        visible: false
                        // Hold the full-res decode while flinging (unless it is
                        // already decoded), letting the thumbnail carry the scroll.
                        source: root.mediaSourceActive && mediaSlot.visible && root.hasLocalImage
                                && (!root.fastFlicking || img.everDecoded)
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

                    // Click-to-open lightbox. SingleTap is made exclusive with
                    // DoubleTap so double-tap-to-reply on the photo still wins.
                    TapHandler {
                        acceptedButtons: Qt.LeftButton
                        enabled: root.hasLocalImage && !root.isSticker && !root.selectionModeActive
                        exclusiveSignals: TapHandler.SingleTap | TapHandler.DoubleTap
                        onSingleTapped: root.imageActivated(root.mediaLocalPath)
                    }

                    HoverHandler {
                        enabled: root.hasLocalImage && !root.selectionModeActive
                        cursorShape: Qt.PointingHandCursor
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

                    // A decode in progress is not, by itself, a reason to cover
                    // the bubble: the thumbnail below is already showing the
                    // picture. Only a row with nothing to look at, an active
                    // download, or a failure gets chrome, so an ordinary decode
                    // (including a re-decode after a fling settles) no longer
                    // darkens and un-darkens the image.
                    readonly property bool hasPicture: root.hasThumbnailImage && thumb.status === Image.Ready
                    visible: !root.hasLocalImage
                             || root.mediaDownloading
                             || thumb.status === Image.Loading
                             || (img.status === Image.Loading && !hasPicture)
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
                            visible: (root.mediaDownloading && root.mediaDownloadProgress < 0)
                                     || (!root.mediaDownloading && !root.hasLocalImage && root.hasThumbnailImage && thumb.status === Image.Loading)
                                     || (root.hasLocalImage && img.status === Image.Loading)
                            running: visible
                            implicitWidth: Kirigami.Units.gridUnit * 2
                            implicitHeight: Kirigami.Units.gridUnit * 2
                        }

                        ProgressCircle {
                            anchors.horizontalCenter: parent.horizontalCenter
                            visible: root.mediaDownloading && root.mediaDownloadProgress >= 0
                            progress: Math.max(0, root.mediaDownloadProgress)
                            width: Kirigami.Units.gridUnit * 2
                            height: width
                        }

                        Button {
                            anchors.horizontalCenter: parent.horizontalCenter
                            visible: !root.hasLocalImage && !root.mediaDownloading
                            icon.name: "folder-download-symbolic"
                            text: Whatevr.I18n.i18nc("@action:button", "Load image")
                            enabled: root.messageId.length > 0
                            onClicked: {
                                Whatevr.ProtocolController.downloadMessageMedia(root.messageId)
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

                Loader {
                    anchors.fill: parent
                    active: mediaSlot.visible && root.isPlayableVideo
                    sourceComponent: VideoBubble {
                        row: root
                        topLeftRadius: root.mediaTopLeftRadius
                        topRightRadius: root.mediaTopRightRadius
                        bottomLeftRadius: root.mediaBottomLeftRadius
                        bottomRightRadius: root.mediaBottomRightRadius
                    }
                }

                Loader {
                    anchors.fill: parent
                    active: mediaSlot.visible && (root.isVoice || root.isAudioFile)
                    sourceComponent: VoiceBubble {
                        row: root
                    }
                }

                Loader {
                    anchors.fill: parent
                    active: mediaSlot.visible && root.isDocument
                    sourceComponent: DocumentBubble {
                        row: root
                    }
                }
            }

            // Plain bodies render with a cheap Text node; rich bodies (markup,
            // links, inline-enlarged emoji) need a QTextDocument and keep the
            // TextEdit. Both expose the end of their last laid-out line via the
            // same lastLine* interface (tntFitsAfterBody, footerSlot).
            Component {
                id: plainBodyComponent

                Text {
                    id: plainBody

                    // End of the last laid-out line, captured during layout.
                    // Mirrors what positionToRectangle(length) reports for the
                    // rich TextEdit.
                    property real lastLineEndX: 0
                    property real lastLineY: 0
                    property real lastLineHeight: 0

                    // While searching this chat, render the body as StyledText
                    // with the matches background-highlighted. StyledText (not
                    // RichText) keeps the line-based layout engine, so
                    // onLineLaidOut still fires and the inline footer stays put.
                    text: root.body
                    textFormat: Text.PlainText
                    wrapMode: Text.Wrap
                    color: root.isRevoked || root.isUnsupported ? Kirigami.Theme.disabledTextColor : Kirigami.Theme.textColor
                    font.family: Kirigami.Theme.defaultFont.family
                    font.pointSize: root.bodyPointSize
                    font.weight: Font.Normal
                    font.italic: root.isRevoked || root.isUnsupported

                    onLineLaidOut: line => {
                        if (line.isLast) {
                            lastLineEndX = line.x + line.implicitWidth
                            lastLineY = line.y
                            lastLineHeight = line.height
                        }
                    }

                }
            }

            Component {
                id: richBodyComponent

                TextEdit {
                    id: richBody

                    // Rect after the last character; re-evaluates with the
                    // document (length) and the wrap geometry (width/height).
                    readonly property rect endCursorRect: width > 0 && height > 0
                                                          ? positionToRectangle(length)
                                                          : Qt.rect(0, 0, 0, 0)
                    readonly property real lastLineEndX: endCursorRect.x
                    readonly property real lastLineY: endCursorRect.y
                    readonly property real lastLineHeight: endCursorRect.height

                    text: root.displayRichText
                    textFormat: TextEdit.RichText
                    readOnly: true
                    selectByMouse: true
                    selectByKeyboard: true
                    persistentSelection: true
                    wrapMode: TextEdit.Wrap
                    color: Kirigami.Theme.textColor
                    font.family: Kirigami.Theme.defaultFont.family
                    font.pointSize: root.bodyPointSize
                    font.weight: Font.Normal
                    onLinkActivated: link => {
                        if (link.startsWith("wamention:")) {
                            root.mentionClicked(link.substring("wamention:".length))
                        } else if (link.startsWith("wamention-all:")) {
                            root.mentionAllClicked()
                        } else {
                            Qt.openUrlExternally(link)
                        }
                    }
                    // Pass-through hover surface: a MouseArea re-applies its
                    // cursorShape on every move (a HoverHandler only re-applies
                    // on enter/leave, so moving onto an inline link mid-hover
                    // never refreshed the cursor). NoButton lets press/click
                    // fall through to the TextEdit for selection and link
                    // activation.
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

            // Text-only and media-only delegates split the cost: a body item is
            // only built when there is body text (and never for frameless rows,
            // whose body renders in the sticker slot as a jumbo emoji).
            Loader {
                id: bodyTextLoader

                active: root.hasBody && !root.frameless
                x: root.innerPadding
                y: root.contentOffsetBeforeBody()
                width: root.textRegionWidth

                sourceComponent: root.displayHasRichText ? richBodyComponent : plainBodyComponent
            }

            // On-demand selection surface for plain bodies (rich bodies select
            // on their TextEdit directly). Its glyphs are transparent so the
            // Text underneath stays the visible one — only the selection
            // highlight and the selected glyphs paint here, which keeps any
            // sub-pixel layout difference between Text and TextEdit from ever
            // shifting the message.
            Loader {
                id: selectionEditLoader

                // Asynchronous: this instantiates a TextEdit, and so a
                // QTextDocument, the first time the pointer touches the row.
                // Doing that inline meant rows sliding under a stationary
                // cursor each stalled a frame the first time they passed it,
                // which is exactly the wheel "resisting once or twice".
                asynchronous: true
                active: root.selectionLatched
                        && root.hasBody
                        && !root.displayHasRichText
                        && !root.frameless
                        && !root.pooled
                x: bodyTextLoader.x
                y: bodyTextLoader.y
                width: bodyTextLoader.width
                height: bodyTextLoader.height

                sourceComponent: TextEdit {
                    id: selectionEdit

                    text: root.body
                    textFormat: TextEdit.PlainText
                    textMargin: 0
                    readOnly: true
                    selectByMouse: true
                    selectByKeyboard: true
                    persistentSelection: true
                    wrapMode: TextEdit.Wrap
                    color: "transparent"
                    selectionColor: Kirigami.Theme.highlightColor
                    selectedTextColor: Kirigami.Theme.highlightedTextColor
                    font.family: Kirigami.Theme.defaultFont.family
                    font.pointSize: root.bodyPointSize
                    font.weight: Font.Normal

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

                objectName: "chatBubble.footerSlot"
                // Image-only: overlay on the media bottom-right (over the
                // vignette). Otherwise sit at the right inner edge, inline with
                // the last text line or on its own row.
                x: {
                    if (root.imageOnly) {
                        return mediaSlot.x + mediaSlot.width - width - root.footerInset
                    }
                    // Flush with the block's own right edge rather than the
                    // bubble's, so it lines up with the text above it.
                    if (root.tntFitsInAttachment) {
                        return mediaSlot.x + mediaSlot.width - width
                    }
                    return Math.max(0, parent.width - root.footerInset - width)
                }
                y: {
                    if (root.imageOnly) {
                        return mediaSlot.y + mediaSlot.height - height - root.footerInset
                    }
                    if (root.tntFitsInAttachment) {
                        // Sitting on the block's bottom line, not under it.
                        return mediaSlot.y + mediaSlot.height - height
                    }
                    const off = root.contentOffsetBeforeFooter()
                    if (root.tntFitsAfterReadMore) {
                        return readMoreLoader.y + Math.round((readMoreLoader.height - height) / 2)
                    }
                    if (root.hasBody) {
                        return root.tntFitsAfterBody
                            ? bodyTextLoader.y + bodyTextLoader.item.lastLineY + bodyTextLoader.item.lastLineHeight - height + root.inlineTntYOffset
                            : off + root.tntGap
                    }
                    return Math.max(0, off)
                }
                width: root.tntWidth
                height: root.tntHeight

                Rectangle {
                    anchors.fill: parent
                    anchors.margins: -root.tntSpacing
                    visible: root.imageOnly && root.isPlayableVideo
                    z: -1
                    radius: height / 2
                    color: Qt.alpha("black", 0.55)
                }

                // Delivery status, built only for rows that show one — i.e.
                // never for incoming messages. The single/double forms share
                // one icon pair rather than instantiating both layouts (DN9).
                Loader {
                    id: statusAreaLoader

                    active: root.showStatusIcon
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    width: root.statusAreaWidth
                    height: root.statusIconSize

                    sourceComponent: Item {
                        anchors.fill: parent

                        // Doubles as the single icon (clock / tick / error) and
                        // as the first of the two delivered/read ticks.
                        Kirigami.Icon {
                            x: root.statusIsDoubleTick
                               ? 0
                               : Math.round((parent.width - width) / 2)
                            anchors.verticalCenter: parent.verticalCenter
                            source: root.statusIsDoubleTick ? root.tickSource : root.statusSingleIcon
                            width: root.statusIconSize
                            height: root.statusIconSize
                            color: root.statusIsDoubleTick ? root.statusTickColor : root.statusSingleColor
                            isMask: true
                        }

                        Kirigami.Icon {
                            visible: root.statusIsDoubleTick
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
                    anchors.right: root.showStatusIcon ? statusAreaLoader.left : parent.right
                    anchors.rightMargin: root.showStatusIcon ? root.tntSpacing : 0
                    anchors.verticalCenter: parent.verticalCenter
                    text: root.timeText
                    color: root.footerTextColor
                    width: Math.ceil(footerMetrics.advanceWidth)
                    horizontalAlignment: Text.AlignRight
                    font.pointSize: root.footerTimePointSize
                }

                // Pin / star / edit marks. Most messages carry none, so the
                // three icons (and the anchor chain that used to thread them
                // together) are built only when at least one applies; the Row
                // drops the ones that do not, so ordering stays automatic.
                Loader {
                    active: root.showPinMark || root.showStarMark || root.showEditMark
                    anchors.right: timeLabel.left
                    anchors.rightMargin: root.tntSpacing
                    anchors.verticalCenter: parent.verticalCenter

                    sourceComponent: Row {
                        spacing: root.tntSpacing

                        Kirigami.Icon {
                            visible: root.showPinMark
                            source: "pin-symbolic"
                            width: root.pinMarkSize
                            height: root.pinMarkSize
                            color: root.footerTextColor
                            isMask: true
                        }

                        Kirigami.Icon {
                            visible: root.showStarMark
                            source: "starred-symbolic"
                            width: root.starMarkSize
                            height: root.starMarkSize
                            color: root.footerTextColor
                            isMask: true
                        }

                        Kirigami.Icon {
                            visible: root.showEditMark
                            source: "document-edit-symbolic"
                            width: root.editMarkSize
                            height: root.editMarkSize
                            color: root.footerTextColor
                            isMask: true
                        }
                    }
                }
            }
        }
    }

    // Frameless rows — stickers and 1-3 emoji "jumbo" messages — draw no
    // bubble: a bare slot with a floating time pill beside it. All of that used
    // to be instantiated on every single row and merely hidden, including a
    // jumbo-emoji Text bound to `root.body`, which laid the message body out a
    // second time for rows that never showed it (DN9). The subtree publishes
    // the geometry the row needs (content bounds for the glow, slot bounds for
    // the selection check and reply button, bottom edge for row height) so the
    // outer bindings can fall back to the bubble when it does not exist.
    Loader {
        id: framelessLoader

        active: root.frameless
        anchors.fill: parent

        sourceComponent: FramelessBubble {
            row: root
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
    // pays for the button, only the rows the pointer actually visits do. Off
    // the frame's critical path too, for the same reason as the selection
    // surface above: rows crossing an idle cursor must not each cost a stall.
    Loader {
        anchors.fill: parent
        asynchronous: true
        active: root.hoverLatched && root.canReply && !root.pooled
        z: 8

        sourceComponent: Item {
            ToolButton {
                id: replyButton

                readonly property real desiredX: root.isOutgoing
                                                 ? root.visualX - width - Kirigami.Units.smallSpacing
                                                 : root.visualX + root.visualWidth + Kirigami.Units.smallSpacing

                enabled: opacity > 0.01
                opacity: (rowHoverHandler.hovered || hovered || pressed) ? 1 : 0
                x: Math.round(Math.max(root.outerMargin,
                                       Math.min(root.width - root.outerMargin - width, desiredX)))
                y: Math.round(root.visualY + Math.max(0, root.visualHeight - height) / 2)
                width: Math.round(Math.max(Kirigami.Units.iconSizes.smallMedium + Kirigami.Units.smallSpacing,
                                           Math.min(Kirigami.Units.gridUnit * 1.45,
                                                    root.visualHeight - Kirigami.Units.smallSpacing)))
                height: width
                icon.name: "smiley-add-symbolic"
                // Set both dimensions to the constant directly; binding icon.height to
                // icon.width loops through the control's implicit-size machinery.
                icon.width: Kirigami.Units.iconSizes.smallMedium
                icon.height: Kirigami.Units.iconSizes.smallMedium
                text: Whatevr.I18n.i18nc("@action:button", "React")
                display: AbstractButton.IconOnly
                focusPolicy: Qt.NoFocus
                hoverEnabled: true
                onClicked: root.reactionPickerRequested(x + width / 2, y)

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

    // Reaction chips in their own band below the bubble, aligned with the
    // bubble's edge (left for incoming, right for outgoing via the row's
    // layoutDirection). The row is capped at the bubble's width so many
    // reactions wrap instead of widening past the message; the floor keeps a
    // couple of chips per line on very narrow bubbles. Sits above the
    // context-menu surface (z:9) so chips own their clicks, but is disabled in
    // selection mode so the covering toggle surface wins there.
    // Built only for rows that actually carry reactions: ReactionRow's Repeater
    // drags in a QQmlDelegateModel and its two delegate groups even when the
    // reaction list is empty, which most rows' lists are (DN9).
    Loader {
        id: reactionRowLoader

        active: root.hasReactions
        z: 10
        width: Math.max(root.replyGlowRight - root.replyGlowLeft,
                        Kirigami.Units.gridUnit * 8)
        x: Math.round(root.isOutgoing ? root.replyGlowRight - width : root.replyGlowLeft)
        y: Math.round(root.replyGlowBottom + Kirigami.Units.smallSpacing / 2)

        sourceComponent: ReactionRow {
            reactions: root.reactions
            enabled: !root.selectionModeActive
            width: reactionRowLoader.width
            layoutDirection: root.isOutgoing ? Qt.RightToLeft : Qt.LeftToRight

            onToggleRequested: emoji => root.reactionToggleRequested(emoji)
            onDetailsRequested: root.reactionDetailsRequested()
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
                y: root.dateSeparatorHeight + root.unreadSeparatorHeight + Math.max(0, (root.senderHeaderHeight - height) / 2)
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
                y: root.dateSeparatorHeight + root.unreadSeparatorHeight + Math.max(0, (root.senderHeaderHeight - height) / 2)
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

    // "N unread messages" divider, loaded only on the unread-anchor row. Sits
    // below the day pill when both are present.
    Loader {
        id: unreadSeparatorLoader

        active: root.showUnreadSeparator
        x: root.outerMargin
        y: root.dateSeparatorHeight + Kirigami.Units.largeSpacing / 2
        width: Math.max(0, root.width - root.outerMargin * 2)

        sourceComponent: UnreadSeparator {
            count: root.unreadSeparatorCount
        }
    }

    // While selecting, clicking the day pill toggles that whole day's
    // selection. Sits above the full-row selection surface (z:10) so the pill
    // gets the click instead of toggling just this message. The pill itself is
    // never given a selected/highlighted state.
    MouseArea {
        visible: root.selectionModeActive && root.showDateSeparator
        enabled: visible
        z: 12
        x: dateSeparatorLoader.x
        y: dateSeparatorLoader.y
        width: dateSeparatorLoader.width
        height: dateSeparatorLoader.height
        acceptedButtons: Qt.LeftButton
        cursorShape: Qt.PointingHandCursor
        onClicked: root.daySelectionToggleRequested()
    }

}
