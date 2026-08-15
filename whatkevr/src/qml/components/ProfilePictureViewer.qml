pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Full-screen profile-picture viewer. Opened with a local image path; fills the
// window overlay with a dimmed backdrop and the picture fitted inside. Tapping
// the backdrop or the close button dismisses it.
QQC2.Popup {
    id: root

    property string localPath: ""
    readonly property url imageSource: Whatevr.ProtocolController.localFileUrl(localPath)

    function showImage(path) {
        localPath = path
        open()
    }

    parent: QQC2.Overlay.overlay
    modal: true
    // A focused popup consumes Escape at the popup layer (per the app's
    // escape-handling convention), so closing the viewer never propagates ESC
    // to ConversationPane's close-chat fallback.
    focus: true
    dim: false
    padding: 0
    x: 0
    y: 0
    width: parent ? parent.width : 0
    height: parent ? parent.height : 0
    // Must stay above the Kirigami.Dialog it is opened from and above the emoji
    // picker overlay (z: 10001 in MessageComposer) so the fullscreen image
    // covers everything.
    z: 10002
    closePolicy: QQC2.Popup.CloseOnEscape | QQC2.Popup.CloseOnPressOutside

    background: Rectangle {
        color: Qt.rgba(0, 0, 0, 0.88)
    }

    contentItem: Item {
        TapHandler {
            onTapped: root.close()
        }

        Image {
            id: picture

            // Fit the viewport, upscaling a small avatar to something
            // viewable, but no further than 2x: beyond that it is only blur.
            readonly property real fitScale: implicitWidth > 0 && implicitHeight > 0
                ? Math.min((parent.width - Kirigami.Units.gridUnit * 2) / implicitWidth,
                           (parent.height - Kirigami.Units.gridUnit * 2) / implicitHeight,
                           2)
                : 1

            anchors.centerIn: parent
            source: root.imageSource
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            cache: true
            width: implicitWidth * fitScale
            height: implicitHeight * fitScale

            QQC2.BusyIndicator {
                anchors.centerIn: parent
                running: picture.status === Image.Loading
                visible: running
            }
        }

        QQC2.ToolButton {
            anchors.top: parent.top
            anchors.right: parent.right
            anchors.margins: Kirigami.Units.largeSpacing
            icon.name: "dialog-close-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Close")
            display: QQC2.AbstractButton.IconOnly
            onClicked: root.close()
        }
    }
}
