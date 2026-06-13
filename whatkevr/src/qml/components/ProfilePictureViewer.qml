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
    readonly property string imageSource: localPath.length > 0 ? "file://" + localPath : ""

    function showImage(path) {
        localPath = path
        open()
    }

    parent: QQC2.Overlay.overlay
    modal: true
    dim: false
    padding: 0
    x: 0
    y: 0
    width: parent ? parent.width : 0
    height: parent ? parent.height : 0
    closePolicy: QQC2.Popup.CloseOnEscape | QQC2.Popup.CloseOnPressOutside

    background: Rectangle {
        color: Qt.rgba(0, 0, 0, 0.88)
    }

    contentItem: Item {
        TapHandler {
            onTapped: root.close()
        }

        Image {
            anchors.centerIn: parent
            source: root.imageSource
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            cache: true
            width: Math.min(implicitWidth, parent.width - Kirigami.Units.gridUnit * 2)
            height: Math.min(implicitHeight, parent.height - Kirigami.Units.gridUnit * 2)

            QQC2.BusyIndicator {
                anchors.centerIn: parent
                running: parent.status === Image.Loading
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
