pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Emoji-only picker for choosing a reaction. Mirrors ExpressionPicker's emoji
// half (search field + EmojiPane) but omits the sticker mode, since a reaction
// is always a single emoji.
Popup {
    id: root

    signal emojiSelected(string emoji)

    width: Math.min(parent ? parent.width - Kirigami.Units.largeSpacing * 2 : Kirigami.Units.gridUnit * 26,
                    Kirigami.Units.gridUnit * 24)
    height: Math.min(parent ? parent.height - Kirigami.Units.largeSpacing * 4 : Kirigami.Units.gridUnit * 22,
                     Kirigami.Units.gridUnit * 20)
    modal: false
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    padding: Kirigami.Units.smallSpacing

    function prepareForOpen() {
        searchField.clear()
        emojiPane.prepareForOpen()
    }

    onOpened: searchField.forceActiveFocus(Qt.PopupFocusReason)
    onClosed: emojiPane.reset()

    background: Rectangle {
        Kirigami.Theme.inherit: false
        Kirigami.Theme.colorSet: Kirigami.Theme.View
        color: Kirigami.Theme.backgroundColor
        radius: Kirigami.Units.cornerRadius
        border.color: Qt.alpha(Kirigami.Theme.textColor, 0.14)
        border.width: 1
    }

    contentItem: ColumnLayout {
        spacing: Kirigami.Units.smallSpacing

        RowLayout {
            Layout.fillWidth: true
            spacing: Kirigami.Units.smallSpacing

            TextField {
                id: searchField

                Layout.fillWidth: true
                placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Search emoji")
                inputMethodHints: Qt.ImhNoPredictiveText
                onTextChanged: emojiPane.applySearchFilter(text)

                Keys.onReturnPressed: event => emojiPane.insertFirstVisibleEmoji(event)
                Keys.onEnterPressed: event => emojiPane.insertFirstVisibleEmoji(event)
                Keys.onDownPressed: event => {
                    emojiPane.focusGridFromSearch(0)
                    event.accepted = true
                }
            }

            ToolButton {
                icon.name: "edit-clear-symbolic"
                text: Whatevr.I18n.i18nc("@action:button", "Clear search")
                display: AbstractButton.IconOnly
                enabled: searchField.text.length > 0
                onClicked: searchField.clear()
            }
        }

        EmojiPane {
            id: emojiPane

            Layout.fillWidth: true
            Layout.fillHeight: true
            searchField: searchField
            onEmojiSelected: emoji => root.emojiSelected(emoji)
        }
    }
}
