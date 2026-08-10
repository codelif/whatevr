pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Presentational row of quick-reaction emoji plus a "+" button that opens the
// full emoji picker. Reused by the hover/right-click quick-reaction popup and
// the context menu. Emits the tapped emoji; the consumer decides whether that
// adds or toggles off an existing reaction.
Row {
    id: bar

    signal reacted(string emoji)
    signal pickerRequested()

    // The user's own current reaction on the target message, highlighted so a
    // second tap reads as "remove".
    property string currentEmoji: ""

    readonly property var baseEmojis: ["👍", "❤️", "😂", "😮", "😢", "🙏"]
    // The six WhatsApp defaults, with the user's most recent non-default
    // reaction appended so their last-used emoji is always one tap away.
    readonly property var quickEmojis: {
        const recents = Whatevr.ProtocolController.emojiModel.recentEmoji(8)
        for (let i = 0; i < recents.length; ++i) {
            if (bar.baseEmojis.indexOf(recents[i]) === -1) {
                return bar.baseEmojis.concat([recents[i]])
            }
        }
        return bar.baseEmojis
    }
    readonly property string emojiFontFamily: Whatevr.ProtocolController.emojiModel.emojiFontFamily
    readonly property real cellSize: Kirigami.Units.gridUnit * 1.9

    // When > 0 the bar stretches to this width and spreads its cells edge to
    // edge by widening the gaps, so it fills a wider container (the context
    // menu, sized by its text rows) instead of leaving trailing whitespace.
    // 0 = size to content, used by the floating popup.
    property real fillWidth: 0

    readonly property real minSpacing: Kirigami.Units.smallSpacing / 2
    // Fixed (non-gap) width: every emoji cell + the 1px separator + the "+" cell.
    readonly property real fixedChildrenWidth: quickEmojis.length * cellSize + 1 + cellSize
    readonly property int gapCount: quickEmojis.length + 1
    // Natural minimal width, computed with minSpacing so it never tracks the
    // live (stretched) spacing — consumers use it to size containers without a
    // binding loop.
    readonly property real contentWidth: fixedChildrenWidth + gapCount * minSpacing + leftPadding + rightPadding
    // Emoji glyphs render low within their line box; floating reaction popups
    // shift them slightly up, while embedded menu rows can opt into true centre.
    property real emojiBottomMargin: Math.round(cellSize * 0.08)
    property real contentPadding: Kirigami.Units.smallSpacing

    padding: contentPadding
    spacing: fillWidth > 0
             ? Math.max(minSpacing, (fillWidth - leftPadding - rightPadding - fixedChildrenWidth) / gapCount)
             : minSpacing
    width: fillWidth > 0 ? fillWidth : implicitWidth

    Repeater {
        model: bar.quickEmojis

        delegate: Rectangle {
            id: cell

            required property string modelData
            readonly property bool active: bar.currentEmoji.length > 0 && bar.currentEmoji === modelData

            width: bar.cellSize
            height: bar.cellSize
            radius: Kirigami.Units.cornerRadius
            color: cell.active
                   ? Qt.alpha(Kirigami.Theme.highlightColor, 0.30)
                   : (cellHover.hovered ? Qt.alpha(Kirigami.Theme.textColor, 0.10) : "transparent")

            Text {
                anchors.fill: parent
                anchors.bottomMargin: bar.emojiBottomMargin
                horizontalAlignment: Text.AlignHCenter
                verticalAlignment: Text.AlignVCenter
                text: cell.modelData
                font.family: bar.emojiFontFamily
                font.pixelSize: Math.round(bar.cellSize * 0.62)
                renderType: Text.QtRendering
            }

            HoverHandler {
                id: cellHover
                cursorShape: Qt.PointingHandCursor
            }

            TapHandler {
                onTapped: bar.reacted(cell.modelData)
            }

            Behavior on scale {
                NumberAnimation {
                    duration: Kirigami.Units.shortDuration
                    easing.type: Easing.OutCubic
                }
            }
            scale: cellHover.hovered ? 1.15 : 1
        }
    }

    // Separator before the "+" picker button.
    Rectangle {
        width: 1
        height: bar.cellSize * 0.6
        anchors.verticalCenter: parent.verticalCenter
        color: Qt.alpha(Kirigami.Theme.textColor, 0.18)
    }

    Rectangle {
        width: bar.cellSize
        height: bar.cellSize
        radius: Kirigami.Units.cornerRadius
        color: moreHover.hovered ? Qt.alpha(Kirigami.Theme.textColor, 0.10) : "transparent"

        Kirigami.Icon {
            anchors.centerIn: parent
            width: Kirigami.Units.iconSizes.small
            height: width
            source: "list-add-symbolic"
            color: Kirigami.Theme.textColor
            isMask: true
        }

        HoverHandler {
            id: moreHover
            cursorShape: Qt.PointingHandCursor
        }

        TapHandler {
            onTapped: bar.pickerRequested()
        }
    }
}
