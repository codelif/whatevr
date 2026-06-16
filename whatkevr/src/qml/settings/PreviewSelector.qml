import QtQuick
import QtQuick.Layouts
import org.kde.kirigami as Kirigami

// A row of clickable preview tiles for a single-choice setting, the visual
// alternative to a switch or combo. Drop it directly inside a FormCard.FormCard.
//
// `options` is a list of plain objects; every entry needs `value` and `label`,
// plus the fields the chosen `previewKind` renders. The selected tile is the one
// whose `value` equals `currentValue`; clicking a tile emits activated(value).
//
// Colors come from SystemPalette (like AvatarImage), not Kirigami.Theme:
// attaching a Kirigami PlatformTheme to the per-tile Repeater delegates inside
// this Flow-in-FormCard subtree crashes during page polish (theme propagation
// over an enable-recursion). Plain Text + SystemPalette avoid that entirely.
Item {
    id: root

    property var options: []
    property var currentValue
    // One of: "density", "theme", "wallpaper", "font", "skintone".
    property string previewKind: "theme"
    property real tileWidth: Kirigami.Units.gridUnit * 6
    property real tileHeight: Kirigami.Units.gridUnit * 4.5

    signal activated(var value)

    // Stretch to the FormCard width; the tiles lay out in a centered, wrapping
    // row (see flow below) so 4 font tiles or 6 wallpapers look balanced even
    // when the row count doesn't divide evenly.
    Layout.fillWidth: true

    readonly property real spacing: Kirigami.Units.largeSpacing
    readonly property real margin: Kirigami.Units.largeSpacing
    // Available content width, and how many tiles fit per row.
    readonly property real availableWidth: width - margin * 2
    readonly property int columns: Math.max(1, Math.min(options.length,
        Math.floor((availableWidth + spacing) / (tileWidth + spacing))))

    implicitWidth: tileWidth + margin * 2
    implicitHeight: flow.implicitHeight + margin * 2

    SystemPalette {
        id: pal
        colorGroup: SystemPalette.Active
    }

    Flow {
        id: flow
        // Width is exactly the laid-out row of `columns` tiles, centered in the
        // selector. Flow then wraps the remaining tiles into further rows.
        width: root.columns * root.tileWidth + (root.columns - 1) * root.spacing
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.top: parent.top
        anchors.topMargin: root.margin
        spacing: root.spacing

        Repeater {
            model: root.options

            Item {
                id: tile

                required property var modelData
                readonly property bool selected: root.currentValue === modelData.value

                width: root.tileWidth
                height: previewFrame.height + tileLabel.height + Kirigami.Units.smallSpacing

                Rectangle {
                    id: previewFrame

                    width: root.tileWidth
                    height: root.tileHeight
                    radius: Kirigami.Units.smallSpacing
                    clip: true
                    antialiasing: true
                    color: pal.base
                    border.width: tile.selected ? 2 : 1
                    border.color: tile.selected ? pal.highlight : Qt.alpha(pal.text, 0.15)

                    // --- Theme: a mini titlebar + body + a text bar ---
                    Rectangle {
                        visible: root.previewKind === "theme"
                        anchors { left: parent.left; right: parent.right; top: parent.top }
                        height: parent.height * 0.32
                        color: tile.modelData.accent ?? pal.highlight
                    }
                    Rectangle {
                        visible: root.previewKind === "theme"
                        anchors {
                            left: parent.left; right: parent.right; bottom: parent.bottom
                            top: parent.top; topMargin: parent.height * 0.32
                        }
                        color: tile.modelData.base ?? pal.base
                        Rectangle {
                            anchors.centerIn: parent
                            width: parent.width * 0.5
                            height: 6
                            radius: 3
                            opacity: 0.6
                            color: tile.modelData.text ?? pal.text
                        }
                    }

                    // --- Density: 3 stacked mock chat rows ---
                    Column {
                        visible: root.previewKind === "density"
                        anchors.fill: parent
                        anchors.margins: Kirigami.Units.smallSpacing
                        spacing: tile.modelData.gap ?? 6

                        Repeater {
                            model: 3

                            Item {
                                width: previewFrame.width - Kirigami.Units.smallSpacing * 2
                                height: tile.modelData.avatar ?? 14

                                Rectangle {
                                    id: rowAvatar
                                    width: tile.modelData.avatar ?? 14
                                    height: width
                                    radius: width / 2
                                    anchors.verticalCenter: parent.verticalCenter
                                    color: Qt.alpha(pal.text, 0.25)
                                }
                                Rectangle {
                                    anchors {
                                        left: rowAvatar.right
                                        leftMargin: Kirigami.Units.smallSpacing
                                        right: parent.right
                                        verticalCenter: parent.verticalCenter
                                    }
                                    height: 4
                                    radius: 2
                                    color: Qt.alpha(pal.text, 0.3)
                                }
                            }
                        }
                    }

                    // --- Wallpaper: bubbles over a background ---
                    Rectangle {
                        visible: root.previewKind === "wallpaper"
                        anchors.fill: parent
                        color: tile.modelData.bg ?? pal.base

                        Rectangle {
                            anchors { left: parent.left; top: parent.top; margins: Kirigami.Units.smallSpacing }
                            width: parent.width * 0.5
                            height: 8
                            radius: 4
                            color: Qt.alpha(tile.modelData.bubble ?? pal.text, 0.85)
                        }
                        Rectangle {
                            anchors { right: parent.right; bottom: parent.bottom; margins: Kirigami.Units.smallSpacing }
                            width: parent.width * 0.55
                            height: 8
                            radius: 4
                            color: tile.modelData.outBubble ?? pal.highlight
                        }
                    }

                    // --- Font: a sample at the option's size ---
                    Text {
                        visible: root.previewKind === "font"
                        anchors.centerIn: parent
                        color: pal.text
                        text: tile.modelData.sample ?? "Aa"
                        font.pixelSize: tile.modelData.pixelSize ?? 16
                    }

                    // --- Skin tone: a large emoji rendered in the chosen tone ---
                    Text {
                        visible: root.previewKind === "skintone"
                        anchors.centerIn: parent
                        text: tile.modelData.sample ?? "👋"
                        font.pixelSize: Math.round(previewFrame.height * 0.5)
                    }

                    // Selection checkmark.
                    Rectangle {
                        visible: tile.selected
                        anchors { top: parent.top; right: parent.right; margins: Kirigami.Units.smallSpacing }
                        width: Kirigami.Units.iconSizes.small
                        height: width
                        radius: width / 2
                        color: pal.highlight

                        Text {
                            anchors.centerIn: parent
                            text: "✓"
                            color: pal.highlightedText
                            font.pixelSize: Math.round(parent.height * 0.7)
                            font.bold: true
                        }
                    }

                    TapHandler {
                        onTapped: root.activated(tile.modelData.value)
                    }
                    HoverHandler {
                        cursorShape: Qt.PointingHandCursor
                    }
                }

                Text {
                    id: tileLabel
                    anchors {
                        top: previewFrame.bottom
                        topMargin: Kirigami.Units.smallSpacing
                        horizontalCenter: previewFrame.horizontalCenter
                    }
                    width: root.tileWidth
                    horizontalAlignment: Text.AlignHCenter
                    elide: Text.ElideRight
                    color: pal.text
                    text: tile.modelData.label ?? ""
                    font.weight: tile.selected ? Font.DemiBold : Font.Normal
                }
            }
        }
    }
}
