pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

import "MediaFormat.js" as MediaFormat

// Full-screen media viewer: a photo, or a video with transport controls.
//
// It is the same shell as the profile-picture lightbox, grown a player. Video
// goes through VideoSurface and takes the exclusive playback lane like anything
// else: opening it stops whatever was playing in the conversation behind, which
// is the point rather than something to work around. It used to run on a decoder
// reserved for it, so a clip opened full screen played twice over, from two
// positions.
QQC2.Popup {
    id: root

    /// Local file for a downloaded item, or empty while streaming.
    property string localPath: ""
    /// Streaming URL, used when the file is not complete yet.
    property url streamUrl
    property string activeStreamId: ""
    property string messageId: ""
    /// The message's media kind: image, video, gif, video_note.
    property string kind: "image"
    property real durationSecs: 0
    /// Where to open the clip, in seconds: the second the inline bubble had
    /// reached when it handed over. Read at load, so it is set before the
    /// surface engages.
    property real startAt: 0
    /// Suggested name when saving, for kinds that carry one, and the
    /// message's own timestamp so a save is dated when it was sent rather
    /// than when the dialog opened.
    property string fileName: ""
    property real timestampUnix: 0
    property string returnMessageId: ""
    property real returnPosition: 0
    property bool returnPlaying: false

    readonly property bool isVideo: kind === "video" || kind === "gif" || kind === "video_note"
    readonly property bool isGif: kind === "gif"
    readonly property bool isVideoNote: kind === "video_note"
    /// A looping kind has no meaningful timeline: it is a moving picture.
    readonly property bool hasTimeline: isVideo && !isGif
    readonly property url mediaSource: localPath.length > 0
        ? Whatevr.ProtocolController.localFileUrl(localPath)
        : streamUrl
    readonly property bool hasFile: localPath.length > 0

    /// The still the bubble left behind for this clip, revision-keyed so a
    /// fresh capture is not answered from the image cache. Empty when this
    /// message has never been decoded anywhere.
    property int stillRevision: 0
    readonly property url handoffStill: isVideo && messageId.length > 0 && stillRevision > 0
        ? "image://videoframe/" + encodeURIComponent(messageId) + "?rev=" + stillRevision
        : ""

    /// Playback was asked for and there is still nothing on screen. Split from
    /// the failure below because the first second of it is entirely normal.
    readonly property bool buffering: isVideo && surface.playbackWanted && !surface.hasFrame
    /// Playback genuinely failed: either the decoder said so, or the stream did.
    /// Not a stopwatch. A `media.stream` source reads through the daemon's range
    /// server, where a scrub into bytes that have not arrived blocks until they
    /// do, so "no picture yet" was routinely several seconds of a perfectly
    /// healthy video that this dialog then declared unplayable.
    property bool streamFailed: false
    readonly property bool playbackFailed: streamFailed || surface.failed || stalledOut
    /// The backstop for a decoder that neither errors nor ever draws: generous,
    /// and held off entirely while the engine reports it is working on something.
    property bool stalledOut: false

    /// Whether the chrome is on screen. A full-screen player that permanently
    /// covers a strip of the video with its own transport bar is not
    /// full-screen; it comes back on any pointer movement or key.
    property bool chromeVisible: true

    function wakeChrome() {
        chromeVisible = true
        chromeTimer.restart()
    }

    signal forwardRequested(string messageId)

    function showImage(path, id, name, sentAtUnix) {
        kind = "image"
        localPath = path
        streamUrl = ""
        messageId = id ?? ""
        fileName = name ?? ""
        timestampUnix = sentAtUnix ?? 0
        startAt = 0
        stillRevision = 0
        open()
    }

    function showVideo(id, path, url, streamId, mediaKind, duration, at, name, sentAtUnix) {
        kind = mediaKind && mediaKind.length > 0 ? mediaKind : "video"
        messageId = id
        localPath = path
        streamUrl = url
        activeStreamId = streamId ?? ""
        durationSecs = duration
        fileName = name ?? ""
        timestampUnix = sentAtUnix ?? 0
        // Before open(), so the surface engages with it already set: the start
        // position is read once, when the file is loaded. Same for the still
        // the bubble just captured, which stands in until the decoder catches
        // up to that position.
        startAt = at ?? 0
        stillRevision = Whatevr.VideoPlayback.frameRevision(id)
        surface.muted = false
        surface.volume = 100
        surface.playbackWanted = true
        open()
    }

    function formatTime(seconds) {
        return MediaFormat.clockTime(seconds)
    }

    function skip(seconds) {
        let target = Math.max(0, surface.position + seconds)
        if (surface.duration > 0) {
            target = Math.min(target, Math.max(0, surface.duration - 0.5))
        }
        surface.seek(target)
    }

    function togglePlayback() {
        surface.playbackWanted = !surface.playbackWanted
    }

    function cycleSpeed() {
        const speeds = [1.0, 1.5, 2.0]
        const index = speeds.indexOf(surface.speed)
        surface.speed = speeds[(index + 1) % speeds.length]
    }

    function saveMedia() {
        if (hasFile) {
            saveRequested(localPath, kind, fileName, timestampUnix)
        }
    }

    signal saveRequested(string localPath, string kind, string fileName, real timestampUnix)

    parent: QQC2.Overlay.overlay
    modal: true
    focus: true
    dim: false
    padding: 0
    x: 0
    y: 0
    width: parent ? parent.width : 0
    height: parent ? parent.height : 0
    z: 10002
    closePolicy: QQC2.Popup.CloseOnEscape

    onAboutToHide: {
        if (isVideo && messageId.length > 0) {
            // While the decoder is still attached: the frame goes back to the
            // bubble with the position, so it comes back showing where the user
            // was rather than the clip's first frame.
            surface.rememberStill()
            returnMessageId = messageId
            returnPosition = surface.handoffPosition()
            // Escaping a video that never started must not make the bubble
            // start it: wanting playback only counts once there was playback,
            // a frame on screen or a position reached.
            returnPlaying = surface.playbackWanted && (surface.hasFrame || surface.position > 0)
        }
    }

    onClosed: {
        // Release the decoder as soon as the viewer goes away. Where the clip
        // got to is already written down by the surface as it lets go, so the
        // bubble underneath comes back showing that position rather than zero.
        streamUrl = ""
        activeStreamId = ""
        localPath = ""
        messageId = ""
        kind = "image"
        fileName = ""
        timestampUnix = 0
        startAt = 0
        stillRevision = 0
        surface.speed = 1.0
        surface.volume = 100
        surface.muted = false
        surface.playbackWanted = true
        streamFailed = false
        stalledOut = false
        const id = returnMessageId
        const at = returnPosition
        const resume = returnPlaying
        returnMessageId = ""
        returnPosition = 0
        returnPlaying = false
        if (id.length > 0) {
            Qt.callLater(function() {
                Whatevr.VideoPlayback.handoffToInline(id, at, resume)
            })
        }
    }

    onOpened: {
        streamFailed = false
        stalledOut = false
        wakeChrome()
    }

    // Restarted from scratch whenever what we are waiting on changes, so a
    // seek or a second file never inherits an older verdict. Only runs while
    // the engine is *not* telling us it is busy, so a slow open, a scrub into
    // unfetched bytes and a stalled buffer all sit under the spinner for as
    // long as they need instead of being called a failure.
    Timer {
        id: failureTimer

        interval: 20000
        running: root.visible && root.buffering && !surface.stalled && !root.playbackFailed
        onTriggered: root.stalledOut = true
    }

    onBufferingChanged: if (!buffering) stalledOut = false

    Connections {
        target: Whatevr.ProtocolController

        function onMediaStreamUpdated(streamId, messageId, state, path, error) {
            // By message id, not stream id: the bubble could consume the
            // one-shot stream id before this viewer opened, stranding it on a
            // URL the daemon had already torn down. The running decoder is
            // switched to the file by the arbiter; this only updates what
            // Save and Open Externally point at.
            if (messageId !== root.messageId)
                return
            root.activeStreamId = ""
            if (state === "local" && path.length > 0) {
                root.localPath = path
                root.streamFailed = false
                root.stalledOut = false
            } else if (state === "failed") {
                surface.playbackWanted = false
                root.streamFailed = true
            }
        }
    }

    Timer {
        id: chromeTimer

        interval: 2500
        // Only a video that is actually running hides its controls. On a
        // paused clip, an image, or a failure, the chrome is the whole point.
        running: root.visible && root.isVideo && surface.hasFrame
                 && surface.playbackWanted && !root.playbackFailed
        onTriggered: root.chromeVisible = false
    }

    background: Rectangle {
        color: Qt.rgba(0, 0, 0, 0.92)
    }

    contentItem: Item {
        id: viewerContent

        // Tapping the backdrop closes; tapping the media itself toggles play,
        // which is what every video player does. The chrome bars carry their
        // own handlers so that a tap on the bar's background (as opposed to one
        // of its buttons) does not fall through to here and shut the viewer.
        TapHandler {
            onTapped: root.close()
        }

        // Any pointer movement anywhere brings the controls back.
        HoverHandler {
            onPointChanged: root.wakeChrome()
        }

        Image {
            id: photo

            // Fit the viewport in both directions, upscaling a small image to
            // something viewable instead of leaving it postage-stamp sized in
            // the middle of a black screen, but no further than 2x: beyond
            // that it is only blur.
            readonly property real fitScale: implicitWidth > 0 && implicitHeight > 0
                ? Math.min((parent.width - Kirigami.Units.gridUnit * 2) / implicitWidth,
                           (parent.height - Kirigami.Units.gridUnit * 2) / implicitHeight,
                           2)
                : 1

            anchors.centerIn: parent
            visible: !root.isVideo
            source: root.isVideo ? "" : root.mediaSource
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            cache: true
            width: implicitWidth * fitScale
            height: implicitHeight * fitScale

            // Takes the tap that would otherwise reach the backdrop handler:
            // a click on the photo itself must not dismiss the viewer.
            TapHandler {
                onTapped: root.wakeChrome()
            }
        }

        // A slow decode showed a black screen with nothing on it, and a
        // missing or corrupt file showed the same black screen forever.
        QQC2.BusyIndicator {
            anchors.centerIn: parent
            visible: !root.isVideo && photo.status === Image.Loading
            running: visible
        }

        ColumnLayout {
            anchors.centerIn: parent
            visible: !root.isVideo && photo.status === Image.Error
            width: Math.min(parent.width - Kirigami.Units.gridUnit * 2, Kirigami.Units.gridUnit * 24)
            spacing: Kirigami.Units.smallSpacing

            Kirigami.Icon {
                Layout.alignment: Qt.AlignHCenter
                source: "dialog-error-symbolic"
                color: "white"
                implicitWidth: Kirigami.Units.iconSizes.large
                implicitHeight: implicitWidth
            }

            QQC2.Label {
                Layout.fillWidth: true
                text: Whatevr.I18n.i18nc("@info", "Image could not be displayed")
                color: "white"
                horizontalAlignment: Text.AlignHCenter
                wrapMode: Text.Wrap
            }

            QQC2.Button {
                Layout.alignment: Qt.AlignHCenter
                visible: root.hasFile
                icon.name: "document-open-symbolic"
                text: Whatevr.I18n.i18nc("@action:button", "Open Externally")
                onClicked: Whatevr.ProtocolController.openLocalFile(root.localPath)
            }
        }

        // A video note stays round full screen: it is a face on a video call,
        // not a clip, and cropping it to a rectangle would cut the head off.
        Item {
            id: videoFrame

            anchors.centerIn: parent
            visible: root.isVideo
            width: root.isVideoNote
                ? Math.min(parent.width, parent.height) - Kirigami.Units.gridUnit * 6
                : parent.width - Kirigami.Units.gridUnit * 2
            height: root.isVideoNote
                ? width
                : parent.height - Kirigami.Units.gridUnit * 6

            // The frame the bubble was showing when it handed over, held until
            // this viewer's own decoder has one. Opening a file and seeking to
            // the handoff position takes long enough to see, and what used to
            // fill it was a black rectangle.
            Image {
                anchors.fill: parent
                visible: root.isVideo && !surface.hasFrame && status === Image.Ready
                source: root.handoffStill
                fillMode: Image.PreserveAspectFit
                asynchronous: true
                cache: false
            }

            VideoSurface {
                id: surface

                anchors.fill: parent
                // Always visible: the round presentation below hides it through
                // the ShaderEffectSource's hideSource, which keeps the decoder
                // attached and the frames flowing into the texture.
                messageId: root.messageId
                source: root.visible && root.isVideo ? root.mediaSource : ""
                claimWithoutSource: root.visible && root.isVideo
                startPosition: root.startAt
                // Engaged for as long as the viewer is open, playing only when
                // it should be running. Pausing here keeps the decoder and so
                // keeps the frame; a bubble does the opposite and goes back to
                // its thumbnail.
                engaged: root.visible && root.isVideo
                playing: root.isVideo && playbackWanted
                muted: false
                loop: root.isGif && Whatevr.Settings.loopGifs

                property bool playbackWanted: true

                // Nothing else can take the exclusive lane while the viewer is
                // up, since it is modal, but if it ever did the viewer must not
                // sit there claiming to play.
                onRevoked: surface.playbackWanted = false
                // A voice note took the audio focus; show the paused state
                // honestly rather than a transport that claims playback.
                onExternallyPaused: surface.playbackWanted = false
                // A clip that ran out is finished: stop wanting playback and
                // leave the last frame on screen. The old seek(0) here ran
                // against a player Qt had already stopped, which parked a
                // pending seek nothing could flush; play on an ended clip
                // restarts from zero on its own. A GIF never gets here when it
                // loops.
                onEndOfFile: surface.playbackWanted = false

                TapHandler {
                    onTapped: {
                        root.togglePlayback()
                        root.wakeChrome()
                    }
                }
            }

            // A paused full-screen video used to show a still frame and nothing
            // else, which is indistinguishable from a video that has stopped
            // working. One obvious way back in; on a finished clip it says so
            // and starts over.
            MediaOverlayButton {
                anchors.centerIn: parent
                visible: root.isVideo && !surface.playbackWanted && !root.playbackFailed
                diameter: Kirigami.Units.gridUnit * 4
                iconName: surface.atEnd ? "media-playlist-repeat-symbolic" : "media-playback-start-symbolic"
                text: surface.atEnd
                    ? Whatevr.I18n.i18nc("@action:button", "Replay")
                    : Whatevr.I18n.i18nc("@action:button", "Play")
                onClicked: {
                    surface.playbackWanted = true
                    root.wakeChrome()
                }
            }

            // Opening a file, claiming a decoder, waiting behind one, or
            // landing a seek. The last frame stays on screen during a seek,
            // so without the indicator a slow one would look ignored.
            QQC2.BusyIndicator {
                anchors.centerIn: parent
                visible: (root.buffering || surface.seeking) && !root.playbackFailed
                running: visible
            }

            // What a black rectangle used to say nothing about. The external
            // opener is the way out that always works.
            ColumnLayout {
                anchors.centerIn: parent
                width: Math.min(parent.width - Kirigami.Units.gridUnit * 2, Kirigami.Units.gridUnit * 24)
                visible: root.playbackFailed
                spacing: Kirigami.Units.largeSpacing

                Kirigami.Icon {
                    Layout.alignment: Qt.AlignHCenter
                    source: "dialog-error-symbolic"
                    implicitWidth: Kirigami.Units.iconSizes.large
                    implicitHeight: Kirigami.Units.iconSizes.large
                    color: "white"
                }

                QQC2.Label {
                    Layout.fillWidth: true
                    text: Whatevr.I18n.i18nc("@info", "This video could not be played")
                    color: "white"
                    horizontalAlignment: Text.AlignHCenter
                    wrapMode: Text.Wrap
                }

                // What the decoder actually said, when it said anything. This
                // dialog used to be reached by a timer and so had nothing to
                // report; now it usually does, and "could not be played" alone
                // is not much help in deciding what to try next.
                QQC2.Label {
                    Layout.fillWidth: true
                    visible: text.length > 0
                    text: surface.errorText
                    color: "white"
                    opacity: 0.7
                    horizontalAlignment: Text.AlignHCenter
                    wrapMode: Text.Wrap
                    font.pointSize: Kirigami.Theme.smallFont.pointSize
                }

                QQC2.Button {
                    Layout.alignment: Qt.AlignHCenter
                    visible: root.hasFile
                    icon.name: "document-open-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button", "Open Externally")
                    onClicked: Whatevr.ProtocolController.openLocalFile(root.localPath)
                }
            }

            // The circular presentation reuses the bubbles' rounding shader
            // rather than a mask, so the edge is antialiased at any size.
            Loader {
                anchors.fill: parent
                active: root.isVideoNote

                sourceComponent: Item {
                    anchors.fill: parent

                    ShaderEffectSource {
                        id: roundSource

                        anchors.fill: parent
                        visible: false
                        live: true
                        hideSource: true
                        sourceItem: surface
                    }

                    RoundedImage {
                        anchors.fill: parent
                        source: roundSource
                        topLeftRadius: width / 2
                        topRightRadius: width / 2
                        bottomLeftRadius: width / 2
                        bottomRightRadius: width / 2
                    }
                }
            }
        }

        // Close, always reachable in the corner rather than only in the
        // transport bar an image never shows.
        QQC2.ToolButton {
            anchors.top: parent.top
            anchors.right: parent.right
            anchors.margins: Kirigami.Units.largeSpacing
            opacity: root.chromeVisible ? 1 : 0
            visible: opacity > 0
            Behavior on opacity {
                NumberAnimation { duration: Kirigami.Units.longDuration }
            }
            focusPolicy: Qt.NoFocus
            icon.name: "window-close-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Close")
            display: QQC2.AbstractButton.IconOnly
            QQC2.ToolTip.text: text
            QQC2.ToolTip.visible: hovered
            QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
            Accessible.name: text
            onClicked: root.close()
        }

        // Actions that used to be reachable only from the bubble's context
        // menu, so the full-screen view is no longer a dead end.
        Rectangle {
            anchors.top: parent.top
            anchors.left: parent.left
            anchors.margins: Kirigami.Units.largeSpacing
            opacity: root.chromeVisible ? 1 : 0
            visible: root.hasFile && opacity > 0
            Behavior on opacity {
                NumberAnimation { duration: Kirigami.Units.longDuration }
            }
            width: actions.implicitWidth + Kirigami.Units.smallSpacing * 2
            height: actions.implicitHeight + Kirigami.Units.smallSpacing
            radius: Kirigami.Units.cornerRadius
            color: Qt.rgba(0, 0, 0, 0.6)

            // Swallows taps on the bar's own background, which would otherwise
            // reach the backdrop handler and close the viewer.
            TapHandler {
                onTapped: root.wakeChrome()
            }

            RowLayout {
                id: actions

                anchors.centerIn: parent
                spacing: 0

                QQC2.ToolButton {
                    focusPolicy: Qt.NoFocus
                    icon.name: "document-save-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button", "Save As…")
                    display: QQC2.AbstractButton.IconOnly
                    QQC2.ToolTip.text: text
                    QQC2.ToolTip.visible: hovered
                    QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
                    Accessible.name: text
                    onClicked: root.saveMedia()
                }

                QQC2.ToolButton {
                    visible: !root.isVideo
                    focusPolicy: Qt.NoFocus
                    icon.name: "edit-copy-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button", "Copy Image")
                    display: QQC2.AbstractButton.IconOnly
                    QQC2.ToolTip.text: text
                    QQC2.ToolTip.visible: hovered
                    QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
                    Accessible.name: text
                    onClicked: Whatevr.ProtocolController.copyImageToClipboard(root.localPath)
                }

                QQC2.ToolButton {
                    focusPolicy: Qt.NoFocus
                    icon.name: "document-open-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button", "Open Externally")
                    display: QQC2.AbstractButton.IconOnly
                    QQC2.ToolTip.text: text
                    QQC2.ToolTip.visible: hovered
                    QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
                    Accessible.name: text
                    onClicked: Whatevr.ProtocolController.openLocalFile(root.localPath)
                }

                QQC2.ToolButton {
                    visible: root.messageId.length > 0
                    focusPolicy: Qt.NoFocus
                    icon.name: "mail-forward-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button", "Forward…")
                    display: QQC2.AbstractButton.IconOnly
                    QQC2.ToolTip.text: text
                    QQC2.ToolTip.visible: hovered
                    QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
                    Accessible.name: text
                    onClicked: {
                        const id = root.messageId
                        root.close()
                        root.forwardRequested(id)
                    }
                }
            }
        }

        // Transport controls.
        Rectangle {
            anchors.bottom: parent.bottom
            anchors.horizontalCenter: parent.horizontalCenter
            anchors.bottomMargin: Kirigami.Units.gridUnit
            opacity: root.chromeVisible ? 1 : 0
            visible: root.isVideo && opacity > 0
            Behavior on opacity {
                NumberAnimation { duration: Kirigami.Units.longDuration }
            }
            width: Math.min(parent.width - Kirigami.Units.gridUnit * 2, Kirigami.Units.gridUnit * 42)
            height: controls.implicitHeight + Kirigami.Units.largeSpacing
            radius: Kirigami.Units.cornerRadius
            color: Qt.rgba(0, 0, 0, 0.6)

            // Same as the action bar: a tap on the bar itself must not fall
            // through to the backdrop's close handler.
            TapHandler {
                onTapped: root.wakeChrome()
            }

            RowLayout {
                id: controls

                anchors.fill: parent
                anchors.margins: Kirigami.Units.smallSpacing
                spacing: Kirigami.Units.smallSpacing

                QQC2.ToolButton {
                    visible: root.hasTimeline
                    focusPolicy: Qt.NoFocus
                    icon.name: "media-seek-backward-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button", "Back 10 seconds")
                    display: QQC2.AbstractButton.IconOnly
                    QQC2.ToolTip.text: text
                    QQC2.ToolTip.visible: hovered
                    QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
                    Accessible.name: text
                    onClicked: root.skip(-10)
                }

                QQC2.ToolButton {
                    focusPolicy: Qt.NoFocus
                    icon.name: surface.playbackWanted ? "media-playback-pause-symbolic" : "media-playback-start-symbolic"
                    text: surface.playbackWanted
                        ? Whatevr.I18n.i18nc("@action:button", "Pause")
                        : Whatevr.I18n.i18nc("@action:button", "Play")
                    display: QQC2.AbstractButton.IconOnly
                    QQC2.ToolTip.text: text
                    QQC2.ToolTip.visible: hovered
                    QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
                    Accessible.name: text
                    onClicked: root.togglePlayback()
                }

                QQC2.ToolButton {
                    visible: root.hasTimeline
                    focusPolicy: Qt.NoFocus
                    icon.name: "media-seek-forward-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button", "Forward 10 seconds")
                    display: QQC2.AbstractButton.IconOnly
                    QQC2.ToolTip.text: text
                    QQC2.ToolTip.visible: hovered
                    QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
                    Accessible.name: text
                    onClicked: root.skip(10)
                }

                QQC2.Label {
                    visible: root.hasTimeline
                    // While a seek settles the clock shows where it is headed,
                    // not the pre-seek point the engine still reports.
                    text: root.formatTime(surface.seeking ? surface.seekTarget : surface.position)
                    color: "white"
                    font.pointSize: Kirigami.Theme.smallFont.pointSize
                }

                QQC2.Slider {
                    id: seekBar

                    // The drag's destination, captured from onMoved, which
                    // only user interaction fires. The release handler must
                    // not read `value`: the Binding below shares the
                    // pressedChanged notifier and can restore the stale
                    // playback position into `value` before the handler runs,
                    // which turned every scrub into a seek to where the clip
                    // already was.
                    property real dragTarget: -1

                    visible: root.hasTimeline
                    Layout.fillWidth: true
                    focusPolicy: Qt.NoFocus
                    from: 0
                    // The decoder's duration once it has one; the sender's
                    // declared duration is frequently wrong, and a bar scaled
                    // past the real end turns "drag to the right edge" into a
                    // seek beyond EOF.
                    to: surface.duration > 0 ? surface.duration : Math.max(root.durationSecs, 1)
                    // Seek on release rather than on every pixel of the drag:
                    // an absolute seek per mouse move makes the decoder thrash
                    // and the picture stutter while scrubbing.
                    onMoved: dragTarget = value
                    onPressedChanged: {
                        if (pressed) {
                            dragTarget = value
                            return
                        }
                        if (dragTarget >= 0) {
                            surface.seek(dragTarget)
                        }
                        dragTarget = -1
                    }

                    // The bar follows playback except while it is being
                    // dragged, when it follows the finger, and while a seek is
                    // settling, when it holds the destination instead of
                    // snapping back to wherever the audio clock still is.
                    // Written as a Binding rather than
                    // `value: pressed ? value : surface.position`, which reads
                    // its own value and is a binding loop.
                    Binding {
                        target: seekBar
                        property: "value"
                        value: surface.seeking ? surface.seekTarget : surface.position
                        when: !seekBar.pressed
                        restoreMode: Binding.RestoreNone
                    }

                    QQC2.ToolTip.text: root.formatTime(seekBar.value)
                    QQC2.ToolTip.visible: seekBar.pressed
                    QQC2.ToolTip.delay: 0
                }

                // A GIF has no timeline to fill the bar with, so the spacer
                // keeps the remaining controls at the right edge.
                Item {
                    visible: !root.hasTimeline
                    Layout.fillWidth: true
                }

                QQC2.Label {
                    visible: root.hasTimeline
                    text: root.formatTime(surface.duration > 0 ? surface.duration : root.durationSecs)
                    color: "white"
                    font.pointSize: Kirigami.Theme.smallFont.pointSize
                }

                QQC2.AbstractButton {
                    visible: root.hasTimeline
                    focusPolicy: Qt.NoFocus
                    implicitWidth: speedLabel.implicitWidth + Kirigami.Units.smallSpacing * 2
                    implicitHeight: speedLabel.implicitHeight + Kirigami.Units.smallSpacing
                    text: Whatevr.I18n.i18nc("@action:button", "Playback speed")
                    QQC2.ToolTip.text: text
                    QQC2.ToolTip.visible: hovered
                    QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
                    Accessible.name: text
                    onClicked: root.cycleSpeed()

                    background: Rectangle {
                        radius: height / 2
                        color: Qt.rgba(1, 1, 1, 0.16)
                    }

                    contentItem: QQC2.Label {
                        id: speedLabel

                        text: Whatevr.I18n.i18nc("@label playback speed", "%1x",
                                                 surface.speed.toFixed(surface.speed === Math.round(surface.speed) ? 0 : 1))
                        color: "white"
                        font.pointSize: Kirigami.Theme.smallFont.pointSize
                        horizontalAlignment: Text.AlignHCenter
                        verticalAlignment: Text.AlignVCenter
                    }
                }

                QQC2.ToolButton {
                    focusPolicy: Qt.NoFocus
                    icon.name: surface.muted || surface.volume <= 0
                        ? "audio-volume-muted-symbolic"
                        : "audio-volume-high-symbolic"
                    text: surface.muted
                        ? Whatevr.I18n.i18nc("@action:button", "Unmute")
                        : Whatevr.I18n.i18nc("@action:button", "Mute")
                    display: QQC2.AbstractButton.IconOnly
                    QQC2.ToolTip.text: text
                    QQC2.ToolTip.visible: hovered
                    QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
                    Accessible.name: text
                    onClicked: surface.muted = !surface.muted
                }

                QQC2.Slider {
                    id: volumeBar

                    Layout.preferredWidth: Kirigami.Units.gridUnit * 4
                    focusPolicy: Qt.NoFocus
                    from: 0
                    to: 100
                    onMoved: {
                        surface.volume = value
                        surface.muted = value <= 0
                    }

                    // Same shape as the seek bar: a drag writes `value`
                    // directly and would destroy a plain binding, after which
                    // the keyboard volume keys stop moving the handle.
                    Binding {
                        target: volumeBar
                        property: "value"
                        value: surface.volume
                        when: !volumeBar.pressed
                        restoreMode: Binding.RestoreNone
                    }
                }
            }
        }

        // Keyboard transport, matching what a video player is expected to do.
        Keys.onPressed: event => {
            // Any key counts as "the user is here", the same as moving the
            // pointer; driving by keyboard alone must not leave the controls
            // hidden.
            root.wakeChrome()
            switch (event.key) {
            case Qt.Key_Space:
                root.togglePlayback()
                event.accepted = true
                break
            case Qt.Key_Left:
                root.skip(event.modifiers & Qt.ShiftModifier ? -30 : -5)
                event.accepted = true
                break
            case Qt.Key_Right:
                root.skip(event.modifiers & Qt.ShiftModifier ? 30 : 5)
                event.accepted = true
                break
            case Qt.Key_Up:
                surface.volume = Math.min(100, surface.volume + 5)
                surface.muted = false
                event.accepted = true
                break
            case Qt.Key_Down:
                surface.volume = Math.max(0, surface.volume - 5)
                event.accepted = true
                break
            case Qt.Key_M:
                surface.muted = !surface.muted
                event.accepted = true
                break
            case Qt.Key_S:
                if (event.modifiers & Qt.ControlModifier) {
                    root.saveMedia()
                    event.accepted = true
                }
                break
            case Qt.Key_C:
                if ((event.modifiers & Qt.ControlModifier) && !root.isVideo && root.hasFile) {
                    Whatevr.ProtocolController.copyImageToClipboard(root.localPath)
                    event.accepted = true
                }
                break
            }
        }
        focus: true
    }
}
