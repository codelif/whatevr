pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Lists everyone who reacted to a message (emoji + reactor name). The viewer's
// own reaction is tappable to remove it.
Popup {
    id: root

    // List of {emoji, senderId, senderName, fromMe} maps.
    property var reactions: []
    property string messageId: ""

    readonly property string emojiFontFamily: Whatevr.AppController.emojiModel.emojiFontFamily

    modal: false
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    padding: Kirigami.Units.smallSpacing

    function openFor(reactionList, msgId, anchorX, anchorY) {
        reactions = reactionList || []
        messageId = msgId
        open()
        const limit = parent ? parent.width : anchorX + width
        x = Math.round(Math.max(Kirigami.Units.smallSpacing,
                                Math.min(limit - width - Kirigami.Units.smallSpacing, anchorX)))
        const bottomLimit = parent ? parent.height : anchorY + height
        y = Math.round(Math.min(anchorY, bottomLimit - height - Kirigami.Units.smallSpacing))
    }

    background: Rectangle {
        Kirigami.Theme.inherit: false
        Kirigami.Theme.colorSet: Kirigami.Theme.View
        color: Kirigami.Theme.backgroundColor
        radius: Kirigami.Units.cornerRadius
        border.color: Qt.alpha(Kirigami.Theme.textColor, 0.14)
        border.width: 1
    }

    contentItem: ListView {
        id: list

        implicitWidth: Kirigami.Units.gridUnit * 13
        implicitHeight: Math.min(contentHeight, Kirigami.Units.gridUnit * 16)
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        model: root.reactions

        delegate: ItemDelegate {
            id: entry

            required property var modelData
            readonly property bool removable: Boolean(modelData.fromMe)
            readonly property real rowPadding: Kirigami.Units.smallSpacing

            width: ListView.view.width
            topPadding: rowPadding
            bottomPadding: rowPadding
            leftPadding: rowPadding
            rightPadding: rowPadding
            hoverEnabled: removable
            enabled: removable
            opacity: removable ? 1 : 0.95
            background: Rectangle {
                opacity: entry.removable && (entry.hovered || entry.down || entry.highlighted) ? 1 : 0
                color: entry.down ? Kirigami.Theme.focusColor : Qt.alpha(Kirigami.Theme.focusColor, 0.3)
                border.color: Kirigami.Theme.focusColor
                border.width: 1
                radius: Kirigami.Units.cornerRadius
            }

            onClicked: {
                if (removable) {
                    Whatevr.AppController.sendReaction(root.messageId, "")
                    root.close()
                }
            }

            contentItem: RowLayout {
                spacing: Kirigami.Units.smallSpacing

                Label {
                    text: entry.removable
                          ? Whatevr.I18n.i18nc("@label the viewer's own reaction", "You")
                          : (String(entry.modelData.senderName).length > 0
                             ? String(entry.modelData.senderName)
                             : Whatevr.I18n.i18nc("@label unknown reactor", "Someone"))
                    elide: Text.ElideRight
                    Layout.fillWidth: true
                }

                Label {
                    visible: entry.removable
                    text: Whatevr.I18n.i18nc("@label hint to remove reaction", "tap to remove")
                    color: Kirigami.Theme.disabledTextColor
                    font.pixelSize: Math.round(Kirigami.Theme.defaultFont.pixelSize * 0.85)
                }

                Text {
                    text: String(entry.modelData.emoji)
                    font.family: root.emojiFontFamily
                    font.pixelSize: Math.round(Kirigami.Units.gridUnit * 1.1)
                    renderType: Text.QtRendering
                }
            }
        }
    }
}
