pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// The frameless rendering of a message row — stickers and 1-3 emoji "jumbo"
// messages — which draws no bubble: a bare sticker slot, an optional quoted
// reply above it, and a floating time/ticks pill below.
//
// Split out of ChatBubble for DN9. Inline, this subtree was built for every
// single message row and then merely hidden, including a jumbo-emoji Text bound
// to the message body, which laid that body out a second time on rows that
// never displayed it. Being a real type rather than an inline Component also
// makes ChatBubble's geometry reads statically typed, so qmlcachegen compiles
// them instead of falling back to the interpreter.
//
// `row` is the owning ChatBubble: one typed back-reference, rather than
// plumbing the ~35 message properties this subtree reads through the Loader.
Item {
    id: framelessRoot

    required property ChatBubble row

    anchors.fill: parent

    // Geometry ChatBubble reads back: the union of everything drawn here (the
    // jump glow and the reaction band bracket it), the sticker slot alone (the
    // selection check and hover reply button sit beside it), and the bottom
    // edge (the row's own height).
    readonly property real contentLeft: Math.min(stickerSlot.x,
                                                 stickerReplyPreviewLoader.active ? stickerReplyPreviewLoader.x : stickerSlot.x,
                                                 stickerFooter.x)
    readonly property real contentTop: Math.min(stickerSlot.y,
                                                stickerReplyPreviewLoader.active ? stickerReplyPreviewLoader.y : stickerSlot.y,
                                                stickerFooter.y)
    readonly property real contentRight: Math.max(stickerSlot.x + stickerSlot.width,
                                                  stickerReplyPreviewLoader.active ? stickerReplyPreviewLoader.x + stickerReplyPreviewLoader.width : stickerSlot.x + stickerSlot.width,
                                                  stickerFooter.x + stickerFooter.width)
    readonly property real contentBottom: Math.max(stickerSlot.y + stickerSlot.height,
                                                   stickerReplyPreviewLoader.active ? stickerReplyPreviewLoader.y + stickerReplyPreviewLoader.height : stickerSlot.y + stickerSlot.height,
                                                   stickerFooter.y + stickerFooter.height)
    readonly property real bottomEdge: stickerFooter.y + stickerFooter.height
    readonly property real slotX: stickerSlot.x
    readonly property real slotY: stickerSlot.y
    readonly property real slotWidth: stickerSlot.width
    readonly property real slotHeight: stickerSlot.height

    Loader {
        id: stickerReplyPreviewLoader

        active: framelessRoot.row.hasReplyPreview
        x: framelessRoot.row.isOutgoing
           ? framelessRoot.row.width - width - framelessRoot.row.outerMargin
           : framelessRoot.row.outerMargin + framelessRoot.row.senderGutterWidth
        y: framelessRoot.row.messageBaseY
        // Typed handle on the loaded quote: Loader.item is only QObject, which
        // would make the width binding below untyped and uncompilable.
        readonly property ReplyPreview preview: item as ReplyPreview

        width: Math.min(Math.max(0, framelessRoot.row.width - framelessRoot.row.outerMargin * 2 - framelessRoot.row.senderGutterWidth),
                        Math.max(Kirigami.Units.gridUnit * 5, preview ? preview.naturalContentWidth : 0))

        sourceComponent: ReplyPreview {
            senderName: framelessRoot.row.replyToSenderName
            body: framelessRoot.row.replyToText
            mediaKind: framelessRoot.row.replyToMediaKind
            mediaMimeType: framelessRoot.row.replyToMediaMimeType
            targetMessageId: framelessRoot.row.replyToMessageId
            outgoing: framelessRoot.row.replyToIsOutgoing
            fillColor: Qt.alpha(Kirigami.Theme.backgroundColor, 0.78)
            borderColor: Qt.alpha(Kirigami.Theme.textColor, 0.08)
            onActivated: messageId => framelessRoot.row.replyPreviewActivated(messageId)
        }
    }

    Item {
        id: stickerSlot

        x: framelessRoot.row.isOutgoing
           ? framelessRoot.row.width - width - framelessRoot.row.outerMargin
           : framelessRoot.row.outerMargin + framelessRoot.row.senderGutterWidth
        y: framelessRoot.row.messageBaseY + (stickerReplyPreviewLoader.active ? stickerReplyPreviewLoader.height + Kirigami.Units.smallSpacing : 0)
        // A video note is square by definition, at a fixed diameter: the round
        // recording carries no useful aspect metadata to size it from.
        width: framelessRoot.row.isVideoNote
            ? framelessRoot.row.videoNoteDiameter
            : (framelessRoot.row.isJumboEmoji ? jumboEmoji.implicitWidth : framelessRoot.row.stickerDisplayWidth)
        height: framelessRoot.row.isVideoNote
            ? framelessRoot.row.videoNoteDiameter
            : (framelessRoot.row.isJumboEmoji ? jumboEmoji.implicitHeight : framelessRoot.row.stickerDisplayHeight)

        // The circle, its progress ring and its mute toggle: the same component
        // the rectangular video bubbles use, which reads isVideoNote off the row
        // and presents itself round.
        Loader {
            anchors.fill: parent
            active: framelessRoot.row.isVideoNote

            sourceComponent: VideoBubble {
                row: framelessRoot.row
            }
        }

        Text {
            id: jumboEmoji

            anchors.centerIn: parent
            visible: framelessRoot.row.isJumboEmoji
            text: framelessRoot.row.body
            font.family: Whatevr.ProtocolController.emojiModel.emojiFontFamily
            font.pixelSize: framelessRoot.row.jumboEmojiPixelSize
            horizontalAlignment: Text.AlignHCenter
            textFormat: Text.PlainText
        }

        // The sticker renderers — an AnimatedImage and a custom Lottie paint
        // item are the heaviest — are built only for sticker messages. Text,
        // image and jumbo-emoji delegates never instantiate them. jumboEmoji
        // stays outside so the slot can size to it.
        Loader {
            anchors.fill: parent
            active: framelessRoot.row.isSticker
            sourceComponent: Component {
              Item {
                anchors.fill: parent

                // Whether the actual sticker (whichever renderer applies) is on
                // screen. Drives the thumbnail placeholder during a fast fling.
                readonly property bool stickerContentReady: framelessRoot.row.isLottieSticker
                    ? lottieSticker.status === Whatevr.RlottieSticker.Ready
                    : framelessRoot.row.isAnimatedSticker
                        ? animatedSticker.status === AnimatedImage.Ready
                        : staticSticker.status === Image.Ready

        // While the sticker is still decoding it draws nothing, which over the
        // wallpaper doodle reads as an empty hole. A faint plate fills the slot
        // until the real sticker (or its thumbnail) is on screen.
        Rectangle {
            anchors.fill: parent
            radius: Kirigami.Units.cornerRadius
            visible: !parent.stickerContentReady
                     && !(framelessRoot.row.isSticker && framelessRoot.row.hasThumbnailImage && stickerThumb.status === Image.Ready)
            color: Qt.alpha(Kirigami.Theme.textColor, 0.06)
        }

        Image {
            id: stickerThumb

            anchors.fill: parent
            // Hold the thumbnail until the real sticker is showing, so a fling
            // (which defers the full sticker decode) still has a placeholder.
            visible: framelessRoot.row.isSticker && framelessRoot.row.hasThumbnailImage && !parent.stickerContentReady
            opacity: status === Image.Ready ? 0.7 : 0
            source: framelessRoot.row.mediaSourceActive && stickerSlot.visible && visible ? Qt.resolvedUrl("file://" + framelessRoot.row.mediaThumbnailLocalPath) : ""
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            cache: true
            smooth: true
            sourceSize.width: framelessRoot.row.stickerDecodeCap
            sourceSize.height: framelessRoot.row.stickerDecodeCap
        }

        Image {
            id: staticSticker

            // Latched readiness, set from onStatusChanged rather than read off
            // `status` inside the source binding (which would make source depend
            // on its own load state and loop). Reset when the underlying file
            // changes on delegate reuse.
            property bool everDecoded: false
            readonly property string targetPath: framelessRoot.row.mediaLocalPath
            onTargetPathChanged: everDecoded = false
            onStatusChanged: if (status === Image.Ready) everDecoded = true

            anchors.fill: parent
            visible: framelessRoot.row.isRenderableStickerImage && framelessRoot.row.hasLocalSticker && !framelessRoot.row.isAnimatedSticker
            opacity: status === Image.Ready ? 1 : 0
            // Defer the decode while flinging unless it is already decoded.
            source: framelessRoot.row.mediaSourceActive && stickerSlot.visible && visible
                    && (!framelessRoot.row.fastFlicking || everDecoded)
                    ? Qt.resolvedUrl("file://" + framelessRoot.row.mediaLocalPath) : ""
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            cache: true
            smooth: true
            sourceSize.width: framelessRoot.row.stickerDecodeCap
            sourceSize.height: framelessRoot.row.stickerDecodeCap
        }

        AnimatedImage {
            id: animatedSticker

            // Same latch as staticSticker: keeps `status` out of the source
            // binding so it cannot loop on its own load state.
            property bool everDecoded: false
            readonly property string targetPath: framelessRoot.row.mediaLocalPath
            onTargetPathChanged: everDecoded = false
            onStatusChanged: if (status === AnimatedImage.Ready) everDecoded = true

            anchors.fill: parent
            visible: framelessRoot.row.isRenderableStickerImage && framelessRoot.row.hasLocalSticker && framelessRoot.row.isAnimatedSticker
            playing: framelessRoot.row.animationActive && visible && status === AnimatedImage.Ready
            // Defer the (heavy, multi-frame) decode while flinging unless ready.
            source: framelessRoot.row.mediaSourceActive && stickerSlot.visible && visible
                    && (!framelessRoot.row.fastFlicking || everDecoded)
                    ? Qt.resolvedUrl("file://" + framelessRoot.row.mediaLocalPath) : ""
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            cache: false
            smooth: true
            sourceSize.width: framelessRoot.row.stickerDecodeCap
            sourceSize.height: framelessRoot.row.stickerDecodeCap
        }

        Whatevr.RlottieSticker {
            id: lottieSticker

            // Same latch as staticSticker: keeps `status` out of the source
            // binding so it cannot loop on its own load state.
            property bool everDecoded: false
            readonly property string targetPath: framelessRoot.row.mediaLocalPath
            onTargetPathChanged: everDecoded = false
            onStatusChanged: if (status === Whatevr.RlottieSticker.Ready) everDecoded = true

            anchors.fill: parent
            visible: framelessRoot.row.isLottieSticker && framelessRoot.row.hasLocalSticker
            // Defer the JSON load + first rasterisation while flinging unless ready.
            source: framelessRoot.row.mediaSourceActive && stickerSlot.visible && visible
                    && (!framelessRoot.row.fastFlicking || everDecoded)
                    ? Qt.resolvedUrl("file://" + framelessRoot.row.mediaLocalPath) : ""
            playing: framelessRoot.row.animationActive && visible
            // Rasterise at device resolution (capped) rather than a fixed 2.5×;
            // the sticker is ~144 px so 2× is already crisp and halves the
            // per-frame raster cost on 1× displays.
            renderScale: Math.max(1, Math.min(2, Screen.devicePixelRatio))
        }

        Item {
            id: stickerOverlay

            anchors.fill: parent
            visible: framelessRoot.row.isSticker
                     && (!framelessRoot.row.hasLocalSticker
                     || framelessRoot.row.mediaDownloading
                     || stickerThumb.status === Image.Loading
                     || staticSticker.status === Image.Loading
                     || animatedSticker.status === AnimatedImage.Loading
                     || lottieSticker.status === Whatevr.RlottieSticker.Loading
                     || staticSticker.status === Image.Error
                     || animatedSticker.status === AnimatedImage.Error
                     || lottieSticker.status === Whatevr.RlottieSticker.Error
                     || framelessRoot.row.mediaDownloadError.length > 0)

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
                    visible: (framelessRoot.row.mediaDownloading && framelessRoot.row.mediaDownloadProgress < 0)
                             || (!framelessRoot.row.mediaDownloading && !framelessRoot.row.hasLocalSticker && framelessRoot.row.hasThumbnailImage && stickerThumb.status === Image.Loading)
                             || (framelessRoot.row.hasLocalSticker && (staticSticker.status === Image.Loading
                                                          || animatedSticker.status === AnimatedImage.Loading
                                                          || lottieSticker.status === Whatevr.RlottieSticker.Loading))
                    running: visible
                    implicitWidth: Kirigami.Units.gridUnit * 1.75
                    implicitHeight: implicitWidth
                }

                ProgressCircle {
                    anchors.horizontalCenter: parent.horizontalCenter
                    visible: framelessRoot.row.mediaDownloading && framelessRoot.row.mediaDownloadProgress >= 0
                    progress: Math.max(0, framelessRoot.row.mediaDownloadProgress)
                    width: Kirigami.Units.gridUnit * 1.75
                    height: width
                }

                Button {
                    anchors.horizontalCenter: parent.horizontalCenter
                    visible: !framelessRoot.row.hasLocalSticker && !framelessRoot.row.mediaDownloading
                    flat: true
                    icon.name: "folder-download-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button", "Load sticker")
                    enabled: framelessRoot.row.messageId.length > 0
                    onClicked: {
                        Whatevr.ProtocolController.downloadMessageMedia(framelessRoot.row.messageId)
                        framelessRoot.row.conversationFocusRequested()
                    }
                }

                Label {
                    anchors.horizontalCenter: parent.horizontalCenter
                    width: parent.width
                    visible: (staticSticker.status === Image.Error
                              || animatedSticker.status === AnimatedImage.Error
                              || lottieSticker.status === Whatevr.RlottieSticker.Error) && framelessRoot.row.hasLocalSticker
                    text: Whatevr.I18n.i18nc("@info", "Sticker could not be displayed")
                    color: Kirigami.Theme.negativeTextColor
                    font.pointSize: Kirigami.Theme.smallFont.pointSize
                    wrapMode: Text.Wrap
                    horizontalAlignment: Text.AlignHCenter
                }

                Label {
                    anchors.horizontalCenter: parent.horizontalCenter
                    width: parent.width
                    visible: !framelessRoot.row.mediaDownloading && framelessRoot.row.mediaDownloadError.length > 0
                    text: framelessRoot.row.mediaDownloadError
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

        x: Math.round(Math.max(framelessRoot.row.outerMargin + framelessRoot.row.senderGutterWidth,
                               Math.min(framelessRoot.row.width - framelessRoot.row.outerMargin - width,
                                        stickerSlot.x + stickerSlot.width - width)))
        y: Math.round(stickerSlot.y + stickerSlot.height + Kirigami.Units.smallSpacing / 2)
        width: framelessRoot.row.tntWidth + framelessRoot.row.framelessFooterHPadding * 2
        height: framelessRoot.row.tntHeight + framelessRoot.row.framelessFooterVPadding * 2
        radius: Kirigami.Units.cornerRadius
        // Opaque so the wallpaper doodle never shows through the time/ticks pill
        // on frameless (sticker / jumbo-emoji) rows. Mirrors the bubble fill: the
        // old translucent tints composited onto the theme background.
        color: framelessRoot.row.isOutgoing
               ? Qt.tint(Kirigami.Theme.backgroundColor, Qt.alpha(Kirigami.Theme.highlightColor, 0.30))
               : Kirigami.Theme.backgroundColor
        border.color: Qt.alpha(Kirigami.Theme.textColor, framelessRoot.row.isOutgoing ? 0.05 : 0.12)

        // The whole footer only exists on frameless rows now, so the Loader
        // that used to defer its contents has nothing left to defer.
        Item {
            id: stickerFooterContent

            x: framelessRoot.row.framelessFooterHPadding
            y: Math.round((parent.height - height) / 2)
            width: framelessRoot.row.tntWidth
            height: framelessRoot.row.tntHeight

            Item {
                id: stickerStatusArea

                anchors.right: parent.right
                anchors.verticalCenter: parent.verticalCenter
                visible: framelessRoot.row.showStatusIcon
                width: framelessRoot.row.statusAreaWidth
                height: framelessRoot.row.statusIconSize

                Kirigami.Icon {
                    id: stickerSingleIcon
                    x: Math.round((parent.width - width) / 2)
                    anchors.verticalCenter: parent.verticalCenter
                    visible: !framelessRoot.row.statusIsDoubleTick
                    source: framelessRoot.row.statusSingleIcon
                    width: framelessRoot.row.statusIconSize
                    height: framelessRoot.row.statusIconSize
                    color: framelessRoot.row.statusSingleColor
                    isMask: true
                }

                Item {
                    anchors.fill: parent
                    visible: framelessRoot.row.statusIsDoubleTick

                    Kirigami.Icon {
                        id: firstStickerTick
                        x: 0
                        anchors.verticalCenter: parent.verticalCenter
                        source: framelessRoot.row.tickSource
                        width: framelessRoot.row.statusIconSize
                        height: framelessRoot.row.statusIconSize
                        color: framelessRoot.row.statusTickColor
                        isMask: true
                    }

                    Kirigami.Icon {
                        id: secondStickerTick
                        x: framelessRoot.row.statusDoubleTickOffset
                        anchors.verticalCenter: parent.verticalCenter
                        source: framelessRoot.row.tickSource
                        width: framelessRoot.row.statusIconSize
                        height: framelessRoot.row.statusIconSize
                        color: framelessRoot.row.statusTickColor
                        isMask: true
                    }
                }
            }

            Label {
                id: stickerTimeLabel
                anchors.right: stickerStatusArea.visible ? stickerStatusArea.left : parent.right
                anchors.rightMargin: stickerStatusArea.visible ? framelessRoot.row.tntSpacing : 0
                anchors.verticalCenter: parent.verticalCenter
                text: framelessRoot.row.timeText
                color: Kirigami.Theme.disabledTextColor
                width: framelessRoot.row.footerTimeWidth
                horizontalAlignment: Text.AlignRight
                font.pointSize: framelessRoot.row.footerTimePointSize
            }

            Kirigami.Icon {
                id: stickerEditMark
                visible: framelessRoot.row.showEditMark
                source: "document-edit-symbolic"
                anchors.right: stickerTimeLabel.left
                anchors.rightMargin: framelessRoot.row.tntSpacing
                anchors.verticalCenter: parent.verticalCenter
                width: framelessRoot.row.editMarkSize
                height: framelessRoot.row.editMarkSize
                color: framelessRoot.row.footerTextColor
                isMask: true
            }

            Kirigami.Icon {
                id: stickerStarMark
                visible: framelessRoot.row.showStarMark
                source: "starred-symbolic"
                anchors.right: stickerEditMark.visible ? stickerEditMark.left : stickerTimeLabel.left
                anchors.rightMargin: framelessRoot.row.tntSpacing
                anchors.verticalCenter: parent.verticalCenter
                width: framelessRoot.row.starMarkSize
                height: framelessRoot.row.starMarkSize
                color: framelessRoot.row.footerTextColor
                isMask: true
            }

            Kirigami.Icon {
                visible: framelessRoot.row.showPinMark
                source: "pin-symbolic"
                anchors.right: stickerStarMark.visible ? stickerStarMark.left
                                                       : (stickerEditMark.visible ? stickerEditMark.left : stickerTimeLabel.left)
                anchors.rightMargin: framelessRoot.row.tntSpacing
                anchors.verticalCenter: parent.verticalCenter
                width: framelessRoot.row.pinMarkSize
                height: framelessRoot.row.pinMarkSize
                color: framelessRoot.row.footerTextColor
                isMask: true
            }
        }
    }}
