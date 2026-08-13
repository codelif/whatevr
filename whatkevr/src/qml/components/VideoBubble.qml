// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as Controls

import org.kde.kirigami as Kirigami

import Whatevr as Whatevr

/**
 * Video, GIF and video-note bubbles.
 *
 * All three are the same thing on the wire and differ only in presentation:
 * a video plays inline and opens full screen on a second tap, a GIF loops muted
 * on its own, and a video note is a bare circle with a progress ring. Playback
 * goes through VideoSurface so neither this file nor MediaViewer knows which
 * engine is live.
 *
 * A bubble only holds a decoder while it is actually playing; otherwise it is
 * the thumbnail the message arrived with, which is also what it shows while a
 * fling is in progress or when the player pool is exhausted.
 */
Item {
    id: root

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

    // GIFs and video notes play by themselves once they are on screen and the
    // list has settled; a full video waits to be asked.
    readonly property bool wantsAutoplay: (isGif || isVideoNote)
                                          && Whatevr.Settings.autoplayInlineMedia
                                          && row.activeInViewport
                                          && !(row.fastFlicking && Whatevr.Settings.pausePlaybackWhileScrolling)
                                          && !row.pooled
                                          && !userPaused
    /// A plain video the user started here, in the bubble.
    property bool userStarted: false
    /// A looping kind the user tapped to stop.
    property bool userPaused: false
    /// Video notes and GIFs loop silently until the user asks for sound.
    property bool userUnmuted: false
    readonly property bool loops: isGif ? Whatevr.Settings.loopGifs : isVideoNote
    readonly property bool shouldPlay: (wantsAutoplay || userStarted) && playbackSource.toString().length > 0
    readonly property bool playingInline: surface.showingVideo

    // Local file first; while it is still downloading the daemon can stream it.
    property url streamUrl
    readonly property url playbackSource: hasFile
        ? Qt.resolvedUrl("file://" + row.mediaLocalPath)
        : streamUrl

    /**
     * A single tap.
     *
     * A looping kind (GIF, video note) toggles sound and pause where it sits,
     * because it is already playing and opening it full screen on every stray
     * tap would be hostile. A plain video starts inline on the first tap and
     * goes full screen on the next one, so one gesture escalates.
     */
    function activate() {
        if (row.messageId.length === 0)
            return
        // Nothing to play yet, whatever the kind: the first tap fetches it.
        // Without this a video note that autoplay never reached had no way to
        // be asked for at all, since the branch below only toggles sound.
        if (!hasFile && !playingInline) {
            requestPlayback()
            return
        }
        if (isVideoNote || isGif) {
            if (!userUnmuted && !userPaused) {
                userUnmuted = true
                return
            }
            userPaused = !userPaused
            if (!userPaused)
                userUnmuted = true
            return
        }
        if (playingInline) {
            openFullScreen()
            return
        }
        userStarted = true
    }

    /// Hands the message to the full-screen viewer, streaming URL and all, so a
    /// clip that is still downloading can still be opened.
    function openFullScreen() {
        row.videoActivated(row.messageId,
                           row.mediaLocalPath,
                           root.streamUrl.toString(),
                           row.mediaKind,
                           row.mediaDurationSecs)
    }

    /// Starts a stream if we have no file yet, falling back to an ordinary
    /// download when the daemon says this message cannot be streamed.
    function requestPlayback() {
        if (hasFile || row.mediaDownloading)
            return
        if (!Whatevr.Settings.streamWhileDownloading) {
            Whatevr.ProtocolController.downloadMessageMedia(row.messageId)
            return
        }
        Whatevr.ProtocolController.streamMessageMedia(row.messageId)
    }

    onWantsAutoplayChanged: {
        if (wantsAutoplay && !hasFile && streamUrl.toString().length === 0)
            requestPlayback()
    }

    Connections {
        target: Whatevr.ProtocolController

        function onMediaStreamReady(messageId, url) {
            if (messageId !== root.row.messageId)
                return
            root.streamUrl = url
            if (!root.isGif && !root.isVideoNote)
                root.userStarted = true
        }

        function onMediaStreamFailed(messageId) {
            if (messageId !== root.row.messageId)
                return
            // Streaming is an optimisation, not a requirement: fall back to
            // fetching the whole file and playing it from disk.
            Whatevr.ProtocolController.downloadMessageMedia(messageId)
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

            anchors.fill: parent
            visible: false
            source: root.hasThumbnail && !root.row.pooled
                ? Qt.resolvedUrl("file://" + root.row.mediaThumbnailLocalPath)
                : ""
            fillMode: Image.PreserveAspectCrop
            asynchronous: true
            cache: true
            sourceSize.width: root.row.thumbnailDecodeWidth
            sourceSize.height: root.row.thumbnailDecodeHeight
        }

        RoundedImage {
            anchors.fill: parent
            visible: root.hasThumbnail && !root.playingInline && poster.status === Image.Ready
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
            visible: !root.playingInline
                     && !(root.hasThumbnail && poster.status === Image.Ready)
            color: Qt.alpha(Kirigami.Theme.textColor, 0.08)
            corners.topLeftRadius: root.cornerTopLeft
            corners.topRightRadius: root.cornerTopRight
            corners.bottomLeftRadius: root.cornerBottomLeft
            corners.bottomRightRadius: root.cornerBottomRight
        }

        VideoSurface {
            id: surface

            anchors.fill: parent
            // The live frames go through the rounding shader below, so the
            // surface must not also draw into the scene. Hiding it is the
            // ShaderEffectSource's job (hideSource), not a `visible: false`
            // here: an item that is invisible on its own account has no reason
            // to keep a decoder attached, and both engines have been known to
            // read that as "nothing to draw".
            visible: root.playingInline
            messageId: root.row.messageId
            source: root.shouldPlay ? root.playbackSource : ""
            playing: root.shouldPlay
            muted: (root.isGif || root.isVideoNote) && !root.userUnmuted
            loop: root.loops
            readonly property bool showingVideo: active && playing
        }

        // One layer, alive only while this bubble is actually decoding.
        Loader {
            anchors.fill: parent
            active: root.playingInline

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

    // ---- Video-note chrome ----

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
        progress: surface.duration > 0
            ? Math.min(1, surface.position / surface.duration)
            : 0
        // Read against the wallpaper, not just against the picture: the note
        // draws no plate, so a fainter track disappeared into the doodle.
        trackColor: Qt.alpha(Kirigami.Theme.textColor, 0.38)
        fillColor: Whatevr.Palette.highlight
    }

    Controls.ToolButton {
        id: muteButton

        // Only while it is actually running: on a still circle there is no
        // sound to toggle, and the button read as a smudge on the rim.
        visible: root.isVideoNote && root.playingInline && !root.row.selectionModeActive
        // On the circle's lower-right diagonal rather than the corner of its
        // bounding box, which for a circle is empty space outside the picture.
        anchors.horizontalCenter: picture.horizontalCenter
        anchors.verticalCenter: picture.verticalCenter
        anchors.horizontalCenterOffset: Math.round(root.circleRadius * 0.56)
        anchors.verticalCenterOffset: Math.round(root.circleRadius * 0.56)
        implicitWidth: Kirigami.Units.gridUnit * 1.6
        implicitHeight: implicitWidth
        display: Controls.AbstractButton.IconOnly
        icon.name: root.userUnmuted ? "audio-volume-high-symbolic" : "audio-volume-muted-symbolic"
        text: root.userUnmuted
            ? Whatevr.I18n.i18nc("@action:button", "Mute")
            : Whatevr.I18n.i18nc("@action:button", "Unmute")
        Controls.ToolTip.text: text
        Controls.ToolTip.visible: hovered
        Controls.ToolTip.delay: Kirigami.Units.toolTipDelay
        Accessible.name: text
        onClicked: root.userUnmuted = !root.userUnmuted

        background: Rectangle {
            radius: width / 2
            color: Qt.alpha("black", muteButton.hovered ? 0.6 : 0.45)
        }
    }

    // ---- Shared chrome ----

    // Play affordance, hidden once something is actually playing.
    Rectangle {
        anchors.centerIn: parent
        visible: !root.playingInline && !root.row.mediaDownloading
        width: Kirigami.Units.gridUnit * 2.6
        height: width
        radius: width / 2
        color: Qt.alpha("black", 0.45)

        Kirigami.Icon {
            anchors.centerIn: parent
            width: Kirigami.Units.iconSizes.medium
            height: width
            source: "media-playback-start-symbolic"
            color: "white"
        }
    }

    // Duration and kind chip, the way WhatsApp labels a clip before you open it.
    Rectangle {
        anchors.left: parent.left
        anchors.top: parent.top
        anchors.margins: Kirigami.Units.smallSpacing
        visible: !root.isVideoNote && (root.isGif || root.row.mediaDurationSecs > 0)
        width: chipLabel.implicitWidth + Kirigami.Units.smallSpacing * 2
        height: chipLabel.implicitHeight + Kirigami.Units.smallSpacing
        radius: Kirigami.Units.cornerRadius
        color: Qt.alpha("black", 0.55)

        Controls.Label {
            id: chipLabel

            anchors.centerIn: parent
            text: {
                if (root.isGif)
                    return Whatevr.I18n.i18nc("@label animated image", "GIF")
                const whole = Math.max(0, Math.floor(root.row.mediaDurationSecs))
                const minutes = Math.floor(whole / 60)
                const rest = whole % 60
                return minutes + ":" + (rest < 10 ? "0" : "") + rest
            }
            color: "white"
            font.pointSize: Kirigami.Theme.smallFont.pointSize
        }
    }

    // A clip that has to be fetched before it can play says so, rather than
    // hiding a download behind a play glyph.
    Controls.ToolButton {
        id: downloadButton

        // A circle has no corner to put this in, and the centred play glyph is
        // already the affordance there: tapping a video note fetches it.
        visible: !root.isVideoNote && !root.hasFile
                 && !root.row.mediaDownloading && !root.row.selectionModeActive
        anchors.right: parent.right
        anchors.top: parent.top
        anchors.margins: Kirigami.Units.smallSpacing
        implicitWidth: Kirigami.Units.gridUnit * 1.8
        implicitHeight: implicitWidth
        display: Controls.AbstractButton.IconOnly
        icon.name: "folder-download-symbolic"
        text: Whatevr.I18n.i18nc("@action:button", "Download")
        Controls.ToolTip.text: text
        Controls.ToolTip.visible: hovered
        Controls.ToolTip.delay: Kirigami.Units.toolTipDelay
        Accessible.name: text
        onClicked: Whatevr.ProtocolController.downloadMessageMedia(root.row.messageId)

        background: Rectangle {
            radius: width / 2
            color: Qt.alpha("black", downloadButton.hovered ? 0.6 : 0.45)
        }
    }

    ProgressCircle {
        anchors.centerIn: parent
        visible: root.row.mediaDownloading && root.row.mediaDownloadProgress >= 0
        progress: Math.max(0, root.row.mediaDownloadProgress)
        width: Kirigami.Units.gridUnit * 2
        height: width
    }

    Controls.BusyIndicator {
        anchors.centerIn: parent
        visible: root.row.mediaDownloading && root.row.mediaDownloadProgress < 0
        running: visible
    }

    MediaDragArea {
        anchors.fill: parent
        z: -1
        localPath: root.row.mediaLocalPath
        blocked: root.row.selectionModeActive
    }

    TapHandler {
        enabled: !root.row.selectionModeActive
        exclusiveSignals: TapHandler.SingleTap | TapHandler.DoubleTap
        onSingleTapped: {
            root.activate()
            root.row.conversationFocusRequested()
        }
        // A looping kind has no second-tap escalation, so the double tap is how
        // it reaches the viewer.
        onDoubleTapped: {
            if (root.isGif || root.isVideoNote)
                root.openFullScreen()
        }
    }
}
