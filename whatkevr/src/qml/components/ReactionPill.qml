pragma ComponentBehavior: Bound

import QtQuick
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Compact WhatsApp-style reaction chip that overlaps the bottom edge of a
// bubble. Aggregates the message's reactions into a single pill: up to three
// distinct emoji followed by the total reactor count. Clicking it opens the
// reactor list.
Item {
    id: pill

    // List of {emoji, senderId, senderName, fromMe} maps (MessageListModel
    // ReactionsRole).
    property var reactions: []
    signal clicked()

    readonly property string emojiFontFamily: Whatevr.AppController.emojiModel.emojiFontFamily
    readonly property int totalCount: reactions ? reactions.length : 0
    readonly property bool mine: {
        if (!reactions) {
            return false
        }
        for (let i = 0; i < reactions.length; ++i) {
            if (reactions[i].fromMe) {
                return true
            }
        }
        return false
    }
    // Distinct emoji in first-seen order, capped to three for the pill face.
    readonly property var distinctEmojis: {
        const seen = []
        if (reactions) {
            for (let i = 0; i < reactions.length && seen.length < 3; ++i) {
                const emoji = reactions[i].emoji
                if (emoji && seen.indexOf(emoji) === -1) {
                    seen.push(emoji)
                }
            }
        }
        return seen
    }

    visible: totalCount > 0
    implicitWidth: visible ? chip.implicitWidth : 0
    implicitHeight: visible ? chip.implicitHeight : 0

    Rectangle {
        id: chip

        implicitWidth: contentRow.implicitWidth + Kirigami.Units.smallSpacing * 2
        implicitHeight: contentRow.implicitHeight + Kirigami.Units.smallSpacing
        radius: height / 2
        color: Kirigami.Theme.backgroundColor
        border.color: pill.mine
                      ? Qt.alpha(Kirigami.Theme.highlightColor, 0.7)
                      : Qt.alpha(Kirigami.Theme.textColor, 0.18)
        border.width: 1

        Row {
            id: contentRow
            anchors.centerIn: parent
            spacing: Kirigami.Units.smallSpacing / 2

            Repeater {
                model: pill.distinctEmojis

                delegate: Text {
                    required property string modelData
                    text: modelData
                    font.family: pill.emojiFontFamily
                    font.pixelSize: Math.round(Kirigami.Units.gridUnit * 0.95)
                    renderType: Text.QtRendering
                    anchors.verticalCenter: parent.verticalCenter
                }
            }

            Text {
                visible: pill.totalCount > 1
                text: pill.totalCount
                color: Kirigami.Theme.disabledTextColor
                font.pixelSize: Math.round(Kirigami.Theme.defaultFont.pixelSize * 0.9)
                renderType: Text.QtRendering
                anchors.verticalCenter: parent.verticalCenter
            }
        }

        HoverHandler {
            cursorShape: Qt.PointingHandCursor
        }

        TapHandler {
            onTapped: pill.clicked()
        }
    }
}
