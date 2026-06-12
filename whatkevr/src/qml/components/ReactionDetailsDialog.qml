pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Centered dialog listing everyone who reacted to a message. With more than
// one distinct emoji a filter-chip row ("All" + one chip per emoji with its
// count) narrows the list. The viewer's own row is tappable to remove their
// reaction.
CenteredDialog {
    id: root

    // List of {emoji, senderId, senderName, fromMe} maps.
    property var reactions: []
    property string messageId: ""
    // Emoji the reactor list is filtered to; empty shows all reactions.
    property string filterEmoji: ""

    readonly property string emojiFontFamily: Whatevr.AppController.emojiModel.emojiFontFamily

    // Distinct emoji with counts in first-seen order: [{emoji, count}].
    readonly property var groups: {
        const out = []
        const byEmoji = {}
        const list = reactions || []
        for (let i = 0; i < list.length; ++i) {
            const emoji = String(list[i].emoji || "")
            if (emoji.length === 0) {
                continue
            }
            if (byEmoji[emoji] === undefined) {
                byEmoji[emoji] = out.length
                out.push({ emoji: emoji, count: 1 })
            } else {
                out[byEmoji[emoji]].count += 1
            }
        }
        return out
    }

    // Reactions under the active filter, the viewer's own first.
    readonly property var visibleReactions: {
        let list = reactions ? reactions.slice() : []
        if (filterEmoji.length > 0) {
            list = list.filter(r => String(r.emoji || "") === filterEmoji)
        }
        list.sort((a, b) => Number(Boolean(b.fromMe)) - Number(Boolean(a.fromMe)))
        return list
    }

    title: Whatevr.I18n.i18nc("@title:dialog people who reacted to a message", "Reactions")
    standardButtons: Kirigami.Dialog.Close
    padding: Kirigami.Units.largeSpacing
    preferredWidth: Kirigami.Units.gridUnit * 20
    maximumHeight: Kirigami.Units.gridUnit * 24

    function openFor(reactionList, msgId) {
        reactions = reactionList || []
        messageId = msgId
        filterEmoji = ""
        open()
    }

    // A flat, boxless tab: the selected one is marked by a 2px accent underline
    // sized to its content, with a faint underline on hover for affordance.
    component FilterTab: AbstractButton {
        id: filterTab

        property bool selected: false

        leftPadding: Kirigami.Units.smallSpacing
        rightPadding: Kirigami.Units.smallSpacing
        topPadding: Math.round(Kirigami.Units.smallSpacing / 2)
        bottomPadding: Kirigami.Units.smallSpacing + 2
        hoverEnabled: true

        // Pin a shared height (tall enough for the larger emoji glyph) so the
        // "All" and emoji tabs centre their content on the same line and their
        // bottom-anchored underlines align.
        implicitHeight: Math.round(Kirigami.Units.gridUnit * 1.2) + topPadding + bottomPadding

        background: null

        Rectangle {
            anchors.bottom: parent.bottom
            anchors.horizontalCenter: parent.horizontalCenter
            width: filterTab.contentItem ? filterTab.contentItem.width : 0
            height: 2
            radius: 1
            color: Kirigami.Theme.highlightColor
            opacity: filterTab.selected ? 1 : (filterTab.hovered ? 0.25 : 0)

            Behavior on opacity {
                NumberAnimation { duration: Kirigami.Units.shortDuration }
            }
        }
    }

    ColumnLayout {
        // Kirigami.Dialog does not give a bare Layout a width, which collapses
        // the fillWidth rows and leaves the dialog body blank; pin it to the
        // dialog's preferred width like the other dialogs do.
        implicitWidth: root.preferredWidth
        spacing: Kirigami.Units.smallSpacing

        Flow {
            Layout.fillWidth: true
            visible: root.groups.length > 1
            spacing: Kirigami.Units.largeSpacing

            FilterTab {
                id: allTab

                selected: root.filterEmoji.length === 0
                onClicked: root.filterEmoji = ""

                contentItem: Label {
                    text: Whatevr.I18n.i18nc("@option:check show reactions with every emoji; %1 is their total count", "All %1", root.reactions.length)
                    color: allTab.selected ? Kirigami.Theme.highlightColor : Kirigami.Theme.textColor
                    verticalAlignment: Text.AlignVCenter
                    horizontalAlignment: Text.AlignHCenter
                }
            }

            Repeater {
                model: root.groups

                delegate: FilterTab {
                    id: emojiTab

                    required property var modelData

                    selected: root.filterEmoji === emojiTab.modelData.emoji
                    onClicked: root.filterEmoji = emojiTab.modelData.emoji

                    contentItem: Row {
                        spacing: Math.round(Kirigami.Units.smallSpacing / 2)

                        Text {
                            text: emojiTab.modelData.emoji
                            font.family: root.emojiFontFamily
                            font.pixelSize: Math.round(Kirigami.Units.gridUnit * 0.9)
                            renderType: Text.QtRendering
                            anchors.verticalCenter: parent.verticalCenter
                        }

                        Label {
                            text: emojiTab.modelData.count
                            color: Kirigami.Theme.disabledTextColor
                            font.pixelSize: Math.round(Kirigami.Theme.defaultFont.pixelSize * 0.8)
                            anchors.verticalCenter: parent.verticalCenter
                        }
                    }
                }
            }
        }

        Rectangle {
            Layout.fillWidth: true
            Layout.bottomMargin: Kirigami.Units.smallSpacing
            implicitHeight: 1
            visible: root.groups.length > 1
            color: Qt.alpha(Kirigami.Theme.textColor, 0.1)
        }

        Repeater {
            model: root.visibleReactions

            delegate: ItemDelegate {
                id: entry

                required property var modelData
                readonly property bool removable: Boolean(modelData.fromMe)

                Layout.fillWidth: true
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
}
