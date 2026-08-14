// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as Controls
import QtQuick.Layouts

import org.kde.kirigami as Kirigami

import Whatevr as Whatevr

import "MediaFormat.js" as MediaFormat

/**
 * Video, GIF and video-note bubbles.
 *
 * All three are the same thing on the wire and differ only in presentation: a
 * video and a video note wait to be asked and play with sound, a video note in
 * a circle with a progress ring; a GIF is a silent loop that starts by itself.
 * Playback goes through VideoSurface so neither this file nor MediaViewer knows
 * which engine is live, and so that only one clip with sound plays anywhere in
 * the app.
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
    readonly property bool hasThumbnail: row.mediaThumbnailLocalPath.length > 0

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
    /// GIFs are silent by definition. Everything else plays out loud, because
    /// the user asked for it by tapping, and can be muted from the strip.
    property bool userMuted: false

    /// Where playback should pick up, refreshed from the arbiter rather than
    /// tracked here: the surface writes it down at the moment it lets go of the
    /// decoder, which is the only moment the position is still true.
    property real resumeAt: 0

    // Local file first; while it is still downloading the daemon can stream it.
    property url streamUrl
    readonly property url playbackSource: hasFile
        ? Qt.resolvedUrl("file://" + row.mediaLocalPath)
        : streamUrl

    /// Long enough with a decoder and no picture that something is wrong rather
    /// than slow. Before this existed a bubble that never got a frame span its
    /// spinner for the rest of the session.
    property bool playbackFailed: false

    // ---- Phase ----

    /**
     * The single answer to "what is this bubble doing", and the only thing any
     * overlay below is allowed to test.
     */
    readonly property string phase: {
        if (row.mediaDownloading)
            return "downloading"
        if (playbackFailed)
            return "failed"
        // A backend may retain its last decoded frame until its teardown lands.
        // That frame is presentation only, not playback state. Once the clip is
        // stopped, return to the poster and let the next tap create a fresh
        // backend instead of trying to resume a decoder already sitting at EOF.
        if (engaged && surface.hasFrame)
            return wantsRun ? "playing" : "paused"
        if (engaged && surface.grantHeld)
            return "buffering"
        if (!hasSource && !row.mediaDownloading)
            return "needsDownload"
        return "idle"
    }

    readonly property bool showsVideo: phase === "playing" || phase === "paused"
    /// The transport belongs to any clip that has a picture up, GIFs included.
    /// A GIF showing the same strip as a video is noisier than hiding it behind
    /// a hover, and it is the point: there is one set of controls, in one place,
    /// that you do not have to discover.
    readonly property bool showsTransport: showsVideo && !row.selectionModeActive
    readonly property real transportRightMargin: row.imageOnly
        ? row.tntWidth + row.footerInset + Kirigami.Units.smallSpacing
        : Kirigami.Units.smallSpacing

    Timer {
        id: failureTimer

        interval: 6000
        running: root.phase === "buffering"
        onTriggered: root.playbackFailed = true
    }

    onPhaseChanged: {
        if (phase === "playing" || phase === "paused")
            playbackFailed = false
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
            playbackFailed = false
            beginPlayback()
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
            // Acts as a stop: the spinner is the only thing on screen, so the
            // only useful thing a tap can mean is "never mind".
            intent = "stopped"
            return
        default:
            beginPlayback()
        }
    }

    function beginPlayback() {
        resumeAt = Whatevr.VideoPlayback.resumePosition(row.messageId)
        intent = "playing"
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
        row.videoActivated(row.messageId,
                           row.mediaLocalPath,
                           root.streamUrl.toString(),
                           row.mediaKind,
                           row.mediaDurationSecs,
                           surface.position)
    }

    /// Below this, fetching the whole file beats streaming it: the download
    /// finishes in about the time the range server takes to warm up, and unlike
    /// a stream it leaves something on disk.
    readonly property int streamingThresholdBytes: 4 * 1024 * 1024
    /// A clip small enough that streaming it is all cost and no benefit. Video
    /// notes are the case that matters: a one-second, quarter-megabyte
    /// recording was re-fetched and re-decrypted on every scroll past, because
    /// streaming never writes mediaLocalPath and so never counts as downloaded.
    readonly property bool prefersDownload: isVideoNote
        || (row.mediaSizeBytes > 0 && row.mediaSizeBytes <= streamingThresholdBytes)

    /// Starts a stream if we have no file yet, falling back to an ordinary
    /// download when the daemon says this message cannot be streamed.
    function requestPlayback() {
        if (hasFile || row.mediaDownloading)
            return
        if (!Whatevr.Settings.streamWhileDownloading || prefersDownload) {
            Whatevr.ProtocolController.downloadMessageMedia(row.messageId)
            return
        }
        Whatevr.ProtocolController.streamMessageMedia(row.messageId)
    }

    /// A GIF that is on screen and has nothing to play asks for its bytes; a
    /// video or a video note waits to be tapped.
    function requestAutoplaySource() {
        if (autoWants && onScreen && !hasFile && streamUrl.toString().length === 0)
            requestPlayback()
    }

    onAutoWantsChanged: requestAutoplaySource()
    onOnScreenChanged: requestAutoplaySource()
    // A row already in the viewport when its delegate is built has these true
    // from the first binding evaluation, so waiting for a change signal alone
    // is waiting for something that may never come.
    Component.onCompleted: {
        resumeAt = Whatevr.VideoPlayback.resumePosition(row.messageId)
        requestAutoplaySource()
    }

    Connections {
        target: Whatevr.ProtocolController

        function onMediaStreamReady(messageId, url) {
            if (messageId !== root.row.messageId)
                return
            root.streamUrl = url
        }

        function onMediaStreamFailed(messageId) {
            if (messageId !== root.row.messageId)
                return
            // Streaming is an optimisation, not a requirement: fall back to
            // fetching the whole file and playing it from disk.
            Whatevr.ProtocolController.downloadMessageMedia(messageId)
        }
    }

    Connections {
        target: Whatevr.VideoPlayback

        function onResumePositionChanged(messageId) {
            if (messageId === root.row.messageId)
                root.resumeAt = Whatevr.VideoPlayback.resumePosition(messageId)
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
            readonly property string targetPath: root.row.mediaThumbnailLocalPath
            onTargetPathChanged: everDecoded = false
            onStatusChanged: if (status === Image.Ready) everDecoded = true

            anchors.fill: parent
            visible: false
            // Do not begin a texture upload in the middle of a fast fling. An
            // already-decoded poster remains available, while a new one waits
            // for the list to settle. This is the same policy as photo rows.
            source: root.hasThumbnail && !root.row.pooled
                    && (!root.row.fastFlicking || everDecoded)
                ? Qt.resolvedUrl("file://" + root.row.mediaThumbnailLocalPath)
                : ""
            fillMode: Image.PreserveAspectCrop
            asynchronous: true
            cache: true
            // The generic thumbnail cap is deliberately tiny and exists to
            // make a blurred photo placeholder. A video poster is the final
            // idle artwork, so decode it at the stable media display size.
            sourceSize.width: root.row.imageDecodeWidth
            sourceSize.height: root.row.imageDecodeHeight
        }

        RoundedImage {
            anchors.fill: parent
            visible: root.hasThumbnail && !root.showsVideo && poster.status === Image.Ready
            source: poster
            topLeftRadius: root.cornerTopLeft
            topRightRadius: root.cornerTopRight
            bottomLeftRadius: root.cornerBottomLeft
            bottomRightRadius: root.cornerBottomRight
        }

        // Placeholder for a clip that arrived without a thumbnail, so the slot is
        // never a hole in the wallpaper.
        Kirigami.ShadowedRectangle {
            anchors.fill: parent
            visible: !root.showsVideo
                     && !(root.hasThumbnail && poster.status === Image.Ready)
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
            // Underneath the poster and the placeholder plate. It has to be
            // visible to render at all (below), but until it has frames it is
            // an empty black rectangle, and covering the thumbnail with one
            // while the decoder opens is worse than showing nothing new.
            z: -1
            // Visible from the moment a decoder is wanted, not from the moment
            // there is a frame. An invisible QQuickFramebufferObject is never
            // rendered, so it never creates mpv's render context, so mpv never
            // decodes, so there is never a frame: gating this on the frame
            // deadlocks the two against each other. There is nothing to see
            // during the gap anyway, because the surface has no frames to draw.
            // Hiding it once it does is the ShaderEffectSource's job below.
            visible: root.engaged
            messageId: root.row.messageId
            source: root.playbackSource
            startPosition: root.resumeAt
            lane: root.isGif ? Whatevr.VideoPlayback.Animated : Whatevr.VideoPlayback.Exclusive
            engaged: root.engaged
            playing: root.wantsRun
            muted: root.isGif || root.userMuted
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
    MediaOverlayButton {
        id: primaryButton

        anchors.centerIn: parent
        visible: !root.row.selectionModeActive
                 && (root.phase === "idle" || root.phase === "needsDownload")
        diameter: Kirigami.Units.gridUnit * 2.6
        iconName: root.phase === "needsDownload"
            ? "folder-download-symbolic"
            : "media-playback-start-symbolic"
        text: root.phase === "needsDownload"
            ? Whatevr.I18n.i18nc("@action:button", "Download")
            : (root.resumeAt > 0
               ? Whatevr.I18n.i18nc("@action:button", "Resume")
               : Whatevr.I18n.i18nc("@action:button", "Play"))
        onClicked: root.activate()
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
                    text: Whatevr.I18n.i18nc("@info", "Could not play")
                    color: "white"
                    font.pointSize: Kirigami.Theme.smallFont.pointSize
                }

                MediaOverlayButton {
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

    // Duration and kind chip, the way WhatsApp labels a clip before you open it.
    // It steps aside once the transport strip is up, which carries the clock.
    Rectangle {
        anchors.left: parent.left
        anchors.top: parent.top
        anchors.margins: Kirigami.Units.smallSpacing
        // The GIF label stays put; the clock steps aside once the strip is up,
        // because the strip carries one of its own.
        visible: !root.isVideoNote
                 && (root.isGif || (root.totalSeconds > 0 && !root.showsTransport))
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

    // ---- Transport ----

    // A rectangular clip gets a strip along the bottom: where it is, how much
    // is left, sound, and the way to full screen. Always on while the clip is
    // up rather than fading on a timer, because a control you have to make
    // reappear is a control you have to know about first.
    Loader {
        id: transportLoader

        objectName: "videoBubble.transportLoader"
        anchors.left: picture.left
        anchors.right: picture.right
        anchors.bottom: picture.bottom
        anchors.leftMargin: Kirigami.Units.smallSpacing
        anchors.rightMargin: root.transportRightMargin
        anchors.bottomMargin: Kirigami.Units.smallSpacing
        active: root.showsTransport && !root.isVideoNote

        sourceComponent: Rectangle {
            objectName: "videoBubble.transportStrip"
            height: stripRow.implicitHeight + Kirigami.Units.smallSpacing
            radius: Kirigami.Units.cornerRadius
            color: Qt.alpha("black", 0.55)

            RowLayout {
                id: stripRow

                anchors.fill: parent
                anchors.leftMargin: Kirigami.Units.smallSpacing
                anchors.rightMargin: Kirigami.Units.smallSpacing
                spacing: Kirigami.Units.smallSpacing

                MediaOverlayButton {
                    diameter: Kirigami.Units.gridUnit * 1.4
                    iconName: root.phase === "playing"
                        ? "media-playback-pause-symbolic"
                        : "media-playback-start-symbolic"
                    text: root.phase === "playing"
                        ? Whatevr.I18n.i18nc("@action:button", "Pause")
                        : Whatevr.I18n.i18nc("@action:button", "Play")
                    onClicked: root.activate()
                }

                Controls.Slider {
                    id: inlineSeek

                    Layout.fillWidth: true
                    from: 0
                    to: Math.max(root.totalSeconds, 1)
                    // Seek on release rather than on every pixel of the drag:
                    // an absolute seek per mouse move makes the decoder thrash.
                    onPressedChanged: if (!pressed) surface.seek(value)

                    // Written as a Binding rather than the self-referential
                    // `value: pressed ? value : surface.position`, which reads
                    // its own value and is a binding loop.
                    Binding {
                        target: inlineSeek
                        property: "value"
                        value: surface.position
                        when: !inlineSeek.pressed
                        restoreMode: Binding.RestoreNone
                    }
                }

                Controls.Label {
                    text: MediaFormat.clockTime(root.remainingSeconds)
                    color: "white"
                    font.pointSize: Kirigami.Theme.smallFont.pointSize
                }

                MediaOverlayButton {
                    // A GIF is silent by definition, so there is nothing here
                    // to toggle. The slot closes rather than showing a dead
                    // control.
                    visible: !root.isGif
                    diameter: Kirigami.Units.gridUnit * 1.4
                    iconName: root.userMuted ? "audio-volume-muted-symbolic" : "audio-volume-high-symbolic"
                    text: root.userMuted
                        ? Whatevr.I18n.i18nc("@action:button", "Unmute")
                        : Whatevr.I18n.i18nc("@action:button", "Mute")
                    onClicked: root.userMuted = !root.userMuted
                }

                MediaOverlayButton {
                    diameter: Kirigami.Units.gridUnit * 1.4
                    iconName: "view-fullscreen-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button", "Full screen")
                    onClicked: root.openFullScreen()
                }
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
