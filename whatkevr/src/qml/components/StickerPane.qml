pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Sticker half of the expression picker. Category row: Recents · Favorites ·
// installed packs · All · Store. Files download lazily as tiles scroll into
// view (the daemon dedups and rate-limits), Lottie stickers animate on hover
// only, and clicking a sticker sends it to the current chat.
Item {
    id: pane

    // keepOpen is true for Ctrl+click multi-send.
    signal stickerChosen(bool keepOpen)

    property Item searchField: null
    property string replyToMessageId: ""
    property string hoverInfo: ""
    property string sendErrorText: ""
    property bool storeOpen: false
    property bool packOpenedFromStore: false
    readonly property var stickers: Whatevr.AppController.stickers
    readonly property real cellSize: Kirigami.Units.gridUnit * 4.5
    readonly property int stickerDecodeSize: Math.round(cellSize * Math.min(2, Screen.devicePixelRatio))
    readonly property bool packView: stickers.activeSource === "pack"

    function prepareForOpen() {
        pane.sendErrorText = ""
        pane.hoverInfo = ""
        pane.storeOpen = false
        pane.packOpenedFromStore = false
        pane.stickers.activate()
    }

    function applySearchFilter(query) {
        pane.sendErrorText = ""
        if (query.length > 0) {
            pane.storeOpen = false
            pane.stickers.search(query)
        } else {
            pane.stickers.search("")
        }
    }

    function sendFirstVisible(event) {
        if (pane.storeOpen || stickerGrid.count === 0) {
            return
        }
        stickerGrid.currentIndex = 0
        const item = stickerGrid.itemAtIndex(0)
        if (item && item.downloaded) {
            pane.sendSticker(item.cacheKey, false)
            event.accepted = true
        }
    }

    function sendSticker(cacheKey, keepOpen) {
        pane.sendErrorText = ""
        pane.stickers.sendSticker(cacheKey, pane.replyToMessageId)
        pane.stickerChosen(keepOpen)
    }

    function showSource(source) {
        pane.sendErrorText = ""
        pane.storeOpen = false
        pane.packOpenedFromStore = false
        if (pane.searchField && pane.searchField.text.length > 0) {
            pane.searchField.clear()
        }
        if (source === "favorites") {
            pane.stickers.showFavorites()
        } else if (source === "all") {
            pane.stickers.showAll()
        } else {
            pane.stickers.showRecents()
        }
    }

    function openPack(packId, fromStore) {
        pane.sendErrorText = ""
        pane.storeOpen = false
        pane.packOpenedFromStore = fromStore
        if (pane.searchField && pane.searchField.text.length > 0) {
            pane.searchField.clear()
        }
        pane.stickers.showPack(packId)
    }

    function openStore() {
        pane.sendErrorText = ""
        pane.storeOpen = true
        if (pane.searchField && pane.searchField.text.length > 0) {
            pane.searchField.clear()
        }
    }

    Connections {
        target: pane.stickers
        function onStickerSendFailed(cacheKey, errorText) {
            pane.sendErrorText = errorText
        }
    }

    component CategoryTile: Item {
        id: tile

        property string glyph: ""
        property string trayPath: ""
        property string label: ""
        property bool current: false

        width: Kirigami.Units.gridUnit * 2.2
        height: parent ? parent.height : Kirigami.Units.gridUnit * 2.35
        Accessible.role: Accessible.Button
        Accessible.name: label

        signal tapped()

        Rectangle {
            anchors.fill: parent
            anchors.margins: Kirigami.Units.smallSpacing / 3
            radius: Kirigami.Units.cornerRadius
            color: tile.current
                   ? Qt.alpha(Kirigami.Theme.highlightColor, 0.22)
                   : (tileHover.hovered ? Qt.alpha(Kirigami.Theme.textColor, 0.06) : "transparent")
            border.color: tile.current
                          ? Qt.alpha(Kirigami.Theme.highlightColor, 0.55)
                          : "transparent"
        }

        Text {
            anchors.centerIn: parent
            visible: tile.trayPath.length === 0
            text: tile.glyph
            color: tile.current ? Kirigami.Theme.highlightColor : Kirigami.Theme.textColor
            font.weight: Font.DemiBold
            font.pixelSize: Math.round(Kirigami.Theme.defaultFont.pixelSize * 1.45)
            renderType: Text.QtRendering
        }

        Image {
            anchors.centerIn: parent
            visible: tile.trayPath.length > 0
            width: Kirigami.Units.gridUnit * 1.5
            height: Kirigami.Units.gridUnit * 1.5
            source: tile.trayPath.length > 0 ? Qt.resolvedUrl("file://" + tile.trayPath) : ""
            sourceSize.width: Math.round(Kirigami.Units.gridUnit * 1.5 * Screen.devicePixelRatio)
            sourceSize.height: Math.round(Kirigami.Units.gridUnit * 1.5 * Screen.devicePixelRatio)
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            opacity: tile.current ? 1 : 0.82
        }

        HoverHandler {
            id: tileHover
            onHoveredChanged: {
                if (hovered && tile.label.length > 0) {
                    pane.hoverInfo = tile.label
                } else if (pane.hoverInfo === tile.label) {
                    pane.hoverInfo = ""
                }
            }
        }

        TapHandler {
            onTapped: tile.tapped()
        }
    }

    ColumnLayout {
        anchors.fill: parent
        spacing: Kirigami.Units.smallSpacing

        Flickable {
            Layout.fillWidth: true
            Layout.preferredHeight: Kirigami.Units.gridUnit * 2.35
            contentWidth: categoryRow.width
            clip: true
            interactive: contentWidth > width
            boundsBehavior: Flickable.StopAtBounds

            Row {
                id: categoryRow

                height: parent.height
                spacing: 0

                CategoryTile {
                    glyph: "◷"
                    label: Whatevr.I18n.i18nc("@info", "Recent stickers")
                    current: !pane.storeOpen && pane.stickers.activeSource === "recents"
                    onTapped: pane.showSource("recents")
                }

                CategoryTile {
                    glyph: "★"
                    label: Whatevr.I18n.i18nc("@info", "Favorite stickers")
                    current: !pane.storeOpen && pane.stickers.activeSource === "favorites"
                    onTapped: pane.showSource("favorites")
                }

                CategoryTile {
                    glyph: "▦"
                    label: Whatevr.I18n.i18nc("@info", "All stickers")
                    current: !pane.storeOpen && pane.stickers.activeSource === "all"
                    onTapped: pane.showSource("all")
                }

                Repeater {
                    model: pane.stickers.installedPackModel

                    delegate: CategoryTile {
                        required property string packId
                        required property string name
                        required property string trayPath

                        label: name
                        glyph: "▣"
                        current: !pane.storeOpen && pane.packView && pane.stickers.activePackId === packId
                        onTapped: pane.openPack(packId, false)
                    }
                }

                CategoryTile {
                    glyph: "＋"
                    label: Whatevr.I18n.i18nc("@info", "Browse sticker packs")
                    current: pane.storeOpen
                    onTapped: pane.openStore()
                }
            }
        }

        Rectangle {
            Layout.fillWidth: true
            height: 1
            color: Qt.alpha(Kirigami.Theme.textColor, 0.10)
        }

        // Pack preview header: name + install toggle + back.
        RowLayout {
            Layout.fillWidth: true
            visible: !pane.storeOpen && pane.packView
            spacing: Kirigami.Units.smallSpacing

            ToolButton {
                icon.name: "go-previous-symbolic"
                text: Whatevr.I18n.i18nc("@action:button", "Back")
                display: AbstractButton.IconOnly
                onClicked: {
                    if (pane.packOpenedFromStore) {
                        pane.openStore()
                    } else {
                        pane.showSource("recents")
                    }
                }
            }

            Label {
                Layout.fillWidth: true
                text: pane.stickers.activePackName
                font.weight: Font.DemiBold
                elide: Text.ElideRight
            }

            ToolButton {
                text: pane.stickers.activePackInstalled
                      ? Whatevr.I18n.i18nc("@action:button", "Remove from picker")
                      : Whatevr.I18n.i18nc("@action:button", "Add to picker")
                icon.name: pane.stickers.activePackInstalled ? "list-remove-symbolic" : "list-add-symbolic"
                onClicked: pane.stickers.setPackInstalled(pane.stickers.activePackId, !pane.stickers.activePackInstalled)
            }
        }

        StackLayout {
            Layout.fillWidth: true
            Layout.fillHeight: true
            currentIndex: pane.storeOpen ? 1 : 0

            // Page 0: sticker grid.
            GridView {
                id: stickerGrid

                clip: true
                reuseItems: true
                cacheBuffer: pane.cellSize * 6
                boundsBehavior: Flickable.StopAtBounds
                model: pane.stickers.stickerModel
                cellWidth: Math.max(pane.cellSize, Math.floor(width / Math.max(1, Math.floor(width / pane.cellSize))))
                cellHeight: pane.cellSize
                keyNavigationWraps: true
                highlightMoveDuration: 60
                onCountChanged: currentIndex = count > 0 ? Math.max(0, Math.min(currentIndex, count - 1)) : -1

                Keys.onReturnPressed: event => {
                    const item = stickerGrid.itemAtIndex(stickerGrid.currentIndex)
                    if (item && item.downloaded) {
                        pane.sendSticker(item.cacheKey, (event.modifiers & Qt.ControlModifier) !== 0)
                        event.accepted = true
                    }
                }
                Keys.onEnterPressed: event => {
                    const item = stickerGrid.itemAtIndex(stickerGrid.currentIndex)
                    if (item && item.downloaded) {
                        pane.sendSticker(item.cacheKey, (event.modifiers & Qt.ControlModifier) !== 0)
                        event.accepted = true
                    }
                }
                Keys.onUpPressed: event => {
                    const columns = Math.max(1, Math.floor(stickerGrid.width / Math.max(1, stickerGrid.cellWidth)))
                    if (stickerGrid.currentIndex >= 0 && stickerGrid.currentIndex < columns) {
                        if (pane.searchField) {
                            pane.searchField.forceActiveFocus(Qt.PopupFocusReason)
                        }
                        event.accepted = true
                        return
                    }
                    event.accepted = false
                }

                delegate: Item {
                    id: stickerTile

                    required property int index
                    required property string cacheKey
                    required property string localPath
                    required property bool animated
                    required property bool downloaded
                    required property string emojis
                    required property string accessText
                    readonly property string description: accessText.length > 0
                                                          ? (emojis.length > 0 ? accessText + "  " + emojis : accessText)
                                                          : emojis

                    width: stickerGrid.cellWidth
                    height: stickerGrid.cellHeight
                    Accessible.role: Accessible.Button
                    Accessible.name: description.length > 0 ? description : Whatevr.I18n.i18nc("@info", "Sticker")

                    function maybeDownload() {
                        if (!downloaded && cacheKey.length > 0) {
                            pane.stickers.requestDownload(cacheKey)
                        }
                    }

                    Component.onCompleted: maybeDownload()
                    GridView.onReused: maybeDownload()

                    Rectangle {
                        anchors.fill: parent
                        anchors.margins: Kirigami.Units.smallSpacing / 3
                        radius: Kirigami.Units.cornerRadius
                        color: stickerHover.hovered || stickerGrid.currentIndex === stickerTile.index
                               ? Qt.alpha(Kirigami.Theme.highlightColor, 0.14)
                               : "transparent"
                        border.color: stickerGrid.currentIndex === stickerTile.index
                                      ? Qt.alpha(Kirigami.Theme.highlightColor, 0.45)
                                      : "transparent"
                    }

                    Image {
                        anchors.fill: parent
                        anchors.margins: Kirigami.Units.smallSpacing
                        visible: stickerTile.downloaded && !stickerTile.animated
                        source: stickerTile.downloaded && !stickerTile.animated
                                ? Qt.resolvedUrl("file://" + stickerTile.localPath)
                                : ""
                        sourceSize.width: pane.stickerDecodeSize
                        sourceSize.height: pane.stickerDecodeSize
                        fillMode: Image.PreserveAspectFit
                        asynchronous: true
                        cache: true
                    }

                    Whatevr.RlottieSticker {
                        anchors.fill: parent
                        anchors.margins: Kirigami.Units.smallSpacing
                        visible: stickerTile.downloaded && stickerTile.animated
                        source: stickerTile.downloaded && stickerTile.animated
                                ? Qt.resolvedUrl("file://" + stickerTile.localPath)
                                : ""
                        // First frame renders at rest; animate on hover only so a
                        // full grid of Lottie stickers stays cheap.
                        playing: stickerHover.hovered
                        renderScale: Math.max(1, Math.min(2, Screen.devicePixelRatio))
                    }

                    // Not-yet-downloaded placeholder; the lazy download was
                    // already kicked off above.
                    Rectangle {
                        anchors.fill: parent
                        anchors.margins: Kirigami.Units.smallSpacing
                        visible: !stickerTile.downloaded
                        radius: Kirigami.Units.cornerRadius
                        color: Qt.alpha(Kirigami.Theme.textColor, 0.05)

                        BusyIndicator {
                            anchors.centerIn: parent
                            width: Kirigami.Units.gridUnit * 1.5
                            height: width
                            running: visible && busyDelay.fired
                        }
                    }

                    Timer {
                        id: busyDelay
                        property bool fired: false
                        interval: 400
                        running: !stickerTile.downloaded && stickerTile.visible
                        onTriggered: fired = true
                    }

                    HoverHandler {
                        id: stickerHover
                        onHoveredChanged: {
                            if (hovered) {
                                pane.hoverInfo = stickerTile.description
                            } else if (pane.hoverInfo === stickerTile.description) {
                                pane.hoverInfo = ""
                            }
                        }
                    }

                    MouseArea {
                        anchors.fill: parent
                        acceptedButtons: Qt.LeftButton
                        hoverEnabled: false

                        onClicked: mouse => {
                            stickerGrid.currentIndex = stickerTile.index
                            if (stickerTile.downloaded) {
                                pane.sendSticker(stickerTile.cacheKey, (mouse.modifiers & Qt.ControlModifier) !== 0)
                            } else {
                                stickerTile.maybeDownload()
                            }
                            mouse.accepted = true
                        }
                    }
                }

                Kirigami.PlaceholderMessage {
                    anchors.centerIn: parent
                    visible: stickerGrid.count === 0 && !pane.stickers.loading
                    width: Math.min(parent.width - Kirigami.Units.largeSpacing * 2,
                                    Kirigami.Units.gridUnit * 18)
                    text: {
                        if (pane.stickers.errorText.length > 0) {
                            return pane.stickers.errorText
                        }
                        if (pane.stickers.activeSource === "favorites") {
                            return Whatevr.I18n.i18nc("@info", "No favorite stickers yet")
                        }
                        if (pane.stickers.activeSource === "search") {
                            return Whatevr.I18n.i18nc("@info", "No stickers found")
                        }
                        return Whatevr.I18n.i18nc("@info", "No stickers yet")
                    }
                    explanation: pane.stickers.activeSource === "favorites"
                                 ? Whatevr.I18n.i18nc("@info", "Stickers you favorite on your phone appear here.")
                                 : Whatevr.I18n.i18nc("@info", "Browse the sticker packs to get started.")
                    helpfulAction: Kirigami.Action {
                        icon.name: "list-add-symbolic"
                        text: Whatevr.I18n.i18nc("@action:button", "Browse sticker packs")
                        onTriggered: pane.openStore()
                    }
                }

                BusyIndicator {
                    anchors.centerIn: parent
                    running: stickerGrid.count === 0 && pane.stickers.loading
                }
            }

            // Page 1: store browser.
            ColumnLayout {
                spacing: Kirigami.Units.smallSpacing

                RowLayout {
                    Layout.fillWidth: true
                    spacing: Kirigami.Units.smallSpacing

                    Label {
                        Layout.fillWidth: true
                        Layout.leftMargin: Kirigami.Units.smallSpacing
                        text: Whatevr.I18n.i18nc("@title", "Sticker packs")
                        font.weight: Font.DemiBold
                    }

                    ToolButton {
                        icon.name: "view-refresh-symbolic"
                        text: Whatevr.I18n.i18nc("@action:button", "Refresh sticker packs")
                        display: AbstractButton.IconOnly
                        enabled: !pane.stickers.packsLoading
                        onClicked: pane.stickers.refreshStore()
                    }
                }

                ListView {
                    id: packList

                    Layout.fillWidth: true
                    Layout.fillHeight: true
                    clip: true
                    reuseItems: true
                    boundsBehavior: Flickable.StopAtBounds
                    model: pane.stickers.packModel
                    spacing: Kirigami.Units.smallSpacing / 2

                    delegate: Item {
                        id: packRow

                        required property string packId
                        required property string name
                        required property string publisher
                        required property string trayPath
                        required property bool installed
                        required property int stickerCount

                        width: packList.width
                        height: Kirigami.Units.gridUnit * 3

                        Rectangle {
                            anchors.fill: parent
                            radius: Kirigami.Units.cornerRadius
                            color: packHover.hovered ? Qt.alpha(Kirigami.Theme.textColor, 0.06) : "transparent"
                        }

                        RowLayout {
                            anchors.fill: parent
                            anchors.leftMargin: Kirigami.Units.smallSpacing
                            anchors.rightMargin: Kirigami.Units.smallSpacing
                            spacing: Kirigami.Units.smallSpacing * 2

                            Image {
                                Layout.preferredWidth: Kirigami.Units.gridUnit * 2
                                Layout.preferredHeight: Kirigami.Units.gridUnit * 2
                                source: packRow.trayPath.length > 0 ? Qt.resolvedUrl("file://" + packRow.trayPath) : ""
                                sourceSize.width: Math.round(Kirigami.Units.gridUnit * 2 * Screen.devicePixelRatio)
                                sourceSize.height: Math.round(Kirigami.Units.gridUnit * 2 * Screen.devicePixelRatio)
                                fillMode: Image.PreserveAspectFit
                                asynchronous: true
                            }

                            ColumnLayout {
                                Layout.fillWidth: true
                                spacing: 0

                                Label {
                                    Layout.fillWidth: true
                                    text: packRow.name
                                    font.weight: Font.DemiBold
                                    elide: Text.ElideRight
                                }

                                Label {
                                    Layout.fillWidth: true
                                    text: packRow.stickerCount > 0
                                          ? Whatevr.I18n.i18nc("@info", "%1 · %2 stickers", packRow.publisher, packRow.stickerCount)
                                          : packRow.publisher
                                    color: Kirigami.Theme.disabledTextColor
                                    font: Kirigami.Theme.smallFont
                                    elide: Text.ElideRight
                                }
                            }

                            ToolButton {
                                icon.name: packRow.installed ? "list-remove-symbolic" : "list-add-symbolic"
                                text: packRow.installed
                                      ? Whatevr.I18n.i18nc("@action:button", "Remove from picker")
                                      : Whatevr.I18n.i18nc("@action:button", "Add to picker")
                                display: AbstractButton.IconOnly
                                onClicked: pane.stickers.setPackInstalled(packRow.packId, !packRow.installed)
                            }
                        }

                        HoverHandler {
                            id: packHover
                        }

                        TapHandler {
                            onTapped: pane.openPack(packRow.packId, true)
                        }
                    }

                    Kirigami.PlaceholderMessage {
                        anchors.centerIn: parent
                        visible: packList.count === 0 && !pane.stickers.packsLoading
                        width: Math.min(parent.width - Kirigami.Units.largeSpacing * 2,
                                        Kirigami.Units.gridUnit * 18)
                        text: Whatevr.I18n.i18nc("@info", "Sticker packs unavailable")
                        explanation: Whatevr.I18n.i18nc("@info", "The sticker pack catalogue could not be loaded. Check your connection and refresh.")
                    }

                    BusyIndicator {
                        anchors.centerIn: parent
                        running: packList.count === 0 && pane.stickers.packsLoading
                    }
                }
            }
        }

        Label {
            Layout.fillWidth: true
            text: {
                if (pane.sendErrorText.length > 0) {
                    return pane.sendErrorText
                }
                if (pane.hoverInfo.length > 0) {
                    return pane.hoverInfo
                }
                return Whatevr.I18n.i18nc("@info", "Click to send · Ctrl+click to send several")
            }
            color: pane.sendErrorText.length > 0 ? Kirigami.Theme.negativeTextColor : Kirigami.Theme.disabledTextColor
            font: Kirigami.Theme.smallFont
            elide: Text.ElideRight
        }
    }
}
