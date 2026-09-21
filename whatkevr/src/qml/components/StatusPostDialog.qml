pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import Qt.labs.platform as Platform
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Posts text and media statuses. The backend stores the text background/font;
// media editing remains deliberately local to the selected file.
QQC2.Dialog {
    id: root

    title: Whatevr.I18n.i18nc("@title:window post a status", "New status")
    modal: true
    standardButtons: QQC2.Dialog.Close

    ColumnLayout {
        width: parent.width
        spacing: Kirigami.Units.largeSpacing

        QQC2.TextField {
            id: statusText

            Layout.fillWidth: true
            placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Type a status…")
        }

        QQC2.ComboBox {
            id: backgroundChoice
            Layout.fillWidth: true
            model: [
                {label: Whatevr.I18n.i18nc("@item:inlistbox status background", "Blue"), value: 0},
                {label: Whatevr.I18n.i18nc("@item:inlistbox status background", "Green"), value: 1},
                {label: Whatevr.I18n.i18nc("@item:inlistbox status background", "Purple"), value: 2},
                {label: Whatevr.I18n.i18nc("@item:inlistbox status background", "Orange"), value: 3}
            ]
            textRole: "label"
            visible: statusText.text.trim().length > 0
        }

        QQC2.ComboBox {
            id: fontChoice
            Layout.fillWidth: true
            model: [
                {label: Whatevr.I18n.i18nc("@item:inlistbox status font", "Classic"), value: 0},
                {label: Whatevr.I18n.i18nc("@item:inlistbox status font", "Bold"), value: 1},
                {label: Whatevr.I18n.i18nc("@item:inlistbox status font", "Typewriter"), value: 2}
            ]
            textRole: "label"
            visible: statusText.text.trim().length > 0
        }

        QQC2.Button {
            Layout.fillWidth: true
            text: Whatevr.I18n.i18nc("@action:button post a text status", "Post text status")
            icon.name: "document-send-symbolic"
            enabled: statusText.text.trim().length > 0
            onClicked: {
                Whatevr.ProtocolController.postStatusText(statusText.text,
                                                           backgroundChoice.model[backgroundChoice.currentIndex].value,
                                                           fontChoice.model[fontChoice.currentIndex].value)
                statusText.clear()
                root.close()
            }
        }

        QQC2.Button {
            Layout.fillWidth: true
            text: Whatevr.I18n.i18nc("@action:button post a media status", "Post photo, video, or audio…")
            icon.name: "camera-photo-symbolic"
            onClicked: photoDialog.open()
        }
    }

    Platform.FileDialog {
        id: photoDialog

        title: Whatevr.I18n.i18nc("@title:window", "Post a photo status")
        fileMode: Platform.FileDialog.OpenFile
        nameFilters: [Whatevr.I18n.i18nc("@item:inlistbox", "Media (*.png *.jpg *.jpeg *.webp *.mp4 *.mov *.webm *.ogg *.opus *.mp3 *.m4a)")]
        onAccepted: {
            Whatevr.ProtocolController.postStatusMedia(file, statusText.text)
            statusText.clear()
            root.close()
        }
    }
}
