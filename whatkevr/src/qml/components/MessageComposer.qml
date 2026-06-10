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
    property real composerOverlayX: 0
    property real composerOverlayY: 0
    property real composerOverlayWidth: 0
    property real composerOverlayHeight: 0
    property string replyToMessageId: ""
    property string replyToSenderName: ""
    property string replyToText: ""
    property string replyToMediaKind: ""
    property string replyToMediaMimeType: ""
    property bool replyToOutgoing: false
    readonly property bool replying: replyToMessageId.length > 0

    signal sendTextRequested(string text, string replyToMessageId)
    signal sendImageRequested(string fileUrl, string caption, string replyToMessageId)
    signal composingChanged(bool composing)
    signal clearReplyRequested()
    signal replyConsumed()

    padding: Kirigami.Units.smallSpacing

    function inputPlainText() {
        return input.getText(0, input.length).trim()
    }

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
        const text = root.inputPlainText()
        if (!root.enabledForChat || text.length === 0 || root.sending) {
            return
        }
        root.setComposing(false)
        root.sendTextRequested(text, root.replyToMessageId)
        root.replyConsumed()
        input.clear()
    }

    function forceInputFocus() {
        if (root.visible && input.enabled) {
            input.forceActiveFocus(Qt.OtherFocusReason)
        }
    }

    function focusAndInsertText(text, focusInput) {
        if (!root.visible || !input.enabled || text.length === 0) {
            return
        }

        if (focusInput === undefined || focusInput) {
            input.forceActiveFocus(Qt.OtherFocusReason)
        }
        input.insert(input.cursorPosition, text)
    }

    function updateComposerOverlayRect() {
        if (!Overlay.overlay) {
            return
        }

        const pos = root.mapToItem(Overlay.overlay, 0, 0)
        root.composerOverlayX = pos.x
        root.composerOverlayY = pos.y
        root.composerOverlayWidth = root.width
        root.composerOverlayHeight = root.height
    }

    function positionEmojiPicker() {
        if (!Overlay.overlay) {
            return false
        }

        root.updateComposerOverlayRect()

        const margin = Kirigami.Units.smallSpacing
        // Minimums shrink with the window so the picker still opens on narrow
        // (single-column) layouts instead of silently doing nothing.
        const minHeight = Math.min(Kirigami.Units.gridUnit * 12,
                                   Math.max(0, Overlay.overlay.height * 0.4))
        const maxHeight = Kirigami.Units.gridUnit * 24
        const minWidth = Math.min(Kirigami.Units.gridUnit * 20,
                                  Math.max(0, Overlay.overlay.width - margin * 2))
        const maxWidth = Kirigami.Units.gridUnit * 30
        let targetX = root.composerOverlayX + margin
        let availableWidth = Math.max(0,
                                      Math.min(root.composerOverlayWidth - margin * 2,
                                               Overlay.overlay.width - targetX - margin))
        if (availableWidth < minWidth) {
            // Composer narrower than the picker wants: span the window instead.
            targetX = margin
            availableWidth = Math.max(0, Overlay.overlay.width - margin * 2)
        }
        const availableAbove = root.composerOverlayY - margin * 2

        if (availableWidth < minWidth || availableAbove < minHeight) {
            return false
        }

        emojiPicker.width = Math.min(maxWidth, availableWidth)
        emojiPicker.height = Math.min(maxHeight, availableAbove)
        emojiPicker.x = Math.max(margin, Math.min(targetX, Overlay.overlay.width - emojiPicker.width - margin))
        emojiPicker.y = Math.max(margin, root.composerOverlayY - emojiPicker.height - margin)
        return true
    }

    function toggleEmojiPicker() {
        if (emojiPicker.opened) {
            emojiPicker.close()
            return
        }

        emojiPicker.prepareForOpen()
        if (!root.positionEmojiPicker()) {
            return
        }
        emojiPicker.open()
    }

    function handlePickerGeometryChanged() {
        if (emojiPicker.opened && !root.positionEmojiPicker()) {
            emojiPicker.close()
        }
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

    onEnabledForChatChanged: {
        if (!enabledForChat) {
            root.setComposing(false)
            emojiPicker.close()
            root.clearReplyRequested()
        }
    }
    onSendingChanged: {
        if (sending) {
            root.setComposing(false)
        }
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

        ReplyPreview {
            Layout.fillWidth: true
            visible: root.replying
            senderName: root.replyToSenderName
            body: root.replyToText
            mediaKind: root.replyToMediaKind
            mediaMimeType: root.replyToMediaMimeType
            outgoing: root.replyToOutgoing
            showCloseButton: true
            fillColor: Qt.alpha(Kirigami.Theme.textColor, 0.045)
            borderColor: Qt.alpha(Kirigami.Theme.textColor, 0.09)
            onCloseRequested: root.clearReplyRequested()
        }

        RowLayout {
            Layout.fillWidth: true
            spacing: Kirigami.Units.smallSpacing

            ToolButton {
                id: emojiButton

                text: Whatevr.I18n.i18nc("@action:button", "Choose emoji or sticker")
                display: AbstractButton.IconOnly
                checkable: true
                checked: emojiPicker.opened
                enabled: root.enabledForChat && !root.sending
                onClicked: root.toggleEmojiPicker()
                Layout.alignment: Qt.AlignVCenter

                contentItem: Text {
                    text: "☺"
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                    color: emojiButton.checked ? Kirigami.Theme.highlightColor : Kirigami.Theme.textColor
                    opacity: emojiButton.enabled ? 1 : 0.38
                    font.family: Kirigami.Theme.defaultFont.family
                    font.pixelSize: Math.round(Kirigami.Theme.defaultFont.pixelSize * 1.55)
                    font.weight: Font.DemiBold
                    renderType: Text.QtRendering
                }
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
                    textFormat: TextEdit.PlainText
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

                    Keys.onPressed: event => {
                        if (event.matches(StandardKey.Paste)) {
                            if (Whatevr.AppController.sendClipboardImage(root.inputPlainText(), root.replyToMessageId)) {
                                event.accepted = true
                                root.setComposing(false)
                                root.replyConsumed()
                            }
                            return
                        }

                        if (event.key === Qt.Key_Backspace
                                && event.modifiers === Qt.NoModifier
                                && input.cursorPosition > 0
                                && input.selectionStart === input.selectionEnd) {
                            const previous = Whatevr.AppController.previousGraphemeBoundary(input.text, input.cursorPosition)
                            input.remove(previous, input.cursorPosition)
                            event.accepted = true
                        }
                    }
                }
            }

            ToolButton {
                icon.name: "image-x-generic-symbolic"
                text: Whatevr.I18n.i18nc("@action:button", "Attach image")
                display: AbstractButton.IconOnly
                enabled: root.enabledForChat && !root.sending
                onClicked: imageDialog.open()
                Layout.alignment: Qt.AlignVCenter
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

    ExpressionPicker {
        id: emojiPicker

        parent: Overlay.overlay
        z: 10001
        replyToMessageId: root.replyToMessageId
        onEmojiSelected: emoji => root.focusAndInsertText(emoji, false)
        onStickerChosen: keepOpen => {
            root.replyConsumed()
            if (!keepOpen) {
                emojiPicker.close()
                root.forceInputFocus()
            }
        }
    }

    Item {
        id: emojiInteractionGuard

        parent: Overlay.overlay
        anchors.fill: parent
        visible: emojiPicker.opened
        z: 10000

        readonly property real pickerBottom: emojiPicker.y + emojiPicker.height
        readonly property real pickerRight: emojiPicker.x + emojiPicker.width
        readonly property real composerBottom: root.composerOverlayY + root.composerOverlayHeight
        readonly property real composerRight: root.composerOverlayX + root.composerOverlayWidth

        function closePicker(mouse) {
            emojiPicker.close()
            mouse.accepted = true
        }

        MouseArea {
            x: 0
            y: 0
            width: parent.width
            height: Math.max(0, emojiPicker.y)
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }

        MouseArea {
            x: 0
            y: emojiPicker.y
            width: Math.max(0, emojiPicker.x)
            height: emojiPicker.height
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }

        MouseArea {
            x: emojiInteractionGuard.pickerRight
            y: emojiPicker.y
            width: Math.max(0, parent.width - emojiInteractionGuard.pickerRight)
            height: emojiPicker.height
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }

        MouseArea {
            x: 0
            y: emojiInteractionGuard.pickerBottom
            width: parent.width
            height: Math.max(0, root.composerOverlayY - emojiInteractionGuard.pickerBottom)
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }

        MouseArea {
            x: 0
            y: root.composerOverlayY
            width: Math.max(0, root.composerOverlayX)
            height: root.composerOverlayHeight
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }

        MouseArea {
            x: emojiInteractionGuard.composerRight
            y: root.composerOverlayY
            width: Math.max(0, parent.width - emojiInteractionGuard.composerRight)
            height: root.composerOverlayHeight
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }

        MouseArea {
            x: 0
            y: emojiInteractionGuard.composerBottom
            width: parent.width
            height: Math.max(0, parent.height - emojiInteractionGuard.composerBottom)
            hoverEnabled: true
            acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
            onPressed: mouse => emojiInteractionGuard.closePicker(mouse)
        }
    }

    onWidthChanged: root.handlePickerGeometryChanged()
    onHeightChanged: root.handlePickerGeometryChanged()

    Connections {
        target: Overlay.overlay
        function onWidthChanged() { root.handlePickerGeometryChanged() }
        function onHeightChanged() { root.handlePickerGeometryChanged() }
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
            root.sendImageRequested(file, root.inputPlainText(), root.replyToMessageId)
            root.replyConsumed()
        }
    }
}
