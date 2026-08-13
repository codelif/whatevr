pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Full-screen media viewer: a photo, or a video with transport controls.
//
// It is the same shell as the profile-picture lightbox, grown a player. Video
// goes through VideoSurface, so the viewer does not know or care which engine
// is live, and it plays on the pool's reserved decoder so opening a video full
// screen never evicts one from the conversation behind it.
QQC2.Popup {
    id: root

    /// Local file for a downloaded item, or empty while streaming.
    property string localPath: ""
    /// Streaming URL, used when the file is not complete yet.
    property url streamUrl
    property string messageId: ""
    /// The message's media kind: image, video, gif, video_note.
    property string kind: "image"
    property real durationSecs: 0
    /// Suggested name when saving, for kinds that carry one.
    property string fileName: ""

    readonly property bool isVideo: kind === "video" || kind === "gif" || kind === "video_note"
    readonly property bool isGif: kind === "gif"
    readonly property bool isVideoNote: kind === "video_note"
    /// A looping kind has no meaningful timeline: it is a moving picture.
    readonly property bool hasTimeline: isVideo && !isGif
    readonly property url mediaSource: localPath.length > 0
        ? Qt.resolvedUrl("file://" + localPath)
        : streamUrl
    readonly property bool hasFile: localPath.length > 0

    signal forwardRequested(string messageId)

    function showImage(path) {
        kind = "image"
        localPath = path
        streamUrl = ""
        messageId = ""
        fileName = ""
        open()
    }

    function showVideo(id, path, url, mediaKind, duration) {
        kind = mediaKind && mediaKind.length > 0 ? mediaKind : "video"
        messageId = id
        localPath = path
        streamUrl = url
        durationSecs = duration
        fileName = ""
        open()
    }

    function formatTime(seconds) {
        const whole = Math.max(0, Math.floor(seconds))
        const minutes = Math.floor(whole / 60)
        const rest = whole % 60
        return minutes + ":" + (rest < 10 ? "0" : "") + rest
    }

    function skip(seconds) {
        surface.seek(Math.max(0, surface.position + seconds))
    }

    function cycleSpeed() {
        const speeds = [1.0, 1.5, 2.0]
        const index = speeds.indexOf(surface.speed)
        surface.speed = speeds[(index + 1) % speeds.length]
    }

    function saveMedia() {
        if (hasFile) {
            saveRequested(localPath, kind, fileName)
        }
    }

    signal saveRequested(string localPath, string kind, string fileName)

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

    onClosed: {
        // Release the decoder as soon as the viewer goes away; a paused video
        // holding a core is the one thing the pool cannot afford.
        streamUrl = ""
        localPath = ""
        messageId = ""
        kind = "image"
        surface.speed = 1.0
    }

    background: Rectangle {
        color: Qt.rgba(0, 0, 0, 0.92)
    }

    contentItem: Item {
        id: viewerContent

        // Tapping the backdrop closes; tapping the media itself toggles play,
        // which is what every video player does.
        TapHandler {
            onTapped: root.close()
        }

        Image {
            anchors.centerIn: parent
            visible: !root.isVideo
            source: root.isVideo ? "" : root.mediaSource
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            cache: true
            width: Math.min(implicitWidth, parent.width - Kirigami.Units.gridUnit * 2)
            height: Math.min(implicitHeight, parent.height - Kirigami.Units.gridUnit * 2)
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

            VideoSurface {
                id: surface

                anchors.fill: parent
                // Always visible: the round presentation below hides it through
                // the ShaderEffectSource's hideSource, which keeps the decoder
                // attached and the frames flowing into the texture.
                messageId: root.messageId
                source: root.visible && root.isVideo ? root.mediaSource : ""
                playing: root.isVideo && playbackWanted
                muted: false
                loop: root.isGif || root.isVideoNote
                reserved: true

                property bool playbackWanted: true

                TapHandler {
                    onTapped: surface.playbackWanted = !surface.playbackWanted
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
            visible: root.hasFile
            width: actions.implicitWidth + Kirigami.Units.smallSpacing * 2
            height: actions.implicitHeight + Kirigami.Units.smallSpacing
            radius: Kirigami.Units.cornerRadius
            color: Qt.rgba(0, 0, 0, 0.6)

            RowLayout {
                id: actions

                anchors.centerIn: parent
                spacing: 0

                QQC2.ToolButton {
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
            visible: root.isVideo
            width: Math.min(parent.width - Kirigami.Units.gridUnit * 2, Kirigami.Units.gridUnit * 42)
            height: controls.implicitHeight + Kirigami.Units.largeSpacing
            radius: Kirigami.Units.cornerRadius
            color: Qt.rgba(0, 0, 0, 0.6)

            RowLayout {
                id: controls

                anchors.fill: parent
                anchors.margins: Kirigami.Units.smallSpacing
                spacing: Kirigami.Units.smallSpacing

                QQC2.ToolButton {
                    visible: root.hasTimeline
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
                    icon.name: surface.playbackWanted ? "media-playback-pause-symbolic" : "media-playback-start-symbolic"
                    text: surface.playbackWanted
                        ? Whatevr.I18n.i18nc("@action:button", "Pause")
                        : Whatevr.I18n.i18nc("@action:button", "Play")
                    display: QQC2.AbstractButton.IconOnly
                    QQC2.ToolTip.text: text
                    QQC2.ToolTip.visible: hovered
                    QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
                    Accessible.name: text
                    onClicked: surface.playbackWanted = !surface.playbackWanted
                }

                QQC2.ToolButton {
                    visible: root.hasTimeline
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
                    text: root.formatTime(surface.position)
                    color: "white"
                    font.pointSize: Kirigami.Theme.smallFont.pointSize
                }

                QQC2.Slider {
                    id: seekBar

                    visible: root.hasTimeline
                    Layout.fillWidth: true
                    from: 0
                    to: Math.max(surface.duration, root.durationSecs, 1)
                    // While dragging, the bar follows the finger; otherwise it
                    // follows playback.
                    value: pressed ? value : surface.position
                    onMoved: surface.seek(value)

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
                    text: root.formatTime(Math.max(surface.duration, root.durationSecs))
                    color: "white"
                    font.pointSize: Kirigami.Theme.smallFont.pointSize
                }

                QQC2.AbstractButton {
                    visible: root.hasTimeline
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
                    from: 0
                    to: 100
                    value: surface.volume
                    onMoved: {
                        surface.volume = value
                        surface.muted = value <= 0
                    }
                }
            }
        }

        // Keyboard transport, matching what a video player is expected to do.
        Keys.onPressed: event => {
            switch (event.key) {
            case Qt.Key_Space:
                surface.playbackWanted = !surface.playbackWanted
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
