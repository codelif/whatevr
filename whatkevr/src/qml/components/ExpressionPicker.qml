pragma ComponentBehavior: Bound

import QtCore
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Combined emoji + sticker picker. One shared search field on top, the
// emoji/sticker pane in the middle, and a slim mode switch at the bottom.
// The sticker pane sits behind a lazy Loader so emoji-only users never pay
// for it; the last used mode is remembered across sessions.
Popup {
    id: root

    signal emojiSelected(string emoji)
    // Emitted after a sticker click was dispatched; keepOpen is true for
    // Ctrl+click multi-send.
    signal stickerChosen(bool keepOpen)

    property string replyToMessageId: ""
    property string mode: "emoji" // "emoji" | "stickers"
    readonly property bool stickerMode: mode === "stickers"

    width: Math.min(parent ? parent.width - Kirigami.Units.largeSpacing * 2 : Kirigami.Units.gridUnit * 26,
                    Kirigami.Units.gridUnit * 30)
    height: Math.min(parent ? parent.height - Kirigami.Units.largeSpacing * 4 : Kirigami.Units.gridUnit * 26,
                     Kirigami.Units.gridUnit * 24)
    modal: false
    focus: true
    closePolicy: Popup.CloseOnEscape
    padding: Kirigami.Units.smallSpacing

    Settings {
        id: pickerSettings
        category: "picker"
        property string lastMode: "emoji"
    }

    function prepareForOpen() {
        searchField.clear()
        setMode(pickerSettings.lastMode === "stickers" ? "stickers" : "emoji", true)
    }

    function setMode(newMode, preparing) {
        if (newMode !== "emoji" && newMode !== "stickers") {
            newMode = "emoji"
        }
        if (!preparing && root.mode === newMode) {
            return
        }
        if (!preparing && root.mode === "emoji" && newMode !== "emoji") {
            emojiPane.prepareForModeSwitch()
        } else if (!preparing && root.mode === "stickers" && newMode !== "stickers"
                   && stickerLoader.item) {
            stickerLoader.item.deactivate()
        }
        root.mode = newMode
        pickerSettings.lastMode = newMode
        if (!preparing) {
            searchField.clear()
        }
        if (newMode === "stickers") {
            stickerLoader.active = true
            if (stickerLoader.item) {
                stickerLoader.item.prepareForOpen()
            }
        } else {
            emojiPane.prepareForOpen()
        }
        searchField.forceActiveFocus(Qt.PopupFocusReason)
    }

    function toggleMode() {
        setMode(root.stickerMode ? "emoji" : "stickers", false)
    }

    function routeSearch(text) {
        if (root.stickerMode) {
            if (stickerLoader.item) {
                stickerLoader.item.applySearchFilter(text)
            }
        } else {
            emojiPane.applySearchFilter(text)
        }
    }

    onOpened: searchField.forceActiveFocus(Qt.PopupFocusReason)
    onClosed: {
        emojiPane.reset()
        if (stickerLoader.item)
            stickerLoader.item.deactivate()
    }

    Shortcut {
        sequences: ["Ctrl+Tab"]
        enabled: root.opened
        onActivated: root.toggleMode()
    }

    background: Rectangle {
        Kirigami.Theme.inherit: false
        Kirigami.Theme.colorSet: Kirigami.Theme.View
        color: Kirigami.Theme.backgroundColor
        radius: Kirigami.Units.cornerRadius
        border.color: Qt.alpha(Kirigami.Theme.textColor, 0.14)
    }

    contentItem: ColumnLayout {
        spacing: Kirigami.Units.smallSpacing

        RowLayout {
            Layout.fillWidth: true
            spacing: Kirigami.Units.smallSpacing

            TextField {
                id: searchField

                property bool preparing: false

                Layout.fillWidth: true
                placeholderText: root.stickerMode
                                 ? Whatevr.I18n.i18nc("@info:placeholder", "Search stickers")
                                 : Whatevr.I18n.i18nc("@info:placeholder", "Search emoji")
                inputMethodHints: Qt.ImhNoPredictiveText
                onTextChanged: root.routeSearch(text)

                Keys.onReturnPressed: event => {
                    if (root.stickerMode) {
                        if (stickerLoader.item) {
                            stickerLoader.item.sendFirstVisible(event)
                        }
                    } else {
                        emojiPane.insertFirstVisibleEmoji(event)
                    }
                }
                Keys.onEnterPressed: event => {
                    if (root.stickerMode) {
                        if (stickerLoader.item) {
                            stickerLoader.item.sendFirstVisible(event)
                        }
                    } else {
                        emojiPane.insertFirstVisibleEmoji(event)
                    }
                }
                Keys.onRightPressed: event => {
                    if (!root.stickerMode) {
                        emojiPane.focusGridFromSearch(0)
                    }
                    event.accepted = true
                }
                Keys.onLeftPressed: event => {
                    if (!root.stickerMode) {
                        emojiPane.focusGridFromSearch(0)
                    }
                    event.accepted = true
                }
                Keys.onUpPressed: event => {
                    if (!root.stickerMode) {
                        emojiPane.focusGridFromSearch(0)
                    }
                    event.accepted = true
                }
                Keys.onDownPressed: event => {
                    if (!root.stickerMode) {
                        emojiPane.focusGridFromSearch(0)
                    }
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

        StackLayout {
            Layout.fillWidth: true
            Layout.fillHeight: true
            currentIndex: root.stickerMode ? 1 : 0

            EmojiPane {
                id: emojiPane

                searchField: searchField
                onEmojiSelected: emoji => root.emojiSelected(emoji)
            }

            Loader {
                id: stickerLoader

                active: false

                sourceComponent: StickerPane {
                    searchField: searchField
                    replyToMessageId: root.replyToMessageId
                    onStickerChosen: keepOpen => root.stickerChosen(keepOpen)
                }
            }
        }

        // Bottom mode switch, styled like the category tabs above it.
        RowLayout {
            Layout.fillWidth: true
            spacing: Kirigami.Units.smallSpacing / 2

            Repeater {
                model: [
                    { key: "emoji", glyph: "☺", label: Whatevr.I18n.i18nc("@action:button", "Emoji") },
                    { key: "stickers", glyph: "🖼", label: Whatevr.I18n.i18nc("@action:button", "Stickers") }
                ]

                delegate: Item {
                    id: modeTab

                    required property var modelData
                    readonly property bool current: root.mode === modelData.key

                    Layout.fillWidth: true
                    Layout.preferredHeight: Kirigami.Units.gridUnit * 1.8
                    Accessible.role: Accessible.Button
                    Accessible.name: modelData.label

                    Rectangle {
                        anchors.fill: parent
                        radius: Kirigami.Units.cornerRadius
                        color: modeTab.current
                               ? Qt.alpha(Kirigami.Theme.highlightColor, 0.22)
                               : (modeHover.hovered ? Qt.alpha(Kirigami.Theme.textColor, 0.06) : "transparent")
                        border.color: modeTab.current
                                      ? Qt.alpha(Kirigami.Theme.highlightColor, 0.55)
                                      : "transparent"
                    }

                    Row {
                        anchors.centerIn: parent
                        spacing: Kirigami.Units.smallSpacing

                        Text {
                            text: modeTab.modelData.glyph
                            anchors.verticalCenter: parent.verticalCenter
                            color: modeTab.current ? Kirigami.Theme.highlightColor : Kirigami.Theme.textColor
                            font.pixelSize: Math.round(Kirigami.Theme.defaultFont.pixelSize * 1.2)
                            renderType: Text.QtRendering
                        }

                        Text {
                            text: modeTab.modelData.label
                            anchors.verticalCenter: parent.verticalCenter
                            color: modeTab.current ? Kirigami.Theme.highlightColor : Kirigami.Theme.textColor
                            font.weight: modeTab.current ? Font.DemiBold : Font.Normal
                            renderType: Text.QtRendering
                        }
                    }

                    HoverHandler {
                        id: modeHover
                    }

                    TapHandler {
                        onTapped: root.setMode(modeTab.modelData.key, false)
                    }
                }
            }
        }
    }
}
