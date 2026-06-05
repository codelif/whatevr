pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Popup {
    id: root

    signal emojiSelected(string emoji)

    property var activeAlternates: []
    property string activeBaseEmoji: ""
    property string hoveredEmojiInfo: ""
    property bool preparing: false
    property bool animatingGridChange: false
    readonly property real cellSize: Kirigami.Units.gridUnit * 2.2
    readonly property string emojiFontFamily: Whatevr.AppController.emojiModel.emojiFontFamily

    width: Math.min(parent ? parent.width - Kirigami.Units.largeSpacing * 2 : Kirigami.Units.gridUnit * 26,
                    Kirigami.Units.gridUnit * 30)
    height: Math.min(parent ? parent.height - Kirigami.Units.largeSpacing * 4 : Kirigami.Units.gridUnit * 26,
                     Kirigami.Units.gridUnit * 24)
    modal: false
    focus: true
    closePolicy: Popup.CloseOnEscape
    padding: Kirigami.Units.smallSpacing

    function categoryGlyph(group) {
        if (group === "Recent") {
            return "◷"
        }
        if (group.startsWith("Smileys")) {
            return "🙂"
        }
        if (group === "People") {
            return "🧑"
        }
        if (group.indexOf("Animals") >= 0) {
            return "🐻"
        }
        if (group.indexOf("Food") >= 0) {
            return "🍔"
        }
        if (group.indexOf("Travel") >= 0) {
            return "🚗"
        }
        if (group.indexOf("Activities") >= 0) {
            return "⚽"
        }
        if (group.indexOf("Objects") >= 0) {
            return "💡"
        }
        if (group.indexOf("Symbols") >= 0) {
            return "❤"
        }
        return "🏳"
    }

    function formatAliases(shortcodes, emoticons) {
        const names = shortcodes ? Array.from(shortcodes) : []
        const faces = emoticons ? Array.from(emoticons) : []
        return names.concat(faces).join("  ")
    }

    function applyFilterWithTransition(query, group, categoryIndex) {
        if (root.animatingGridChange) {
            Whatevr.AppController.emojiModel.setFilter(query, group)
            categoryList.currentIndex = categoryIndex
            emojiGrid.positionViewAtBeginning()
            return
        }

        root.animatingGridChange = true
        gridFadeOut.query = query
        gridFadeOut.group = group
        gridFadeOut.categoryIndex = categoryIndex
        gridFadeOut.start()
    }

    function applySearchFilter(query) {
        if (gridFadeOut.running) {
            gridFadeOut.stop()
        }
        root.animatingGridChange = false
        emojiGrid.opacity = 1
        categoryList.currentIndex = -1
        Whatevr.AppController.emojiModel.setFilter(query, "")
        emojiGrid.currentIndex = emojiGrid.count > 0 ? 0 : -1
        emojiGrid.positionViewAtBeginning()
    }

    function insertFirstVisibleEmoji(event) {
        const emoji = Whatevr.AppController.emojiModel.emojiAt(0)
        if (emoji.length === 0) {
            return
        }
        root.selectEmoji(emoji, searchField)
        event.accepted = true
    }

    function visibleColumnCount() {
        return Math.max(1, Math.floor(emojiGrid.width / Math.max(1, emojiGrid.cellWidth)))
    }

    function moveGridSelection(delta) {
        if (emojiGrid.count <= 0) {
            return
        }

        emojiGrid.currentIndex = Math.max(0, Math.min(emojiGrid.count - 1,
                                                       Math.max(0, emojiGrid.currentIndex) + delta))
        emojiGrid.positionViewAtIndex(emojiGrid.currentIndex, GridView.Contain)
        emojiGrid.forceActiveFocus(Qt.PopupFocusReason)
    }

    function focusGridFromSearch(delta) {
        if (emojiGrid.count <= 0) {
            return
        }

        if (emojiGrid.currentIndex < 0) {
            emojiGrid.currentIndex = 0
        }
        if (delta !== 0) {
            root.moveGridSelection(delta)
        } else {
            emojiGrid.positionViewAtIndex(emojiGrid.currentIndex, GridView.Contain)
            emojiGrid.forceActiveFocus(Qt.PopupFocusReason)
        }
    }

    function preferredOpenGroup() {
        const groups = Whatevr.AppController.emojiModel.groups
        if (groups.length === 0) {
            return ""
        }
        const recentIndex = groups.indexOf("Recent")
        return recentIndex >= 0 ? groups[recentIndex] : groups[0]
    }

    function prepareForOpen() {
        root.preparing = true
        alternatesPopup.close()
        root.activeAlternates = []
        root.activeBaseEmoji = ""
        root.hoveredEmojiInfo = ""
        searchField.clear()

        const group = root.preferredOpenGroup()
        Whatevr.AppController.emojiModel.setFilter("", group)
        categoryList.currentIndex = Math.max(0, Whatevr.AppController.emojiModel.groups.indexOf(group))
        emojiGrid.currentIndex = emojiGrid.count > 0 ? 0 : -1
        emojiGrid.positionViewAtBeginning()
        root.preparing = false
    }

    function selectEmoji(emoji, focusTarget) {
        if (emoji.length === 0) {
            return
        }
        Whatevr.AppController.addRecentEmoji(emoji)
        root.emojiSelected(emoji)
        if (focusTarget) {
            focusTarget.forceActiveFocus(Qt.PopupFocusReason)
        }
    }

    function showAlternates(baseEmoji, alternates, item) {
        if (!alternates || alternates.length === 0) {
            return
        }

        root.activeBaseEmoji = baseEmoji
        root.activeAlternates = alternates
        Qt.callLater(() => {
            alternatesPopup.x = Math.max(0, Math.min(item.mapToItem(root.contentItem, 0, 0).x,
                                                      root.contentItem.width - alternatesPopup.implicitWidth))
            alternatesPopup.y = Math.max(0, item.mapToItem(root.contentItem, 0, 0).y - alternatesPopup.implicitHeight - Kirigami.Units.smallSpacing)
            alternatesPopup.open()
        })
    }

    onOpened: searchField.forceActiveFocus(Qt.PopupFocusReason)
    onClosed: {
        alternatesPopup.close()
        root.hoveredEmojiInfo = ""
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

                Layout.fillWidth: true
                placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Search emoji")
                inputMethodHints: Qt.ImhNoPredictiveText
                onTextChanged: {
                    if (root.preparing) {
                        return
                    }
                    root.applySearchFilter(text)
                }

                Keys.onReturnPressed: event => root.insertFirstVisibleEmoji(event)
                Keys.onEnterPressed: event => root.insertFirstVisibleEmoji(event)
                Keys.onRightPressed: event => {
                    root.focusGridFromSearch(0)
                    event.accepted = true
                }
                Keys.onLeftPressed: event => {
                    root.focusGridFromSearch(0)
                    event.accepted = true
                }
                Keys.onUpPressed: event => {
                    root.focusGridFromSearch(0)
                    event.accepted = true
                }
                Keys.onDownPressed: event => {
                    root.focusGridFromSearch(0)
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

        ListView {
            id: categoryList

            Layout.fillWidth: true
            Layout.preferredHeight: Kirigami.Units.gridUnit * 2.35
            orientation: ListView.Horizontal
            clip: true
            interactive: contentWidth > width
            boundsBehavior: Flickable.StopAtBounds
            highlightMoveDuration: 80
            model: Whatevr.AppController.emojiModel.groups

            delegate: Item {
                id: categoryButton

                required property int index
                required property string modelData
                readonly property bool current: categoryList.currentIndex === index

                width: Math.max(Kirigami.Units.gridUnit * 2.2,
                                categoryList.width / Math.max(1, categoryList.count))
                height: categoryList.height
                Accessible.role: Accessible.Button
                Accessible.name: modelData

                Rectangle {
                    anchors.fill: parent
                    anchors.margins: Kirigami.Units.smallSpacing / 3
                    radius: Kirigami.Units.cornerRadius
                    color: categoryButton.current
                           ? Qt.alpha(Kirigami.Theme.highlightColor, 0.22)
                           : (categoryHover.hovered ? Qt.alpha(Kirigami.Theme.textColor, 0.06) : "transparent")
                    border.color: categoryButton.current
                                  ? Qt.alpha(Kirigami.Theme.highlightColor, 0.55)
                                  : "transparent"
                }

                Text {
                    anchors.centerIn: parent
                    text: root.categoryGlyph(categoryButton.modelData)
                    color: categoryButton.modelData === "Recent"
                           ? (categoryButton.current ? Kirigami.Theme.highlightColor : Kirigami.Theme.textColor)
                           : Kirigami.Theme.textColor
                    font.family: categoryButton.modelData === "Recent"
                                 ? Kirigami.Theme.defaultFont.family
                                 : (root.emojiFontFamily.length > 0 ? root.emojiFontFamily : Kirigami.Theme.defaultFont.family)
                    font.weight: categoryButton.modelData === "Recent" ? Font.DemiBold : Font.Normal
                    font.pixelSize: Math.round(Kirigami.Theme.defaultFont.pixelSize * 1.55)
                    renderType: Text.QtRendering
                }

                HoverHandler {
                    id: categoryHover
                }

                TapHandler {
                    onTapped: {
                        root.preparing = true
                        searchField.clear()
                        root.preparing = false
                        root.applyFilterWithTransition("", categoryButton.modelData, categoryButton.index)
                    }
                }
            }
        }

        Rectangle {
            Layout.fillWidth: true
            height: 1
            color: Qt.alpha(Kirigami.Theme.textColor, 0.10)
        }

        GridView {
            id: emojiGrid

            Layout.fillWidth: true
            Layout.fillHeight: true
            clip: true
            reuseItems: true
            cacheBuffer: root.cellSize * 8
            boundsBehavior: Flickable.StopAtBounds
            model: Whatevr.AppController.emojiModel
            cellWidth: Math.max(root.cellSize, Math.floor(width / Math.max(1, Math.floor(width / root.cellSize))))
            cellHeight: root.cellSize
            keyNavigationWraps: true
            highlightMoveDuration: 60
            // Clamp on count changes instead of binding currentIndex to itself
            // (a self-referential binding loop).
            onCountChanged: currentIndex = count > 0 ? Math.max(0, Math.min(currentIndex, count - 1)) : -1

            SequentialAnimation {
                id: gridFadeOut

                property string query: ""
                property string group: ""
                property int categoryIndex: -1

                NumberAnimation {
                    target: emojiGrid
                    property: "opacity"
                    to: 0.18
                    duration: 70
                    easing.type: Easing.OutQuad
                }
                ScriptAction {
                    script: {
                        Whatevr.AppController.emojiModel.setFilter(gridFadeOut.query, gridFadeOut.group)
                        categoryList.currentIndex = gridFadeOut.categoryIndex
                        emojiGrid.currentIndex = emojiGrid.count > 0 ? 0 : -1
                        emojiGrid.positionViewAtBeginning()
                    }
                }
                NumberAnimation {
                    target: emojiGrid
                    property: "opacity"
                    to: 1
                    duration: 105
                    easing.type: Easing.OutCubic
                }
                ScriptAction {
                    script: root.animatingGridChange = false
                }
            }

            Keys.onReturnPressed: event => {
                const emoji = Whatevr.AppController.emojiModel.emojiAt(emojiGrid.currentIndex)
                if (emoji.length > 0) {
                    root.selectEmoji(emoji, emojiGrid)
                    event.accepted = true
                }
            }
            Keys.onEnterPressed: event => {
                const emoji = Whatevr.AppController.emojiModel.emojiAt(emojiGrid.currentIndex)
                if (emoji.length > 0) {
                    root.selectEmoji(emoji, emojiGrid)
                    event.accepted = true
                }
            }
            Keys.onRightPressed: event => {
                root.moveGridSelection(1)
                event.accepted = true
            }
            Keys.onLeftPressed: event => {
                root.moveGridSelection(-1)
                event.accepted = true
            }
            Keys.onDownPressed: event => {
                root.moveGridSelection(root.visibleColumnCount())
                event.accepted = true
            }
            Keys.onUpPressed: event => {
                if (emojiGrid.currentIndex >= 0 && emojiGrid.currentIndex < root.visibleColumnCount()) {
                    searchField.forceActiveFocus(Qt.PopupFocusReason)
                } else {
                    root.moveGridSelection(-root.visibleColumnCount())
                }
                event.accepted = true
            }

            delegate: Item {
                id: emojiTile

                required property int index
                required property string emoji
                required property string group
                required property var shortcodes
                required property var emoticons
                required property var alternates
                readonly property string description: root.formatAliases(shortcodes, emoticons)

                width: emojiGrid.cellWidth
                height: emojiGrid.cellHeight
                Accessible.role: Accessible.Button
                Accessible.name: description.length > 0 ? description : emoji

                Rectangle {
                    anchors.fill: parent
                    anchors.margins: Kirigami.Units.smallSpacing / 3
                    radius: Kirigami.Units.cornerRadius
                    color: emojiHover.hovered || emojiGrid.currentIndex === emojiTile.index
                           ? Qt.alpha(Kirigami.Theme.highlightColor, 0.14)
                           : "transparent"
                    border.color: emojiGrid.currentIndex === emojiTile.index
                                  ? Qt.alpha(Kirigami.Theme.highlightColor, 0.45)
                                  : "transparent"
                }

                Text {
                    anchors.centerIn: parent
                    text: emojiTile.emoji
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                    font.family: root.emojiFontFamily.length > 0 ? root.emojiFontFamily : Kirigami.Theme.defaultFont.family
                    font.pixelSize: Math.round(Kirigami.Theme.defaultFont.pixelSize * 1.65)
                    renderType: Text.QtRendering
                }

                // Bottom-right corner triangle marking emoji with skin-tone
                // variants. A cached glyph is far cheaper than a per-tile Canvas
                // (which is an FBO-backed item repainted from JS on every hover).
                Text {
                    id: variantMarker

                    anchors.right: parent.right
                    anchors.bottom: parent.bottom
                    anchors.rightMargin: Kirigami.Units.smallSpacing * 0.65
                    anchors.bottomMargin: Kirigami.Units.smallSpacing * 0.65
                    text: "◢" // ◢ black lower-right triangle
                    font.pixelSize: Math.round(Kirigami.Units.smallSpacing * 1.3)
                    color: emojiHover.hovered || emojiGrid.currentIndex === emojiTile.index
                           ? Kirigami.Theme.highlightColor
                           : Kirigami.Theme.disabledTextColor
                    visible: emojiTile.alternates && emojiTile.alternates.length > 0
                    opacity: visible ? 0.9 : 0
                    renderType: Text.QtRendering
                }

                HoverHandler {
                    id: emojiHover
                    onHoveredChanged: {
                        if (hovered) {
                            root.hoveredEmojiInfo = emojiTile.description
                        } else if (root.hoveredEmojiInfo === emojiTile.description) {
                            root.hoveredEmojiInfo = ""
                        }
                    }
                }

                MouseArea {
                    anchors.fill: parent
                    acceptedButtons: Qt.LeftButton | Qt.RightButton
                    hoverEnabled: false
                    preventStealing: false

                    function activate(button) {
                        emojiGrid.currentIndex = emojiTile.index
                        if (button === Qt.RightButton) {
                            root.showAlternates(emojiTile.emoji, emojiTile.alternates, emojiTile)
                        } else {
                            root.selectEmoji(emojiTile.emoji, searchField.activeFocus ? searchField : emojiGrid)
                        }
                    }

                    onPressed: mouse => {
                        mouse.accepted = true
                    }
                    onClicked: mouse => {
                        activate(mouse.button)
                        mouse.accepted = true
                    }
                    onDoubleClicked: mouse => {
                        activate(mouse.button)
                        mouse.accepted = true
                    }
                }
            }

            Kirigami.PlaceholderMessage {
                anchors.centerIn: parent
                visible: emojiGrid.count === 0
                width: Math.min(parent.width - Kirigami.Units.largeSpacing * 2,
                                Kirigami.Units.gridUnit * 18)
                text: Whatevr.AppController.emojiModel.loaded
                      ? Whatevr.I18n.i18nc("@info", "No emoji found")
                      : Whatevr.I18n.i18nc("@info", "Emoji metadata could not be loaded")
                explanation: Whatevr.AppController.emojiModel.loaded
                             ? Whatevr.I18n.i18nc("@info", "Try a shortcode like joy, an emoticon like :-), or another category.")
                             : Whatevr.I18n.i18nc("@info", "The bundled Google Fonts emoji metadata resource is unavailable.")
            }
        }

        Label {
            Layout.fillWidth: true
            text: root.hoveredEmojiInfo.length > 0
                  ? root.hoveredEmojiInfo
                  : Whatevr.I18n.i18nc("@info", "Right-click emoji for variants")
            color: Kirigami.Theme.disabledTextColor
            font: Kirigami.Theme.smallFont
            elide: Text.ElideRight
        }

        Popup {
            id: alternatesPopup

            parent: root.contentItem
            padding: Kirigami.Units.smallSpacing
            closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside

            background: Rectangle {
                Kirigami.Theme.inherit: false
                Kirigami.Theme.colorSet: Kirigami.Theme.View
                color: Kirigami.Theme.backgroundColor
                radius: Kirigami.Units.cornerRadius
                border.color: Qt.alpha(Kirigami.Theme.textColor, 0.14)
            }

            contentItem: RowLayout {
                spacing: Kirigami.Units.smallSpacing / 2

                Repeater {
                    model: root.activeAlternates

                    delegate: Item {
                        id: alternateTile

                        required property string modelData

                        Layout.preferredWidth: root.cellSize
                        Layout.preferredHeight: root.cellSize

                        Rectangle {
                            anchors.fill: parent
                            radius: Kirigami.Units.cornerRadius
                            color: alternateHover.hovered ? Qt.alpha(Kirigami.Theme.highlightColor, 0.14) : "transparent"
                        }

                        Text {
                            anchors.centerIn: parent
                            text: alternateTile.modelData
                            horizontalAlignment: Text.AlignHCenter
                            verticalAlignment: Text.AlignVCenter
                            font.family: root.emojiFontFamily.length > 0 ? root.emojiFontFamily : Kirigami.Theme.defaultFont.family
                            font.pixelSize: Math.round(Kirigami.Theme.defaultFont.pixelSize * 1.65)
                            renderType: Text.QtRendering
                        }

                        HoverHandler {
                            id: alternateHover
                        }

                        MouseArea {
                            anchors.fill: parent
                            acceptedButtons: Qt.LeftButton
                            hoverEnabled: false
                            preventStealing: false

                            function activate() {
                                alternatesPopup.close()
                                root.selectEmoji(alternateTile.modelData, searchField.activeFocus ? searchField : emojiGrid)
                            }

                            onPressed: mouse => {
                                mouse.accepted = true
                            }
                            onClicked: mouse => {
                                activate()
                                mouse.accepted = true
                            }
                            onDoubleClicked: mouse => {
                                activate()
                                mouse.accepted = true
                            }
                        }
                    }
                }
            }
        }
    }
}
