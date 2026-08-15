// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as Controls

import org.kde.kirigami as Kirigami

import Whatevr as Whatevr

import "MediaFormat.js" as MediaFormat

/**
 * Video, GIF and video-note bubbles.
 *
 * All three are the same thing on the wire and differ only in presentation: a
 * video and a video note wait to be asked, with a video note shown in a circle
 * with a progress ring. Rectangular media is silent inline; a GIF is a silent
 * loop that starts by itself. Playback goes through VideoSurface so arbitration
 * and decoder lifetime stay consistent between bubbles and the full-screen
 * viewer.
 *
 * Everything on screen is chosen by one `phase`, and every phase has exactly
 * one thing in the middle of the bubble and one meaning for a tap. That is
 * deliberate: this file used to compute six overlapping `visible:` conditions
 * independently, which is how a video note ended up showing a spinner on top of
 * a thumbnail next to a play glyph, forever, with no way out.
 */
Item {
    id: root

    objectName: "videoBubble"
    required property ChatBubble row

    property real topLeftRadius: 0
    property real topRightRadius: 0
    property real bottomLeftRadius: 0
    property real bottomRightRadius: 0

    readonly property bool isGif: row.isGif
    readonly property bool isVideoNote: row.isVideoNote
    readonly property bool hasFile: row.mediaLocalPath.length > 0

    // ---- Artwork ----

    /**
     * The still a decoder left behind for this message, if there is one.
     *
     * It takes precedence over the daemon's poster, which is the *first* frame
     * of the clip: after a trip through the full-screen viewer the poster shows
     * neither where the user was nor, on a clip that opens on black, anything at
     * all. Revision-keyed because the pipeline would otherwise answer from cache
     * and never show a newer capture.
     *
     * Assigned rather than bound: frameRevision() is a plain call with nothing
     * to notify on. refreshStill() runs at every moment it can change: this
     * row being built, being reused for another message, and the arbiter
     * capturing a still for it.
     */
    property int stillRevision: 0
    readonly property bool hasStill: stillRevision > 0
    readonly property url stillSource: hasStill
        ? "image://videoframe/" + encodeURIComponent(row.messageId) + "?rev=" + stillRevision
        : ""

    function refreshStill() {
        stillRevision = Whatevr.VideoPlayback.frameRevision(row.messageId)
    }

    readonly property bool hasPosterFile: row.mediaThumbnailLocalPath.length > 0

    // A video note is a circle, so its four radii are half its size and the
    // bubble's grouped-corner radii do not apply. The picture is inset by the
    // ring's thickness so the ring rides the edge instead of covering frames.
    readonly property real ringWidth: Math.max(2, Math.round(Kirigami.Units.smallSpacing / 2))
    readonly property real ringInset: isVideoNote ? ringWidth * 2 : 0
    readonly property real circleRadius: Math.min(picture.width, picture.height) / 2
    readonly property real cornerTopLeft: isVideoNote ? circleRadius : topLeftRadius
    readonly property real cornerTopRight: isVideoNote ? circleRadius : topRightRadius
    readonly property real cornerBottomLeft: isVideoNote ? circleRadius : bottomLeftRadius
    readonly property real cornerBottomRight: isVideoNote ? circleRadius : bottomRightRadius

    // ---- Intent ----

    /**
     * What the user has asked of this clip: "stopped", "playing" or "paused".
     *
     * One value rather than the three overlapping flags this file used to carry
     * (userStarted, userPaused, userUnmuted), which between them could describe
     * states that made no sense and disagreed about what a tap should do.
     */
    property string intent: "stopped"

    /// A GIF runs on its own; nothing else does. Videos and video notes carry
    /// sound and wait to be asked.
    readonly property bool autoWants: isGif && Whatevr.Settings.autoplayInlineMedia

    /// Playback of every kind stops when the bubble leaves the viewport, not
    /// just autoplay: a clip the user started used to keep its decoder and keep
    /// running several screens away, heard but not seen.
    readonly property bool onScreen: row.activeInViewport
                                     && !row.pooled
                                     && !(row.fastFlicking && Whatevr.Settings.pausePlaybackWhileScrolling)

    readonly property bool hasSource: playbackSource.toString().length > 0
    readonly property bool wantsRun: (intent === "playing" || (intent === "stopped" && autoWants))
                                     && onScreen && hasSource
    /// Paused keeps the decoder so the frame stays on screen; stopped does not.
    readonly property bool engaged: (wantsRun || intent === "paused") && onScreen && hasSource

    readonly property bool loops: isGif ? Whatevr.Settings.loopGifs : false
    /// Ordinary rectangular video begins muted and remembers this choice.
    /// Video notes keep their existing unmuted startup. GIFs stay muted.
    property bool userMuted: !isVideoNote

    /// Where playback should pick up, refreshed from the arbiter rather than
    /// tracked here: the surface writes it down at the moment it lets go of the
    /// decoder, which is the only moment the position is still true.
    property real resumeAt: 0

    // The source chosen for a playback session never changes underneath the
    // decoder. Promotion to a local file takes effect on the next session.
    property url streamUrl
    property string activeStreamId: ""
    property string recoveredLocalPath: ""
    property url sessionSource
    readonly property url playbackSource: sessionSource.toString().length > 0
        ? sessionSource
        : (recoveredLocalPath.length > 0
           ? Qt.resolvedUrl("file://" + recoveredLocalPath)
           : (hasFile ? Qt.resolvedUrl("file://" + row.mediaLocalPath) : streamUrl))

    // Set synchronously before a protocol request is sent. This both makes the
    // first tap visible immediately and prevents a second request.
    property bool requestPending: false
    property bool playAfterDownload: false
    property bool retryDownloadOnly: false
    signal playbackRequestDispatched(string method)

    /// The stream said it could not be fetched. Distinct from the decoder's own
    /// verdict below, which the surface reports.
    property bool streamFailed: false
    /// A decoder that neither errored nor ever drew anything. The backstop, not
    /// the mechanism: a bubble that never got a frame used to spin forever, and
    /// the timer that replaced that then went off on clips that were merely
    /// slow. Now it only runs while the engine is not reporting work in
    /// progress, and playback failure proper comes from the decoder.
    property bool stalledOut: false
    readonly property bool playbackFailed: streamFailed || surface.failed || stalledOut

    // ---- Phase ----

    /**
     * The single answer to "what is this bubble doing", and the only thing any
     * overlay below is allowed to test.
     */
    readonly property string phase: {
        // A backend may retain its last decoded frame until its teardown lands.
        // That frame is presentation only, not playback state. Once the clip is
        // stopped, return to the poster and let the next tap create a fresh
        // backend instead of trying to resume a decoder already sitting at EOF.
        if (engaged && surface.hasFrame)
            return wantsRun ? "playing" : "paused"
        if (playbackFailed || (row.mediaDownloadError.length > 0 && !hasFile))
            return "failed"
        if (requestPending)
            return "buffering"
        if (engaged && surface.grantHeld)
            return "buffering"
        if (row.mediaDownloading && !hasSource)
            return "downloading"
        if (!hasSource && !row.mediaDownloading)
            return "needsDownload"
        return "idle"
    }

    readonly property bool showsVideo: phase === "playing" || phase === "paused"
    readonly property bool showsTransport: showsVideo && !row.selectionModeActive

    Timer {
        id: failureTimer

        // Only while the engine is not reporting work in progress. A clip
        // opening off a `media.stream` source waits on bytes the daemon is
        // still fetching, which is normal and can take a while; a stopwatch
        // running through that called healthy videos unplayable.
        interval: 20000
        running: root.phase === "buffering" && !surface.stalled && !root.playbackFailed
        onTriggered: root.stalledOut = true
    }

    onPhaseChanged: {
        if (phase === "playing" || phase === "paused") {
            streamFailed = false
            stalledOut = false
        }
    }

    // ---- Actions ----

    /**
     * A single tap, which means the same thing on every kind: start what is
     * stopped, stop what is running.
     *
     * There is no second-tap escalation to full screen and no double tap. Both
     * were invisible, and the escalation left the inline copy running behind
     * the viewer. Full screen is a button now.
     */
    function activate() {
        if (row.messageId.length === 0)
            return
        switch (phase) {
        case "downloading":
            return
        case "needsDownload":
            requestPlayback()
            intent = "playing"
            return
        case "failed":
            streamFailed = false
            stalledOut = false
            if (hasSource) {
                beginPlayback()
            } else {
                requestPlayback()
                intent = "playing"
            }
            return
        case "playing":
            intent = "paused"
            return
        case "paused":
            // The decoder is still attached and still where it was left, so
            // this is a resume rather than a fresh start.
            intent = "playing"
            return
        case "buffering":
            // A tap on the spinner used to cancel the session the user just
            // asked for; a slow load is not a reason to throw the request
            // away. Cancelling stays available through the context menu.
            return
        default:
            beginPlayback()
        }
    }

    function beginPlayback() {
        resumeAt = Whatevr.VideoPlayback.resumePosition(row.messageId)
        latchAvailableSource()
        intent = "playing"
    }

    function latchAvailableSource() {
        if (sessionSource.toString().length > 0)
            return
        if (recoveredLocalPath.length > 0)
            sessionSource = Qt.resolvedUrl("file://" + recoveredLocalPath)
        else if (hasFile)
            sessionSource = Qt.resolvedUrl("file://" + row.mediaLocalPath)
        else if (streamUrl.toString().length > 0)
            sessionSource = streamUrl
    }

    /**
     * Hands the clip to the full-screen viewer at the second it reached here.
     *
     * Nothing stops playback from in here. The viewer takes the exclusive lane
     * as it opens, which revokes this bubble through onRevoked below, so one
     * mechanism decides what is playing rather than two that can disagree about
     * it. This is also what stops the viewer and the bubble running the same
     * clip at once, which is what the second tap used to do.
     */
    function openFullScreen() {
        // Before handing over: the viewer opens on this frame while its own
        // decoder is still opening the file and seeking to the position below.
        surface.rememberStill()
        row.videoActivated(row.messageId,
                           root.recoveredLocalPath.length > 0 ? root.recoveredLocalPath : row.mediaLocalPath,
                           root.streamUrl.toString(),
                           root.activeStreamId,
                           row.mediaKind,
                           row.mediaDurationSecs,
                           surface.handoffPosition())
    }

    /// Starts a stream if we have no file yet, falling back to an ordinary
    /// download when the daemon says this message cannot be streamed.
    function requestPlayback() {
        if (hasFile || row.mediaDownloading || requestPending)
            return
        requestPending = true
        playAfterDownload = true
        if (!Whatevr.Settings.streamWhileDownloading || isVideoNote || retryDownloadOnly) {
            playbackRequestDispatched("download")
            Whatevr.ProtocolController.downloadMessageMedia(row.messageId)
            return
        }
        playbackRequestDispatched("stream")
        Whatevr.ProtocolController.streamMessageMedia(row.messageId)
    }

    /// A GIF that is on screen and has nothing to play asks for its bytes; a
    /// video or a video note waits to be tapped.
    function requestAutoplaySource() {
        if (autoWants && onScreen && !hasFile && streamUrl.toString().length === 0)
            requestPlayback()
    }

    function resetForMessage() {
        intent = "stopped"
        streamUrl = ""
        activeStreamId = ""
        recoveredLocalPath = ""
        sessionSource = ""
        requestPending = false
        playAfterDownload = false
        retryDownloadOnly = false
        streamFailed = false
        stalledOut = false
        userMuted = !isVideoNote
        resumeAt = Whatevr.VideoPlayback.resumePosition(row.messageId)
        refreshStill()

        // Model role notifications do not have a useful ordering guarantee.
        // Wait until this event turn has applied the new kind and media paths
        // before deciding whether the replacement row is an autoplaying GIF.
        const replacementId = row.messageId
        Qt.callLater(function() {
            if (root.row.messageId === replacementId)
                root.requestAutoplaySource()
        })
    }

    onAutoWantsChanged: requestAutoplaySource()
    onOnScreenChanged: requestAutoplaySource()
    onIsVideoNoteChanged: if (intent === "stopped") userMuted = !isVideoNote
    onIntentChanged: {
        if (intent === "stopped") {
            sessionSource = ""
            playAfterDownload = false
        }
    }
    onHasFileChanged: {
        if (!hasFile)
            return
        requestPending = false
        retryDownloadOnly = false
        if (playAfterDownload || intent === "playing" || autoWants) {
            latchAvailableSource()
            playAfterDownload = false
        }
    }
    // A row already in the viewport when its delegate is built has these true
    // from the first binding evaluation, so waiting for a change signal alone
    // is waiting for something that may never come.
    Component.onCompleted: {
        resumeAt = Whatevr.VideoPlayback.resumePosition(row.messageId)
        refreshStill()
        requestAutoplaySource()
    }

    Connections {
        target: Whatevr.ProtocolController

        function onMediaStreamReady(messageId, streamId, url) {
            if (messageId !== root.row.messageId)
                return
            root.streamUrl = url
            root.activeStreamId = streamId
            root.requestPending = false
            if (root.intent === "playing" || root.autoWants)
                root.latchAvailableSource()
        }

        function onMediaStreamFailed(messageId) {
            if (messageId !== root.row.messageId)
                return
            root.retryDownloadOnly = true
            root.requestPending = true
            root.playbackRequestDispatched("download")
            Whatevr.ProtocolController.downloadMessageMedia(messageId)
        }

        function onMediaStreamUpdated(streamId, messageId, state, path, error) {
            if (messageId !== root.row.messageId || streamId !== root.activeStreamId)
                return
            root.activeStreamId = ""
            root.requestPending = false
            if (state === "local" && path.length > 0) {
                const at = surface.handoffPosition()
                if (at > 0) {
                    root.resumeAt = at
                    Whatevr.VideoPlayback.setResumePosition(messageId, at)
                }
                root.sessionSource = Qt.resolvedUrl("file://" + path)
                root.recoveredLocalPath = path
                root.streamFailed = false
                root.stalledOut = false
            } else if (state === "failed") {
                root.streamFailed = true
                root.intent = "stopped"
            }
        }
    }

    Connections {
        target: root.row

        function onMessageIdChanged() {
            root.resetForMessage()
        }

        function onMediaDownloadingChanged() {
            if (root.row.mediaDownloading)
                root.requestPending = false
        }

        function onMediaDownloadErrorChanged() {
            if (root.row.mediaDownloadError.length > 0)
                root.requestPending = false
        }
    }

    Connections {
        target: Whatevr.VideoPlayback

        function onResumePositionChanged(messageId) {
            if (messageId === root.row.messageId)
                root.resumeAt = Whatevr.VideoPlayback.resumePosition(messageId)
        }

        function onFrameCaptured(messageId) {
            if (messageId === root.row.messageId)
                root.refreshStill()
        }

        function onInlineHandoff(messageId, seconds, resumePlayback) {
            if (messageId !== root.row.messageId)
                return
            root.resumeAt = Math.max(0, seconds)
            root.sessionSource = ""
            if (resumePlayback) {
                root.latchAvailableSource()
                root.intent = "playing"
            } else {
                root.intent = "stopped"
            }
        }
    }

    // ---- The picture ----
    // Rounding is the same single-pass SDF shader the photo bubbles use. The
    // idle case (a thumbnail) samples the Image directly, so a chat full of
    // clips costs no framebuffers; only the few bubbles actually decoding video
    // pay for a layer.

    Item {
        id: picture

        anchors.fill: parent
        anchors.margins: root.ringInset

        Image {
            id: poster

            objectName: "videoBubble.poster"
            property bool everDecoded: false
            readonly property url targetSource: root.hasStill
                ? root.stillSource
                : (root.hasPosterFile
                   ? Qt.resolvedUrl("file://" + root.row.mediaThumbnailLocalPath)
                   : "")
            onTargetSourceChanged: everDecoded = false
            onStatusChanged: if (status === Image.Ready) everDecoded = true

            anchors.fill: parent
            visible: false
            // Do not begin a texture upload in the middle of a fast fling. An
            // already-decoded poster remains available, while a new one waits
            // for the list to settle. This is the same policy as photo rows.
            source: !root.row.pooled && (!root.row.fastFlicking || everDecoded)
                ? targetSource
                : ""
            fillMode: Image.PreserveAspectCrop
            asynchronous: true
            // A provider url is answered from a store that changes underneath
            // it; the revision in the url is what makes a new capture reload,
            // and caching the old one by url would defeat that.
            cache: !root.hasStill
            // The generic thumbnail cap is deliberately tiny and exists to
            // make a blurred photo placeholder. A video poster is the final
            // idle artwork, so decode it at the stable media display size.
            sourceSize.width: root.row.imageDecodeWidth
            sourceSize.height: root.row.imageDecodeHeight
        }

        RoundedImage {
            anchors.fill: parent
            // Deliberately not gated on `!showsVideo`. The video layer below is
            // built only once playback is running and needs a frame or two to
            // render its first texture; taking the poster away the instant the
            // decoder reported a frame left a hole for exactly that long, which
            // is the blank a clip used to flash on starting.
            visible: poster.status === Image.Ready
            source: poster
            topLeftRadius: root.cornerTopLeft
            topRightRadius: root.cornerTopRight
            bottomLeftRadius: root.cornerBottomLeft
            bottomRightRadius: root.cornerBottomRight
        }

        // Placeholder for a clip that arrived without artwork, so the slot is
        // never a hole in the wallpaper. Same reasoning as above: it stays under
        // the video rather than being switched off as playback starts.
        Kirigami.ShadowedRectangle {
            anchors.fill: parent
            visible: poster.status !== Image.Ready
            color: Qt.alpha(Kirigami.Theme.textColor, 0.08)
            corners.topLeftRadius: root.cornerTopLeft
            corners.topRightRadius: root.cornerTopRight
            corners.bottomLeftRadius: root.cornerBottomLeft
            corners.bottomRightRadius: root.cornerBottomRight
        }

        VideoSurface {
            id: surface

            objectName: "videoBubble.surface"
            anchors.fill: parent
            // Underneath the poster and placeholder plate. Until it has frames
            // it is an empty black rectangle, so keep the thumbnail above it
            // while the decoder opens.
            z: -1
            // Visible from the moment a decoder is wanted. The poster remains
            // above it until the first frame is ready, then the layer below
            // presents that frame with the bubble's rounded corners.
            visible: root.engaged
            messageId: root.row.messageId
            source: root.playbackSource
            startPosition: root.resumeAt
            lane: root.isGif ? Whatevr.VideoPlayback.Animated : Whatevr.VideoPlayback.Exclusive
            engaged: root.engaged
            playing: root.wantsRun
            muted: root.isGif ? true : root.userMuted
            loop: root.loops

            // Something else took the exclusive lane. Stop wanting to play, or
            // the bubble would sit showing transport controls over a surface
            // that is never going to produce another frame.
            onRevoked: root.intent = "stopped"
            // A clip that ran out is finished, not paused partway: back to the
            // poster, and forget the position so the next tap starts it over.
            onEndOfFile: {
                root.intent = "stopped"
                Whatevr.VideoPlayback.clearResumePosition(root.row.messageId)
            }
        }

        // One layer, alive only while this bubble is actually decoding.
        Loader {
            anchors.fill: parent
            active: root.showsVideo

            sourceComponent: Item {
                anchors.fill: parent

                ShaderEffectSource {
                    id: surfaceTexture

                    anchors.fill: parent
                    visible: false
                    live: true
                    hideSource: true
                    sourceItem: surface
                }

                RoundedImage {
                    anchors.fill: parent
                    source: surfaceTexture
                    topLeftRadius: root.cornerTopLeft
                    topRightRadius: root.cornerTopRight
                    bottomLeftRadius: root.cornerBottomLeft
                    bottomRightRadius: root.cornerBottomRight
                }
            }
        }
    }

    // ---- Times ----

    /// Total length: the decoder's answer once it has one, since a sender's
    /// declared duration is frequently rounded or simply wrong.
    readonly property real totalSeconds: surface.duration > 0
        ? surface.duration
        : root.row.mediaDurationSecs
    /// What the chips show: how much is left, counting down while playing, and
    /// the full length before it starts. Every other client counts down.
    readonly property real remainingSeconds: root.showsVideo
        ? Math.max(0, totalSeconds - surface.position)
        : totalSeconds
    /// How far through, for the ring and the inline scrubber. Falls back to the
    /// remembered position so a stopped clip still shows where it got to.
    readonly property real progressFraction: {
        if (totalSeconds <= 0)
            return 0
        const at = root.showsVideo ? surface.position : root.resumeAt
        return Math.min(1, Math.max(0, at / totalSeconds))
    }

    // ---- Video-note ring ----

    // The ring doubles as the progress arc, which is the only progress
    // indication a circle has room for.
    ProgressCircle {
        anchors.fill: parent
        visible: root.isVideoNote
        showLabel: false
        lineWidth: root.ringWidth
        // Centred on the gap between the item edge and the picture, so the ring
        // frames the circle instead of sitting on top of it.
        inset: root.ringInset / 2
        progress: root.progressFraction
        // Read against the wallpaper, not just against the picture: the note
        // draws no plate, so a fainter track disappeared into the doodle.
        trackColor: Qt.alpha(Kirigami.Theme.textColor, 0.38)
        fillColor: Whatevr.Palette.highlight
    }

    // ---- The one button in the middle ----

    // Play, resume or download, whichever this phase means, in the one place
    // the eye already goes. The old bubble had a play glyph in the middle and a
    // download button in a corner, both visible at once on a clip that was
    // neither playable nor downloading.
    Item {
        id: primaryButton

        anchors.centerIn: parent
        visible: !root.row.selectionModeActive
                 && (root.phase === "idle" || root.phase === "needsDownload")
        width: Kirigami.Units.gridUnit * 2.6
        height: width

        Rectangle {
            anchors.fill: parent
            radius: width / 2
            color: Qt.alpha("black", 0.45)
        }

        Kirigami.Icon {
            anchors.centerIn: parent
            width: parent.width * 0.5
            height: width
            source: root.phase === "needsDownload"
                ? "folder-download-symbolic"
                : "media-playback-start-symbolic"
            color: "white"
            isMask: true
        }

        // Keep the video-note button behavior unchanged. Rectangular media has
        // no handler here because the whole surface owns its tap.
        TapHandler {
            enabled: root.isVideoNote
            onTapped: root.activate()
        }
    }

    // The size of what a tap is about to fetch, under the download button, so
    // the decision is informed rather than a surprise.
    Controls.Label {
        anchors.horizontalCenter: primaryButton.horizontalCenter
        anchors.top: primaryButton.bottom
        anchors.topMargin: Kirigami.Units.smallSpacing
        visible: primaryButton.visible && root.phase === "needsDownload" && text.length > 0
        text: MediaFormat.humanSize(root.row.mediaSizeBytes)
        color: "white"
        font.pointSize: Kirigami.Theme.smallFont.pointSize
        style: Text.Outline
        styleColor: Qt.alpha("black", 0.6)
    }

    // Everything below is built only for the phase that needs it. A chat is
    // mostly rows that are doing nothing, and a delegate that instantiates its
    // transport, its spinner and its failure plate whether or not they are on
    // screen is what the DN9 object budget exists to catch.

    Loader {
        anchors.centerIn: parent
        active: root.phase === "buffering"
                || (root.phase === "downloading" && root.row.mediaDownloadProgress < 0)

        sourceComponent: Controls.BusyIndicator {
            running: true
        }
    }

    Loader {
        anchors.centerIn: parent
        active: root.phase === "downloading" && root.row.mediaDownloadProgress >= 0

        sourceComponent: ProgressCircle {
            progress: Math.max(0, root.row.mediaDownloadProgress)
            width: Kirigami.Units.gridUnit * 2
            height: width
        }
    }

    // A clip that asked for a decoder and never drew anything says so, and
    // offers the two ways out. This is what the endless spinner became.
    Loader {
        anchors.centerIn: parent
        active: root.phase === "failed"

        sourceComponent: Rectangle {
            width: Math.min(root.width - Kirigami.Units.largeSpacing * 2,
                            failureRow.implicitWidth + Kirigami.Units.largeSpacing * 2)
            height: failureRow.implicitHeight + Kirigami.Units.largeSpacing
            radius: Kirigami.Units.cornerRadius
            color: Qt.alpha("black", 0.7)

            Row {
                id: failureRow

                anchors.centerIn: parent
                spacing: Kirigami.Units.smallSpacing

                Kirigami.Icon {
                    anchors.verticalCenter: parent.verticalCenter
                    source: "dialog-error-symbolic"
                    color: "white"
                    width: Kirigami.Units.iconSizes.small
                    height: width
                }

                Controls.Label {
                    anchors.verticalCenter: parent.verticalCenter
                    // There is no room in a bubble for the decoder's own
                    // message; the full-screen viewer shows that. This says
                    // only that it was tried and did not work.
                    text: Whatevr.I18n.i18nc("@info", "Could not play")
                    color: "white"
                    font.pointSize: Kirigami.Theme.smallFont.pointSize
                }

                MediaOverlayButton {
                    visible: root.isVideoNote
                    anchors.verticalCenter: parent.verticalCenter
                    diameter: Kirigami.Units.gridUnit * 1.5
                    iconName: "view-refresh-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button", "Try again")
                    onClicked: root.activate()
                }

                MediaOverlayButton {
                    anchors.verticalCenter: parent.verticalCenter
                    visible: root.hasFile
                    diameter: Kirigami.Units.gridUnit * 1.5
                    iconName: "document-open-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button", "Open externally")
                    onClicked: Whatevr.ProtocolController.openLocalFile(root.row.mediaLocalPath)
                }
            }
        }
    }

    // ---- Chips ----

    // Duration and kind chip, retained throughout rectangular playback.
    Rectangle {
        anchors.left: parent.left
        anchors.top: parent.top
        anchors.margins: Kirigami.Units.smallSpacing
        visible: !root.isVideoNote
                 && (root.isGif || root.totalSeconds > 0)
        width: chipLabel.implicitWidth + Kirigami.Units.smallSpacing * 2
        height: chipLabel.implicitHeight + Kirigami.Units.smallSpacing
        radius: Kirigami.Units.cornerRadius
        color: Qt.alpha("black", 0.55)

        Controls.Label {
            id: chipLabel

            anchors.centerIn: parent
            text: root.isGif
                ? Whatevr.I18n.i18nc("@label animated image", "GIF")
                : MediaFormat.clockTime(root.remainingSeconds)
            color: "white"
            font.pointSize: Kirigami.Theme.smallFont.pointSize
        }
    }

    // The same clock for a video note, which has no corner to put a chip in:
    // centred just inside the bottom of the circle. The ring shows progress but
    // never says how much of it is left.
    Rectangle {
        anchors.horizontalCenter: picture.horizontalCenter
        anchors.bottom: picture.bottom
        anchors.bottomMargin: Math.round(root.circleRadius * 0.16)
        visible: root.isVideoNote && root.totalSeconds > 0 && root.phase !== "downloading"
        width: noteTimeLabel.implicitWidth + Kirigami.Units.smallSpacing * 2
        height: noteTimeLabel.implicitHeight + Kirigami.Units.smallSpacing / 2
        radius: height / 2
        color: Qt.alpha("black", 0.55)

        Controls.Label {
            id: noteTimeLabel

            anchors.centerIn: parent
            text: MediaFormat.clockTime(root.remainingSeconds)
            color: "white"
            font.pointSize: Kirigami.Theme.smallFont.pointSize
        }
    }

    // A paused rectangular clip keeps its frame and shows one centered hint.
    Item {
        anchors.centerIn: parent
        visible: !root.isVideoNote && root.phase === "paused" && !root.row.selectionModeActive
        width: Kirigami.Units.gridUnit * 2.6
        height: width

        Rectangle {
            anchors.fill: parent
            radius: width / 2
            color: Qt.alpha("black", 0.45)
        }

        Kirigami.Icon {
            anchors.centerIn: parent
            width: parent.width * 0.5
            height: width
            source: "media-playback-start-symbolic"
            color: "white"
            isMask: true
        }
    }

    Loader {
        anchors.fill: picture
        active: !root.isVideoNote && root.showsTransport

        sourceComponent: Item {
            MediaOverlayButton {
                objectName: "videoBubble.audioButton"
                anchors.left: parent.left
                anchors.bottom: parent.bottom
                anchors.margins: Kirigami.Units.smallSpacing
                visible: !root.isGif
                diameter: Kirigami.Units.gridUnit * 1.6
                iconName: root.userMuted ? "audio-volume-muted-symbolic" : "audio-volume-high-symbolic"
                text: root.userMuted
                    ? Whatevr.I18n.i18nc("@action:button", "Unmute")
                    : Whatevr.I18n.i18nc("@action:button", "Mute")
                onClicked: root.userMuted = !root.userMuted
            }

            MediaOverlayButton {
                objectName: "videoBubble.fullscreenButton"
                anchors.top: parent.top
                anchors.right: parent.right
                anchors.margins: Kirigami.Units.smallSpacing
                diameter: Kirigami.Units.gridUnit * 1.6
                iconName: "view-fullscreen-symbolic"
                text: Whatevr.I18n.i18nc("@action:button", "Full screen")
                onClicked: root.openFullScreen()
            }
        }
    }

    // A circle has no room for a strip, so its two buttons ride the rim, on the
    // lower diagonal where the bounding box is empty anyway. Full screen sits
    // on the right on both shapes.
    Loader {
        anchors.fill: picture
        active: root.isVideoNote && root.showsTransport

        sourceComponent: Item {
            MediaOverlayButton {
                anchors.horizontalCenter: parent.horizontalCenter
                anchors.verticalCenter: parent.verticalCenter
                anchors.horizontalCenterOffset: -Math.round(root.circleRadius * 0.56)
                anchors.verticalCenterOffset: Math.round(root.circleRadius * 0.56)
                diameter: Kirigami.Units.gridUnit * 1.6
                iconName: root.userMuted ? "audio-volume-muted-symbolic" : "audio-volume-high-symbolic"
                text: root.userMuted
                    ? Whatevr.I18n.i18nc("@action:button", "Unmute")
                    : Whatevr.I18n.i18nc("@action:button", "Mute")
                onClicked: root.userMuted = !root.userMuted
            }

            MediaOverlayButton {
                anchors.horizontalCenter: parent.horizontalCenter
                anchors.verticalCenter: parent.verticalCenter
                anchors.horizontalCenterOffset: Math.round(root.circleRadius * 0.56)
                anchors.verticalCenterOffset: Math.round(root.circleRadius * 0.56)
                diameter: Kirigami.Units.gridUnit * 1.6
                iconName: "view-fullscreen-symbolic"
                text: Whatevr.I18n.i18nc("@action:button", "Full screen")
                onClicked: root.openFullScreen()
            }
        }
    }

    MediaDragArea {
        anchors.fill: parent
        z: -1
        localPath: root.row.mediaLocalPath
        blocked: root.row.selectionModeActive
    }

    TapHandler {
        enabled: !root.row.selectionModeActive
        onTapped: {
            root.activate()
            root.row.conversationFocusRequested()
        }
    }
}
