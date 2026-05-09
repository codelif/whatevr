import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Qt.labs.platform as Platform
import org.kde.kirigami as Kirigami

Frame {
    id: root

    property bool enabledForChat: false
    property bool sending: false
    property string errorText: ""

    signal sendTextRequested(string text)
    signal sendImageRequested(string fileUrl, string caption)

    padding: Kirigami.Units.smallSpacing

    background: Rectangle {
        color: Kirigami.Theme.backgroundColor
        border.color: Qt.alpha(Kirigami.Theme.textColor, 0.10)
    }

    function submitText() {
        const text = input.text.trim()
        if (!root.enabledForChat || text.length === 0 || root.sending) {
            return
        }
        root.sendTextRequested(text)
        input.clear()
    }

    contentItem: ColumnLayout {
        spacing: Kirigami.Units.smallSpacing

        Kirigami.InlineMessage {
            Layout.fillWidth: true
            visible: root.errorText.length > 0
            type: Kirigami.MessageType.Error
            showCloseButton: false
            text: root.errorText
        }

        RowLayout {
            Layout.fillWidth: true
            spacing: Kirigami.Units.smallSpacing

            ToolButton {
                icon.name: "image-x-generic-symbolic"
                text: i18nc("@action:button", "Attach image")
                display: AbstractButton.IconOnly
                enabled: root.enabledForChat && !root.sending
                onClicked: imageDialog.open()
            }

            ScrollView {
                Layout.fillWidth: true
                Layout.preferredHeight: Math.min(input.implicitHeight + Kirigami.Units.smallSpacing,
                                                 Kirigami.Units.gridUnit * 6)
                Layout.minimumHeight: Kirigami.Units.gridUnit * 2.6
                clip: true
                ScrollBar.vertical.interactive: false
                ScrollBar.vertical.policy: ScrollBar.AsNeeded

                TextArea {
                    id: input

                    enabled: root.enabledForChat && !root.sending
                    placeholderText: root.enabledForChat
                                     ? i18nc("@info:placeholder", "Message")
                                     : i18nc("@info:placeholder", "Select a chat to message")
                    wrapMode: TextArea.Wrap
                    background: null
                    selectByMouse: true

                    Keys.onReturnPressed: event => {
                        if (event.modifiers & Qt.ShiftModifier) {
                            event.accepted = false
                            return
                        }
                        event.accepted = true
                        root.submitText()
                    }
                }
            }

            Button {
                icon.name: "document-send-symbolic"
                text: i18nc("@action:button", "Send")
                enabled: root.enabledForChat && !root.sending && input.text.trim().length > 0
                onClicked: root.submitText()
            }
        }
    }

    Platform.FileDialog {
        id: imageDialog

        title: i18nc("@title:window", "Attach image")
        nameFilters: [i18nc("@item:inlistbox", "Images (*.png *.jpg *.jpeg *.webp *.gif)")]
        fileMode: Platform.FileDialog.OpenFile
        onAccepted: root.sendImageRequested(file, input.text.trim())
    }
}
