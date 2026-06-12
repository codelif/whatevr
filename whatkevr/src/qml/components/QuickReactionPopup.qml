pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami

// Floating quick-reaction bar shown from the hover button or right-click. Wraps
// QuickReactionBar in a rounded popup and relays the chosen emoji (or a request
// to open the full picker) with the target message id.
Popup {
    id: root

    property string messageId: ""
    // The viewer's existing reaction on this message, highlighted in the bar.
    property string currentEmoji: ""

    signal reacted(string messageId, string emoji)
    signal pickerRequested(string messageId, string currentEmoji)

    modal: false
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    padding: 0

    // anchorX/anchorY are in the parent's coordinates (the React button center
    // and top). The bar is centred on the anchor and floated just above it.
    function openFor(msgId, current, anchorX, anchorY) {
        messageId = msgId
        currentEmoji = current || ""
        open()
        const limit = parent ? parent.width : anchorX + width
        x = Math.round(Math.max(Kirigami.Units.smallSpacing,
                                Math.min(limit - width - Kirigami.Units.smallSpacing,
                                         anchorX - width / 2)))
        y = Math.round(Math.max(Kirigami.Units.smallSpacing,
                                anchorY - height - Kirigami.Units.smallSpacing))
    }

    background: Rectangle {
        Kirigami.Theme.inherit: false
        Kirigami.Theme.colorSet: Kirigami.Theme.View
        color: Kirigami.Theme.backgroundColor
        radius: Kirigami.Units.cornerRadius
        border.color: Qt.alpha(Kirigami.Theme.textColor, 0.16)
        border.width: 1
    }

    contentItem: QuickReactionBar {
        currentEmoji: root.currentEmoji
        onReacted: emoji => {
            root.reacted(root.messageId, emoji)
            root.close()
        }
        onPickerRequested: {
            root.pickerRequested(root.messageId, root.currentEmoji)
            root.close()
        }
    }
}
