pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import Qt.labs.platform as Platform
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Full-screen profile-picture viewer. Opened with a local image path; fills the
// window overlay with a dimmed backdrop and the picture fitted inside. Tapping
// the backdrop or the close button dismisses it.
QQC2.Popup {
    id: root

    property string localPath: ""
    // Chat/contact name for the save dialog's suggested filename.
    property string suggestedName: ""
    readonly property url imageSource: Whatevr.ProtocolController.localFileUrl(localPath)

    function showImage(path, name) {
        localPath = path
        suggestedName = name ?? ""
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
        // A MouseArea, not a TapHandler: a handler watches presses without
        // consuming them, and a press inside a full-screen popup is not
        // outside anything, so modality does not stop it either. The clicks
        // went through to whatever was behind the lightbox.
        MouseArea {
            anchors.fill: parent
            acceptedButtons: Qt.AllButtons

            onClicked: mouse => {
                if (mouse.button === Qt.LeftButton) {
                    root.close()
                }
            }
            onWheel: wheel => wheel.accepted = true
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
            id: closeButton

            anchors.top: parent.top
            anchors.right: parent.right
            anchors.margins: Kirigami.Units.largeSpacing
            icon.name: "dialog-close-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Close")
            display: QQC2.AbstractButton.IconOnly
            onClicked: root.close()
        }

        // Save the picture the viewer was opened with. The daemon already
        // fetched it to its avatar cache; copying it out is a local file
        // operation, so a plain save dialog beats a media.save round trip.
        QQC2.ToolButton {
            anchors.top: closeButton.top
            anchors.right: closeButton.left
            anchors.rightMargin: Kirigami.Units.smallSpacing
            icon.name: "document-save-symbolic"
            text: Whatevr.I18n.i18nc("@action:button save the profile picture", "Save as…")
            display: QQC2.AbstractButton.IconOnly
            enabled: root.localPath.length > 0
            onClicked: saveDialog.openFor()
        }

        Platform.FileDialog {
            id: saveDialog

            title: Whatevr.I18n.i18nc("@title:window save the profile picture", "Save profile picture")
            fileMode: Platform.FileDialog.SaveFile

            // Prefill the chat/contact name (MessageView.saveMediaDialog
            // pattern): the cache filename is an id, useless in a file
            // manager.
            function openFor() {
                let base = root.suggestedName.trim().replace(/\//g, "_")
                if (base.length === 0) {
                    base = Whatevr.I18n.i18nc("@info default profile picture filename", "profile-picture")
                }
                const dot = root.localPath.lastIndexOf(".")
                const extension = dot > 0 ? root.localPath.substring(dot) : ".jpg"
                const preferred = Whatevr.Settings.mediaSaveDirectory
                const directory = preferred.length > 0
                    ? preferred
                    : Platform.StandardPaths.writableLocation(Platform.StandardPaths.PicturesLocation)
                currentFile = directory + "/" + base + extension
                open()
            }

            onAccepted: Whatevr.ProtocolController.saveMediaAs(root.localPath, file)
        }
    }
}
