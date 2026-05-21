import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Qt.labs.platform as Platform
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Frame {
    id: root

    property bool enabledForChat: false
    property bool sending: false
    property string errorText: ""

    signal sendTextRequested(string text)
    signal sendImageRequested(string fileUrl, string caption)
    signal composingChanged(bool composing)

    padding: Kirigami.Units.smallSpacing

    Kirigami.Theme.colorSet: Kirigami.Theme.View

    background: Rectangle {
        Kirigami.Theme.inherit: false
        Kirigami.Theme.colorSet: Kirigami.Theme.View
        color: Kirigami.Theme.backgroundColor

        Rectangle {
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.top: parent.top
            height: 1
            color: Qt.alpha(Kirigami.Theme.textColor, 0.10)
        }
    }

    function submitText() {
        const text = input.text.trim()
        if (!root.enabledForChat || text.length === 0 || root.sending) {
            return
        }
        root.setComposing(false)
        root.sendTextRequested(text)
        input.clear()
    }

    function setComposing(composing) {
        if (composing && (!root.enabledForChat || root.sending || input.text.trim().length === 0)) {
            composing = false
        }

        if (composingTimer.running !== composing) {
            composingTimer.running = composing
        }
        if (pauseTimer.running && !composing) {
            pauseTimer.stop()
        }
        root.composingChanged(composing)
    }

    function noteDraftActivity() {
        if (!root.enabledForChat || root.sending || input.text.trim().length === 0) {
            root.setComposing(false)
            return
        }

        root.setComposing(true)
        pauseTimer.restart()
    }

    onEnabledForChatChanged: if (!enabledForChat) root.setComposing(false)
    onSendingChanged: if (sending) root.setComposing(false)

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
                text: Whatevr.I18n.i18nc("@action:button", "Attach image")
                display: AbstractButton.IconOnly
                enabled: root.enabledForChat && !root.sending
                onClicked: imageDialog.open()
                Layout.alignment: Qt.AlignVCenter
            }

            ScrollView {
                Layout.fillWidth: true
                Layout.preferredHeight: Math.min(input.implicitHeight + Kirigami.Units.smallSpacing,
                                                 Kirigami.Units.gridUnit * 6)
                Layout.minimumHeight: Kirigami.Units.gridUnit * 2.6
                clip: true
                ScrollBar.vertical.interactive: false
                ScrollBar.vertical.policy: ScrollBar.AsNeeded
                background: Rectangle {
                    Kirigami.Theme.inherit: false
                    Kirigami.Theme.colorSet: Kirigami.Theme.View
                    color: Kirigami.Theme.backgroundColor
                }

                TextArea {
                    id: input

                    enabled: root.enabledForChat && !root.sending
                    placeholderText: root.enabledForChat
                                     ? Whatevr.I18n.i18nc("@info:placeholder", "Message")
                                     : Whatevr.I18n.i18nc("@info:placeholder", "Select a chat to message")
                    wrapMode: TextArea.Wrap
                    background: null
                    selectByMouse: true
                    verticalAlignment: TextEdit.AlignVCenter

                    onTextChanged: root.noteDraftActivity()

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

            ToolButton {
                icon.name: "document-send-symbolic"
                text: Whatevr.I18n.i18nc("@action:button", "Send")
                display: AbstractButton.IconOnly
                enabled: root.enabledForChat && !root.sending && input.text.trim().length > 0
                onClicked: root.submitText()
                Layout.alignment: Qt.AlignVCenter
            }
        }
    }

    Timer {
        id: composingTimer

        interval: 10000
        repeat: true
        onTriggered: root.noteDraftActivity()
    }

    Timer {
        id: pauseTimer

        interval: 5000
        repeat: false
        onTriggered: root.setComposing(false)
    }

    Platform.FileDialog {
        id: imageDialog

        title: Whatevr.I18n.i18nc("@title:window", "Attach image")
        nameFilters: [Whatevr.I18n.i18nc("@item:inlistbox", "Images (*.png *.jpg *.jpeg *.webp *.gif)")]
        fileMode: Platform.FileDialog.OpenFile
        onAccepted: {
            root.setComposing(false)
            root.sendImageRequested(file, input.text.trim())
        }
    }
}
