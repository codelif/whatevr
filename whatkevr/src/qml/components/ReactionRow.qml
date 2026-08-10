pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Reactions on a message, shown as a wrapping row of per-emoji chips below the
// bubble. Each chip carries one distinct emoji and its count; the chip holding
// the viewer's own reaction is highlight-tinted. Left-clicking a chip toggles
// the viewer's reaction with that emoji, right-clicking opens the reactor
// details dialog, and hovering lists the reactors in a tooltip. The owner sets
// the width (capped at the bubble) so overflow wraps instead of widening, and
// layoutDirection right-aligns the chips for outgoing messages.
Flow {
    id: row

    // List of {emoji, senderId, senderName, fromMe} maps (MessageListModel
    // ReactionsRole).
    property var reactions: []
    signal toggleRequested(string emoji)
    signal detailsRequested()

    readonly property string emojiFontFamily: Whatevr.ProtocolController.emojiModel.emojiFontFamily

    // Reactions aggregated per distinct emoji in first-seen order:
    // [{emoji, count, mine, names: [..]}].
    readonly property var groups: {
        const out = []
        const byEmoji = {}
        const list = reactions || []
        for (let i = 0; i < list.length; ++i) {
            const emoji = String(list[i].emoji || "")
            if (emoji.length === 0) {
                continue
            }
            const fromMe = Boolean(list[i].fromMe)
            const name = fromMe
                ? Whatevr.I18n.i18nc("@label the viewer's own reaction", "You")
                : (String(list[i].senderName || "").length > 0
                   ? String(list[i].senderName)
                   : Whatevr.I18n.i18nc("@label unknown reactor", "Someone"))
            if (byEmoji[emoji] === undefined) {
                byEmoji[emoji] = out.length
                out.push({ emoji: emoji, count: 1, mine: fromMe, names: [name] })
            } else {
                const group = out[byEmoji[emoji]]
                group.count += 1
                group.mine = group.mine || fromMe
                group.names.push(name)
            }
        }
        return out
    }

    visible: groups.length > 0
    spacing: Math.round(Kirigami.Units.smallSpacing / 2)

    function tooltipFor(group) {
        const separator = Whatevr.I18n.i18nc("@label separator in a list of reactor names", ", ")
        const cap = 6
        if (group.names.length <= cap) {
            return group.names.join(separator)
        }
        return Whatevr.I18n.i18ncp("@label reactor names with overflow; %2 is the leading names",
                                   "%2 and one more", "%2 and %1 more",
                                   group.names.length - cap,
                                   group.names.slice(0, cap).join(separator))
    }

    Repeater {
        model: row.groups

        delegate: Rectangle {
            id: chip

            required property var modelData

            implicitWidth: chipContent.implicitWidth + Kirigami.Units.smallSpacing * 2
            implicitHeight: chipContent.implicitHeight + Kirigami.Units.smallSpacing
            radius: Kirigami.Units.cornerRadius
            color: chip.modelData.mine
                   ? Qt.alpha(Kirigami.Theme.highlightColor, chipMouse.containsMouse ? 0.24 : 0.15)
                   : (chipMouse.containsMouse
                      ? Qt.alpha(Kirigami.Theme.textColor, 0.08)
                      : Kirigami.Theme.backgroundColor)
            border.color: chip.modelData.mine
                          ? Qt.alpha(Kirigami.Theme.highlightColor, 0.7)
                          : Qt.alpha(Kirigami.Theme.textColor, 0.18)
            border.width: 1

            ToolTip.visible: chipMouse.containsMouse
            ToolTip.delay: Kirigami.Units.toolTipDelay
            ToolTip.text: row.tooltipFor(chip.modelData)

            Row {
                id: chipContent

                anchors.centerIn: parent
                spacing: Math.round(Kirigami.Units.smallSpacing / 2)

                Text {
                    text: chip.modelData.emoji
                    font.family: row.emojiFontFamily
                    font.pixelSize: Math.round(Kirigami.Units.gridUnit * 0.9)
                    renderType: Text.QtRendering
                    anchors.verticalCenter: parent.verticalCenter
                }

                Label {
                    visible: chip.modelData.count > 1
                    text: chip.modelData.count
                    color: chip.modelData.mine ? Kirigami.Theme.textColor : Kirigami.Theme.disabledTextColor
                    font.pixelSize: Math.round(Kirigami.Theme.defaultFont.pixelSize * 0.9)
                    anchors.verticalCenter: parent.verticalCenter
                }
            }

            // A MouseArea (not TapHandler) so the right-button press is consumed
            // here instead of also reaching the bubble's context-menu surface.
            MouseArea {
                id: chipMouse

                anchors.fill: parent
                acceptedButtons: Qt.LeftButton | Qt.RightButton
                hoverEnabled: true
                cursorShape: Qt.PointingHandCursor
                onClicked: mouse => {
                    if (mouse.button === Qt.RightButton) {
                        row.detailsRequested()
                    } else {
                        row.toggleRequested(chip.modelData.emoji)
                    }
                }
            }
        }
    }
}
