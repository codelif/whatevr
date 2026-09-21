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
    // Whether the daemon attached a media object at all. A kind alone does not
    // imply something to fetch: a poll and a contact card have kinds and no
    // bytes behind them.
    required property bool hasMedia
    required property bool isKept
    // Kind-specific payloads, empty maps on rows of another kind. One `var` per
    // family rather than a role per field: the bubble that renders a kind is the
    // only thing that reads its payload.
    required property var location
    required property var liveShare
    required property var contacts
    required property var poll
    required property var invite
    required property var eventInfo
    required property var album
    // The card for a link in the text. The only payload that arrives on a row
    // of another kind: this row is a text row, and the card sits above the
    // words rather than replacing them.
    required property var linkPreview
    required property var interactive
    required property var commerce
    required property var stickerPack
    required property var callLog
    required property var system
    required property var waiting
    required property bool isRevoked
    required property bool isEdited
    // WhatsApp forward marker (daemon `forwarded`), rendered as a header
    // above the bubble content in framed bubbles.
    required property bool isForwarded
    // Sender device id: 0 is the primary phone app, anything else a linked
    // device (Web/Desktop or another companion). Drives the footer mark.
    required property int senderDevice
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
    // Open the poll's result breakdown. optionIndex focuses one answer; -1 opens
    // on the whole poll.
    signal pollVotersRequested(int optionIndex)
    // Open an event's full list of answers. `response` narrows it to one of
    // them; empty opens on all three.
    signal eventResponsesRequested(string response)
    signal replyPreviewActivated(string messageId)
    signal readMoreRequested(string messageId)
    // A downloaded message photo was clicked: open it full screen.
    signal imageActivated(string messageId, string localPath)
    // A video, GIF or video note asked to open full screen. The path and
    // duration ride along so the viewer needs no lookup, and startAt carries
    // the second the inline copy had reached, so opening full screen continues
    // a clip instead of restarting it.
    signal videoActivated(string messageId, string localPath, string streamUrl, string streamId, string kind, int durationSecs, real startAt)
    // A tile in an album was clicked. It carries the album rather than the
    // picture, because opening one picture out of a set that was sent together
    // and giving no way to reach the rest is the wrong thing: the viewer takes
    // the whole album and starts at this index.
    signal albumItemActivated(string albumMessageId, int index)
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
    // Forwarded header: framed bubbles only; frameless rows (stickers/jumbo/
    // video notes) draw no plate to hang it on.
    readonly property bool showForwardedHeader: isForwarded && !frameless
    readonly property real forwardedHeaderHeight: showForwardedHeader
        ? forwardedLoader.height
        : 0
    readonly property bool canReply: messageId.length > 0
                                     && !isRevoked
                                     && !centeredPill
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
    // A pill claims no side of the transcript, so it needs neither the avatar
    // gutter nor the sender name above it: both would be labelling a message
    // that nobody sent.
    readonly property real senderGutterWidth: showSenderGutter && !centeredPill ? senderAvatarSize + Kirigami.Units.smallSpacing : 0
    readonly property real senderHeaderHeight: showSenderHeader && !centeredPill
        ? Math.max(senderAvatarSize, senderHeaderLoader.item ? senderHeaderLoader.item.labelImplicitHeight : 0)
        : 0
    /// How much of the column a bubble may take at its widest.
    ///
    /// A share as well as a ceiling. The ceiling alone is what a wide window
    /// needs, but it does not bind at all once the pane is narrower than it,
    /// and then a bubble that asks for everything gets everything: a hero card
    /// always asks for the full content width, so it ran edge to edge with a
    /// few pixels of margin and no side left to tell an incoming message from
    /// an outgoing one. The gap opposite a bubble is what says which way it
    /// went, so it has to survive the window being dragged narrow.
    readonly property real maxBubbleWidthShare: 0.84
    readonly property real availableBubbleWidth: Math.max(0, listWidth - outerMargin * 2 - senderGutterWidth)
    readonly property real maxBubbleWidth: Math.max(Kirigami.Units.gridUnit * 4,
                                                    Math.min(availableBubbleWidth * maxBubbleWidthShare,
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
    // Kinds that render like a photo: they span the bubble edge to edge and
    // drive its width.
    readonly property bool isVideo: mediaKind === "video"
    readonly property bool isGif: mediaKind === "gif"
    readonly property bool isVideoNote: mediaKind === "video_note"
    readonly property bool isPlayableVideo: isVideo || isGif || isVideoNote
    // Kind wins over mime: a gif arrives as a VideoMessage whose mime can
    // still say image/gif, and deriving this from mime alone built the image
    // stack and the video stack on top of each other, two download buttons
    // included.
    // Card kinds are excluded for the same reason GIFs are: a location's media
    // is a PNG map, so mime alone built the photo stack underneath the card,
    // download button and all.
    readonly property bool isImage: !isPlayableVideo && !isSticker && !isCardBlock
                                    && mediaMimeType.startsWith("image/")
    // Kinds that render as a fixed-height row inside the padded content, more
    // like a line of text than a picture.
    readonly property bool isVoice: mediaKind === "voice"
    readonly property bool isAudioFile: mediaKind === "audio"
    readonly property bool isDocument: mediaKind === "document"
    // Kinds that render as a card: a block of their own inside the bubble whose
    // height its own content decides, rather than the fixed row a voice note or
    // a document gets. Width flows down from the bubble, height flows up from
    // the card, which is the same contract FramelessBubble already uses.
    readonly property bool isLocation: mediaKind === "location"
    readonly property bool isLiveLocation: mediaKind === "live_location"
    readonly property bool isContactCard: mediaKind === "contact" || mediaKind === "contacts"
    readonly property bool isPoll: mediaKind === "poll"
    readonly property bool isGroupInvite: mediaKind === "group_invite"
    readonly property bool isEvent: mediaKind === "event"
    // An album is a card by the same contract as the rest: the row hands it a
    // width and reads back a height. It is not an image block, because the row
    // has no picture of its own to be sized by; its pictures are its children.
    readonly property bool isAlbum: mediaKind === "album"
    // A link preview is a card by the same contract as the rest, and the only
    // one that shares its bubble with body text. It is keyed on the payload
    // rather than on the kind because the kind is `text`: the words are still
    // the message, so nothing about the row changes except that a card now
    // sits above them.
    readonly property bool isLinkPreview: mediaKind.length === 0
                                          && !isRevoked
                                          && linkPreview !== undefined
                                          && linkPreview !== null
                                          && String(linkPreview.url ?? "").length > 0
    // A business message. Four wire shapes arrive as one kind, because the
    // daemon flattened them: this build never learns which one it was.
    readonly property bool isInteractive: mediaKind === "interactive"
    // A product, an order or a payment. One card: they differ by a line on it.
    readonly property bool isCommerce: mediaKind === "product" || mediaKind === "order" || mediaKind === "payment"
    readonly property bool isStickerPack: mediaKind === "sticker_pack"
    // A message that arrived and would not decrypt. A card rather than a
    // tombstone because it is not over: something is still being asked for, and
    // the row has a button.
    readonly property bool isWaiting: mediaKind === "waiting"
    readonly property bool isCardBlock: isLocation || isLiveLocation || isContactCard || isPoll || isGroupInvite || isEvent || isAlbum || isLinkPreview
                                        || isInteractive || isCommerce || isStickerPack || isWaiting
    readonly property bool isAttachmentBlock: isVoice || isAudioFile || isDocument || isCardBlock
    // A call that happened. Not a message anybody wrote, and the one row here
    // that draws no plate and picks no side: see the centered-pill mode below.
    readonly property bool isCallLog: mediaKind === "call_log"
    // Something the chat did to itself: a membership change, a setting, a
    // security code. A pill for the same reason a call log is one.
    readonly property bool isSystemEvent: mediaKind === "system"
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
    // Rows that are not somebody talking. A call happened; nobody said it, so
    // there is no side of the transcript it belongs on and no plate to put it
    // in. It draws as a pill in the middle, the way the day separator does,
    // with no avatar, no sender name and no reply affordance. A system event is
    // the same shape of thing: the chat changed, and nobody is claiming to have
    // said so.
    //
    // A sibling of `frameless` rather than a variant of it: a frameless row is
    // still a message from somebody, drawn without its box.
    readonly property bool centeredPill: isCallLog || isSystemEvent
    readonly property real jumboEmojiPixelSize: Kirigami.Units.gridUnit
        * (displayEmojiOnlyCount === 1 ? 2.8 : displayEmojiOnlyCount === 2 ? 2.2 : 1.8)
    readonly property bool isAnimatedSticker: isSticker && (mediaAnimated || mediaMimeType === "image/gif")
    readonly property bool isLottieSticker: isSticker && mediaMimeType === "application/was"
    // Every sticker that is not a Lottie animation draws through Image or
    // AnimatedImage. Deliberately not `&& isImage`: that flag excludes stickers
    // by construction, so requiring it made this false for every sticker there
    // has ever been, and the renderers it gates never drew anything.
    readonly property bool isRenderableStickerImage: isSticker && !isLottieSticker
    readonly property bool hasLocalImage: isImage && mediaLocalPath.length > 0
    // Stickers included, for the same reason: their placeholder is the one thing
    // on screen until the sticker itself decodes.
    readonly property bool hasThumbnailImage: (isImage || isSticker) && mediaThumbnailLocalPath.length > 0
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
    // `hasMedia` is the daemon's own answer to "is there anything to fetch",
    // and it is the only one worth trusting: a kind is not a promise of bytes.
    // Deriving this from `mediaKind.length > 0` meant every structured kind
    // (poll, contact card, system event) looked downloadable and fired a
    // media.download on every scroll-in, falling through to the documents
    // auto-download preference on the way.
    readonly property bool hasDownloadableMedia: hasMedia && !mediaIsLocal && !isUnsupported
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
        // A map is drawn by the daemon from cached tiles, not pulled from
        // WhatsApp, so it answers to its own preference rather than the photo
        // one, and it defaults on: a location bubble without a map is a pair of
        // numbers.
        if (isCardBlock)
            return isLocation || isLiveLocation ? (prefs.auto_fetch_maps ?? true) : false;
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
    // A card wants the whole content width: it is showing a picture of
    // somewhere, not a line of metadata.
    // A card that shows a picture of somewhere wants the whole content width. A
    // card that shows a few lines and a button does not, and stretching it to
    // the ceiling leaves a bubble mostly full of nothing with its action
    // stranded at the far left. So a card may publish a width it would rather
    // have, the same way it already publishes a height; cards that publish none
    // keep filling, which is what a map and a mosaic want.
    readonly property real attachmentBlockWidth: isCardBlock
        ? (cardBlockWidth > 0 ? Math.min(maxContentWidth, cardBlockWidth) : maxContentWidth)
        : Math.min(maxContentWidth, Kirigami.Units.gridUnit * 17)
    property real cardBlockWidth: 0
    // A card publishes its own height, so the row asks it rather than guessing.
    // The fallback is what the slot reserves before the card has laid out, and
    // it is deliberately close to the real thing so nothing jumps.
    property real cardBlockHeight: Kirigami.Units.gridUnit * 12
    // Two lines for a voice note: the waveform and the line under it that now
    // carries the timestamp too, so the block no longer reserves a third. An
    // audio file needs three, since its name will not share a line with its
    // seek track the way a nameless recording's waveform does.
    readonly property real attachmentBlockHeight: isCardBlock
        ? cardBlockHeight
        : isAudioFile
            ? Kirigami.Units.gridUnit * 3.4
            : isDocument
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
    // A small trash shown for a deleted message whose content was kept
    // (anti-delete): the body stays readable, so the mark is what tells it
    // apart from an ordinary message.
    readonly property bool showDeletedMark: isRevoked && hasBody
    readonly property real deletedMarkSize: Math.max(1, Math.round(footerMetrics.height * 0.92))
    readonly property real deletedMarkReserve: showDeletedMark ? deletedMarkSize + tntSpacing : 0
    // A small device shown when the sender wrote from a linked device rather
    // than their primary phone app (senderDevice > 0).
    readonly property bool showLinkedMark: senderDevice > 0
    readonly property real linkedMarkSize: Math.max(1, Math.round(footerMetrics.height * 0.92))
    readonly property real linkedMarkReserve: showLinkedMark ? linkedMarkSize + tntSpacing : 0
    // A small star shown left of the edit mark / timestamp when the message is
    // starred (mirrors the edit-mark reserve so the footer width stays correct).
    readonly property bool showStarMark: isStarred && !isRevoked
    readonly property real starMarkSize: Math.max(1, Math.round(footerMetrics.height * 0.92))
    readonly property real starMarkReserve: showStarMark ? starMarkSize + tntSpacing : 0
    // A small pin shown left of the star mark when the message is pinned.
    readonly property bool showPinMark: isPinned && !isRevoked
    readonly property real pinMarkSize: Math.max(1, Math.round(footerMetrics.height * 0.92))
    readonly property real pinMarkReserve: showPinMark ? pinMarkSize + tntSpacing : 0
    // A bookmark, leftmost of the marks, when somebody asked for a disappearing
    // message to stay. A revoked row keeps nothing, so it shows nothing.
    readonly property bool showKeepMark: isKept && !isRevoked
    readonly property real keepMarkSize: Math.max(1, Math.round(footerMetrics.height * 0.92))
    readonly property real keepMarkReserve: showKeepMark ? keepMarkSize + tntSpacing : 0
    readonly property real tntWidth: Math.ceil(footerMetrics.advanceWidth
                                               + editMarkReserve
                                               + deletedMarkReserve
                                               + linkedMarkReserve
                                               + starMarkReserve
                                               + pinMarkReserve
                                               + keepMarkReserve
                                               + (showStatusIcon ? statusAreaWidth + tntSpacing : 0))
    readonly property real tntHeight: Math.ceil(Math.max(footerMetrics.height, showStatusIcon ? statusIconSize : 0,
                                                         showEditMark ? editMarkSize : 0,
                                                         showDeletedMark ? deletedMarkSize : 0,
                                                         showLinkedMark ? linkedMarkSize : 0,
                                                         showStarMark ? starMarkSize : 0,
                                                         showPinMark ? pinMarkSize : 0,
                                                         showKeepMark ? keepMarkSize : 0))
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
    // A business card, a commerce card and a shared sticker pack all end on a
    // line that spans the card: a button as wide as the plate, or a sentence
    // that wraps across it. There is no corner left to tuck the time into, so
    // it takes a line of its own under the card rather than sitting on top of
    // the last one.
    readonly property bool tntFitsInAttachment: isAttachmentBlock && !hasBody
                                                && !isInteractive && !isCommerce && !isStickerPack && !isWaiting
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
        // Deliberately no reply glow. The glow's job is to point out a row you
        // did not choose: MessageView plays it when a jump lands. Flashing the
        // row the pointer is already on, because it was just double-clicked,
        // tells the reader nothing and reads as the screen glitching.
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
        const top = root.innerPadding + root.forwardedHeaderHeight
        return root.hasReplyPreview
            ? top + replyPreviewLoader.height + Kirigami.Units.smallSpacing
            : (root.showForwardedHeader ? top : 0)
    }

    function contentOffsetBeforeBody() {
        const top = root.innerPadding + root.forwardedHeaderHeight
        if (mediaSlot.visible) {
            return mediaSlot.y + mediaSlot.height + Kirigami.Units.smallSpacing
        }
        if (root.hasReplyPreview) {
            return top + replyPreviewLoader.height + Kirigami.Units.smallSpacing - root.bodyTopInsetCorrection
        }
        return top - root.bodyTopInsetCorrection
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
            return root.innerPadding + root.forwardedHeaderHeight + replyPreviewLoader.height + Kirigami.Units.smallSpacing
        }
        return root.innerPadding + root.forwardedHeaderHeight
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

    // Whether the media slot has real artwork on screen rather than an empty
    // plate. Bound from inside whichever media stack is loaded (see the Binding
    // in the image component below, and the one in VideoBubble); a plain value
    // rather than a binding here, so a text row or a recycled delegate with no
    // stack at all reads false.
    property bool mediaArtworkShown: false
    // The footer is only sitting on a picture once there is a picture. An
    // undownloaded photo is an empty plate with a "Load image" button on it, and
    // white-on-nothing under a scrim is neither readable nor honest about what
    // is there.
    readonly property bool footerOverArtwork: footerOverPicture && mediaArtworkShown

    /// Where the footer's scrim starts, as a fraction of its own height: the
    /// footer's top edge, so nothing above the line of text is darkened at all.
    /// The strip is tntHeight tall sitting footerInset off the bottom, inside a
    /// rectangle of tntHeight + footerInset * 2, which puts that edge exactly
    /// one inset down from its top.
    readonly property real footerScrimOnset: footerInset / Math.max(1, tntHeight + footerInset * 2)
    /// How dark it gets at the very bottom. Enough to carry white on a bright
    /// photograph and no more: this sits on someone's picture.
    readonly property real footerScrimPeak: 0.42

    // Rows whose time and ticks land on a picture rather than on a plate. An
    // album is one of them without being `imageOnly`: that flag means media
    // that drives the bubble's width and runs edge to edge, which a mosaic
    // does not, but the footer still sits on artwork and still needs the scrim
    // under it and the light tones on it. One vignette across the bottom of the
    // whole mosaic, not one per tile: the tiles are one picture cut up.
    readonly property bool footerOverPicture: imageOnly || isAlbum
    // Footer (time + ticks) colours. Over the image-only vignette they switch to
    // light tones for contrast; otherwise the muted theme colours are used.
    //
    // Switched rather than cross-faded: a Behavior here is three more objects on
    // every row in the chat, text rows included, to smooth one frame on the two
    // kinds that can ever make the change. The vignette under them fades, which
    // is the part the eye follows.
    readonly property color footerTextColor: footerOverArtwork ? "white" : Kirigami.Theme.disabledTextColor
    readonly property color statusTickColor: statusIsRead
        ? (footerOverArtwork ? Qt.lighter(Whatevr.Palette.highlight, 1.4) : Whatevr.Palette.highlight)
        : (footerOverArtwork ? "white" : Kirigami.Theme.disabledTextColor)
    readonly property color statusSingleColor: statusIsFailed
        ? Kirigami.Theme.negativeTextColor
        : (footerOverArtwork ? "white" : Kirigami.Theme.disabledTextColor)

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
    readonly property real replyGlowLeft: centeredPill ? pillLoader.x : (framelessBubble ? framelessBubble.contentLeft : bubble.x)
    readonly property real replyGlowTop: centeredPill ? pillLoader.y : (framelessBubble ? framelessBubble.contentTop : bubble.y)
    readonly property real replyGlowRight: centeredPill ? pillLoader.x + pillLoader.width : (framelessBubble ? framelessBubble.contentRight : bubble.x + bubble.width)
    readonly property real replyGlowBottom: centeredPill ? pillLoader.y + pillLoader.height : (framelessBubble ? framelessBubble.contentBottom : bubble.y + bubble.height)

    // Bounds of the row's visual body — the bubble, or the sticker slot on
    // frameless rows. Shared by the selection check circle and the hover reply
    // button, which both sit in the free space beside it.
    readonly property real visualX: centeredPill ? pillLoader.x : (framelessBubble ? framelessBubble.slotX : bubble.x)
    readonly property real visualY: centeredPill ? pillLoader.y : (framelessBubble ? framelessBubble.slotY : bubble.y)
    readonly property real visualWidth: centeredPill ? pillLoader.width : (framelessBubble ? framelessBubble.slotWidth : bubble.width)
    readonly property real visualHeight: centeredPill ? pillLoader.height : (framelessBubble ? framelessBubble.slotHeight : bubble.height)

    readonly property bool hasReactions: reactions !== undefined && reactions !== null && reactions.length > 0
    // The reaction chip row sits in its own band below the bubble; reserve its
    // full height plus the gap above it so it never clips into the next row.
    readonly property real reactionRowReserve: hasReactions
        ? Math.round(reactionRowLoader.height + Kirigami.Units.smallSpacing / 2)
        : 0

    width: listWidth
    height: (centeredPill
        ? pillLoader.y + pillLoader.height
        : framelessBubble
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
        id: rowPointerArea

        anchors.fill: parent
        acceptedButtons: Qt.RightButton
        // Covering the row with a hover-enabled MouseArea takes hover away from
        // everything under it, handlers included, which is exactly why the
        // cursor is resolved here rather than by the things being pointed at.
        // A card is different: it holds real buttons and fields that have to
        // light up under the pointer, so the mask cuts a hole for it. CardBubble
        // hands the row's context menu back for right-clicks in that hole.
        // The parameter and return types are declared because Qt looks the mask
        // up by the exact signature `contains(QPointF)`; an untyped QML function
        // is registered as taking a QVariant and is silently ignored.
        containmentMask: QtObject {
            function contains(point: point): bool {
                const card = cardLoader.item
                if (!card) {
                    return true
                }
                const p = rowPointerArea.mapToItem(card, point.x, point.y)
                return p.x < 0 || p.y < 0 || p.x > card.width || p.y > card.height
            }
        }
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
            // The frameless kinds live outside mediaSlot; a tappable video
            // note under an arrow cursor read as inert.
            if (root.framelessBubble && root.isVideoNote) {
                const f = mapToItem(root.framelessBubble, mouseX, mouseY)
                if (f.x >= root.framelessBubble.slotX && f.y >= root.framelessBubble.slotY
                        && f.x <= root.framelessBubble.slotX + root.framelessBubble.slotWidth
                        && f.y <= root.framelessBubble.slotY + root.framelessBubble.slotHeight) {
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

    // Everything that sits on top of the row: the multi-select chrome, the
    // jump-to-reply glow, the hover reply button. One Loader for all three
    // rather than one each, for the reason the media slot has one: an inactive
    // Loader holding an inline component is two objects on every delegate in
    // the chat, and none of these three is showing on almost any row at any
    // moment. See RowOverlays.qml.
    Loader {
        id: rowOverlaysLoader

        anchors.fill: parent
        z: 7
        active: root.selectionModeActive
                || root.replyGlowOpacity > 0
                || (root.hoverLatched && root.canReply && !root.pooled)

        readonly property url overlaySource: active ? Qt.resolvedUrl("RowOverlays.qml") : ""
        onOverlaySourceChanged: setSource(overlaySource, { row: root })
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
        // FramelessBubble instead; pill rows build theirs in pillLoader. This
        // is a plain `visible`, so it takes the whole content column with it:
        // nothing that either of those rows still needs may live inside this
        // rectangle.
        visible: !root.frameless && !root.centeredPill

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
                id: forwardedLoader

                active: root.showForwardedHeader
                x: root.innerPadding
                y: root.innerPadding
                width: root.textRegionWidth
                height: active && item ? item.implicitHeight : 0

                sourceComponent: Label {
                    width: parent.width
                    text: Whatevr.I18n.i18nc("@label forwarded message header", "Forwarded")
                    font.italic: true
                    font.pointSize: Kirigami.Theme.smallFont.pointSize
                    color: Kirigami.Theme.highlightColor
                    elide: Text.ElideRight
                    maximumLineCount: 1
                }
            }

            Loader {
                id: replyPreviewLoader

                active: root.hasReplyPreview
                x: root.innerPadding
                y: root.innerPadding + root.forwardedHeaderHeight
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
                // A link preview is the one card that shares its bubble with
                // body text, and a card narrower than the paragraph under it
                // reads as a mistake. It takes the bubble's content width,
                // which is what the text got. Every other card has its bubble
                // to itself, where the two are the same number anyway.
                width: root.isLinkPreview
                    ? root.contentBlockWidth
                    : (root.isAttachmentBlock ? root.attachmentBlockWidth : root.imageDisplayWidth)
                height: visible ? (root.isAttachmentBlock ? root.attachmentBlockHeight : root.imageDisplayHeight) : 0
                clip: !root.isAttachmentBlock

                // One Loader for every media kind that fills the slot, not one
                // per kind. An inactive Loader with an inline sourceComponent is
                // two objects (the Loader, and the component it will probably
                // never build) on every delegate in the chat, so a Loader each
                // for pictures, players, audio rows and documents taxed every
                // plain text message four times over for kinds it is not. The
                // kinds are mutually exclusive by construction, so one Loader
                // choosing a URL does the same job for a quarter of the cost.
                //
                // Sourced by URL rather than by component precisely because a
                // URL costs nothing when unused; `row` is the one initial
                // property each of them needs, since everything else they render
                // they bind through that back-reference.
                Loader {
                    anchors.fill: parent
                    active: mediaSlot.visible && mediaSource != ""

                    readonly property url mediaSource: root.isImage
                        ? Qt.resolvedUrl("ImageBubble.qml")
                        : root.isPlayableVideo ? Qt.resolvedUrl("VideoBubble.qml")
                        : root.isVoice ? Qt.resolvedUrl("VoiceBubble.qml")
                        : root.isAudioFile ? Qt.resolvedUrl("AudioFileBubble.qml")
                        : root.isDocument ? Qt.resolvedUrl("DocumentBubble.qml")
                        : ""

                    // Only the change handler, with no Component.onCompleted
                    // beside it. The binding above is evaluated during
                    // completion, so a row that has media reaches this once on
                    // the way up and once per kind change afterwards, and a row
                    // that has none never reaches it at all. Loading from both
                    // places instead built the bubble twice: the second
                    // setSource orphans the first rather than unwinding it in
                    // the same turn, and for a moment the row held two players.
                    onMediaSourceChanged: setSource(mediaSource, { row: root })
                }

                // Dark scrim behind the time and ticks overlaid on media that
                // fills its bubble, video as much as photo: the same reading
                // aid in the same place, rather than a gradient on one kind and
                // a black pill on the other. Declared after the media loaders
                // so it sits over whichever of them built something, and a
                // uniform radius is fine because the top corners are in the
                // transparent part of the gradient.
                Loader {
                    anchors.fill: parent
                    active: mediaSlot.visible && root.footerOverPicture

                    sourceComponent: Rectangle {
                        anchors.left: parent.left
                        anchors.right: parent.right
                        anchors.bottom: parent.bottom
                        // Tall enough to seat the time and ticks and no taller:
                        // the footer sits footerInset off the bottom and is
                        // tntHeight tall, so this is that line plus the same
                        // margin again above it. A fixed 2.4 gridUnits ran a
                        // visible grey a third of the way up the picture, where
                        // there is nothing to make legible.
                        height: Math.min(parent.height, root.tntHeight + root.footerInset * 2)
                        // A mosaic keeps its own corners: the bubble's media
                        // radii are the edge-to-edge ones and are zero for a
                        // card, which would square off the bottom of an album.
                        radius: root.isAlbum
                            ? root.bubbleCornerRadius
                            : Math.max(root.mediaBottomLeftRadius, root.mediaBottomRightRadius)
                        // Faded rather than unloaded: `active` above stays keyed
                        // on the kind, so a decode (or a re-decode after a fling
                        // settles) never tears this down and builds it again.
                        opacity: root.footerOverArtwork ? 1 : 0

                        Behavior on opacity {
                            NumberAnimation {
                                duration: Kirigami.Units.shortDuration
                                easing.type: Easing.OutCubic
                            }
                        }
                        // Shaped rather than a straight ramp, and that is the
                        // difference between a reading aid and a smudge. A
                        // linear fade to the peak starts darkening at the very
                        // top of the strip, so a grey wash sits on the picture
                        // above the line it is meant to serve. Holding it at
                        // nothing until the footer's own top edge, then bending
                        // the ramp so most of the darkening lands in the last
                        // third, keeps it under the text and off the photograph.
                        gradient: Gradient {
                            GradientStop { position: 0.0; color: "transparent" }
                            GradientStop { position: root.footerScrimOnset; color: "transparent" }
                            GradientStop {
                                position: root.footerScrimOnset + (1 - root.footerScrimOnset) * 0.55
                                color: Qt.rgba(0, 0, 0, root.footerScrimPeak * 0.28)
                            }
                            GradientStop { position: 1.0; color: Qt.rgba(0, 0, 0, root.footerScrimPeak) }
                        }
                    }
                }


                // One Loader for the whole card family, not one per kind: an
                // inactive Loader costs two objects on every row in the
                // timeline, so a Loader per kind would tax every plain text
                // message for kinds it is not. CardBubble picks which card.
                Loader {
                    id: cardLoader

                    // A card is sized by its own content, so it is given a width
                    // and asked for a height rather than filled. Anchoring it
                    // would make its implicitHeight depend on the height the row
                    // derived from it, which is a binding loop.
                    width: mediaSlot.width
                    active: mediaSlot.visible && root.isCardBlock
                    sourceComponent: CardBubble {
                        row: root
                    }

                    onItemChanged: {
                        root.cardBlockHeight = Qt.binding(() =>
                            cardLoader.item ? cardLoader.item.implicitHeight : Kirigami.Units.gridUnit * 12)
                        // The width a card would rather have. Read from the
                        // dispatcher rather than the card so the row does not
                        // have to know which kinds publish one; 0 means fill.
                        root.cardBlockWidth = Qt.binding(() =>
                            cardLoader.item ? cardLoader.item.preferredWidth : 0)
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
                    if (root.footerOverPicture) {
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
                    if (root.footerOverPicture) {
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

                // Keep / pin / star / edit / deleted / linked marks. Most messages
                // carry none, so the icons (and the anchor chain that used to
                // thread them together) are built only when at least one
                // applies; the Row drops the ones that do not, so ordering
                // stays automatic.
                Loader {
                    active: root.showKeepMark || root.showPinMark || root.showStarMark || root.showEditMark || root.showDeletedMark || root.showLinkedMark
                    anchors.right: timeLabel.left
                    anchors.rightMargin: root.tntSpacing
                    anchors.verticalCenter: parent.verticalCenter

                    sourceComponent: Row {
                        spacing: root.tntSpacing

                        Kirigami.Icon {
                            visible: root.showKeepMark
                            source: "bookmarks-bookmarked-symbolic"
                            width: root.keepMarkSize
                            height: root.keepMarkSize
                            color: root.footerTextColor
                            isMask: true
                        }

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

                        Kirigami.Icon {
                            visible: root.showDeletedMark
                            source: "edit-delete-remove-symbolic"
                            width: root.deletedMarkSize
                            height: root.deletedMarkSize
                            color: root.footerTextColor
                            isMask: true
                        }

                        Kirigami.Icon {
                            visible: root.showLinkedMark
                            source: "computer-symbolic"
                            width: root.linkedMarkSize
                            height: root.linkedMarkSize
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

    // The centered pill: a row that is not somebody talking. It sizes itself
    // (no width is set here), so it is exactly as wide as what it says, and it
    // is centered on the whole row rather than on the bubble column because it
    // belongs to neither side.
    Loader {
        id: pillLoader

        active: root.centeredPill
        x: Math.round((root.width - width) / 2)
        y: root.messageBaseY

        // Which pill this is comes from a URL rather than from a pair of
        // inline Components. A Component is an object on every delegate that
        // declares it, instantiated or not, so a second one would charge every
        // plain text row for a pill it will never draw (MIGRATION.md, DN9).
        // setSource carries `row` as an initial property, which is what a
        // required property needs, and re-runs on reuse when the kind changes.
        readonly property url pillSource: root.isSystemEvent
            ? Qt.resolvedUrl("SystemPill.qml")
            : Qt.resolvedUrl("CallLogPill.qml")

        onPillSourceChanged: setSource(pillSource, { row: root })
        Component.onCompleted: setSource(pillSource, { row: root })
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
        // The 8gu floor gives chips room under a narrow bubble, but never
        // more room than the row itself has: an uncapped floor pushed chips
        // past the bubble edge in a narrow window.
        width: Math.max(root.replyGlowRight - root.replyGlowLeft,
                        Math.min(Kirigami.Units.gridUnit * 8,
                                 Math.max(0, root.width - root.outerMargin * 2)))
        x: Math.round(Math.max(root.outerMargin,
                               root.isOutgoing ? root.replyGlowRight - width : root.replyGlowLeft))
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
        // The `!centeredPill` half matches senderHeaderHeight above: a pill
        // reserves no room for a header, so drawing one would put a name and an
        // avatar on top of the row rather than above it.
        active: root.showSenderHeader && !root.centeredPill

        sourceComponent: Item {
            readonly property real labelImplicitHeight: senderHeader.implicitHeight

            anchors.fill: parent

            Label {
                id: senderHeader

                visible: root.senderName.length > 0
                // The bubble's own content edge, the same one the body, the
                // cards and the reply quote all start at. Half the padding put
                // the name a few pixels left of every glyph under it, and the
                // width ran to the row's edge rather than the bubble's, so a
                // long name overhung the plate instead of eliding inside it.
                x: bubble.x + root.innerPadding
                y: root.dateSeparatorHeight + root.unreadSeparatorHeight + Math.max(0, (root.senderHeaderHeight - height) / 2)
                width: Math.max(0, bubble.x + bubble.width - root.innerPadding - x)
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
