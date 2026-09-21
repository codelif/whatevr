pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Channel messages page: a read-only broadcast timeline for one channel.
// Subscribes the `channel_messages` view while on screen.
Kirigami.ScrollablePage {
    id: root

    required property string channelJid
    required property string channelName

    title: root.channelName

    Kirigami.Theme.colorSet: Kirigami.Theme.View

    Component.onCompleted: Whatevr.ProtocolController.openChannelMessages(root.channelJid, root.channelName)
    // Guarded: at engine teardown the singleton may already be null.
    Component.onDestruction: { const c = Whatevr.ProtocolController; if (c) c.closeChannelMessages() }

    property string lastMarkedKey: ""

    function markVisibleViewed() {
        if (!root.channelJid || root.channelJid.length === 0)
            return
        const model = Whatevr.ProtocolController.channelMessagesModel
        const ids = []
        if (!model)
            return
        for (let i = 0; i < model.count; ++i) {
            const item = model.itemById(model.idAt(i))
            if (item && Number(item.server_id) > 0)
                ids.push(Number(item.server_id))
        }
        if (ids.length === 0)
            return
        // Break the viewed→invalidate→countChanged→viewed feedback loop:
        // only send when the held id set actually grew.
        const key = root.channelJid + ":" + ids.join(",")
        if (key === root.lastMarkedKey)
            return
        root.lastMarkedKey = key
        Whatevr.ProtocolController.markChannelViewed(root.channelJid, ids)
    }

    Connections {
        target: Whatevr.ProtocolController.channelMessagesModel
        ignoreUnknownSignals: true
        function onReadyChanged() {
            if (Whatevr.ProtocolController.channelMessagesModel.ready)
                root.markVisibleViewed()
        }
        function onCountChanged() { root.markVisibleViewed() }
    }

    actions: [
        Kirigami.Action {
            icon.name: "list-remove-symbolic"
            text: Whatevr.I18n.i18nc("@action:button unfollow channel", "Unfollow")
            onTriggered: {
                Whatevr.ProtocolController.unfollowChannel(root.channelJid)
                applicationWindow().pageStack.layers.pop()
            }
        },
        Kirigami.Action {
            icon.name: "audio-volume-muted-symbolic"
            text: Whatevr.I18n.i18nc("@action:button mute/unmute channel", "Mute / Unmute")
            onTriggered: {
                // Toggle: read current mute state from the channel model if
                // available, otherwise default to muting.
                const model = Whatevr.ProtocolController.channelsModel
                let currentlyMuted = false
                if (model) {
                    const row = model.itemById(root.channelJid)
                    if (row) {
                        currentlyMuted = row.muted === true
                    }
                }
                Whatevr.ProtocolController.muteChannel(root.channelJid, !currentlyMuted)
            }
        }
    ]

    ListView {
        id: messagesList

        model: Whatevr.ProtocolController.channelMessagesModel
        currentIndex: -1
        reuseItems: true
        verticalLayoutDirection: ListView.BottomToTop
        Component.onDestruction: messagesList.model = null

        QQC2.BusyIndicator {
            anchors.centerIn: parent
            running: Whatevr.ProtocolController.channelMessagesLoading && messagesList.count === 0
            visible: running
        }

        Kirigami.PlaceholderMessage {
            anchors.centerIn: parent
            width: parent.width - Kirigami.Units.gridUnit * 4
            visible: messagesList.count === 0 && !Whatevr.ProtocolController.channelMessagesLoading
            icon.name: "rss-symbolic"
            text: Whatevr.I18n.i18nc("@info placeholder for channel messages", "No messages yet")
        }

        delegate: QQC2.ItemDelegate {
            id: msgDelegate

            required property var item

            width: ListView.view.width
            padding: Kirigami.Units.largeSpacing

            contentItem: ColumnLayout {
                spacing: Kirigami.Units.smallSpacing / 2

                RowLayout {
                    spacing: Kirigami.Units.smallSpacing

                    QQC2.Label {
                        text: msgDelegate.item.sender_name || ""
                        font.weight: Font.DemiBold
                        elide: Text.ElideRight
                        Layout.fillWidth: true
                    }

                    QQC2.Label {
                        text: {
                            const ts = msgDelegate.item.time || msgDelegate.item.timestamp || 0
                            if (ts <= 0) return ""
                            return Qt.formatTime(new Date(ts * 1000), Qt.DefaultLocaleShortDate)
                        }
                        color: Kirigami.Theme.disabledTextColor
                        font.pointSize: Kirigami.Theme.smallFont.pointSize
                    }
                }

                QQC2.Label {
                    Layout.fillWidth: true
                    text: msgDelegate.item.text || ""
                    wrapMode: Text.Wrap
                    visible: (msgDelegate.item.text || "").length > 0
                }

                QQC2.Label {
                    Layout.fillWidth: true
                    text: {
                        const mk = msgDelegate.item.media_kind || ""
                        if (mk === "") return ""
                        return "[" + mk + "]"
                    }
                    color: Kirigami.Theme.disabledTextColor
                    font.italic: true
                    visible: (msgDelegate.item.media_kind || "").length > 0
                }
            }

            QQC2.Menu {
                id: messageContextMenu

                QQC2.MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu copy channel message", "Copy text")
                    icon.name: "edit-copy-symbolic"
                    enabled: (msgDelegate.item.text || "").length > 0
                    onTriggered: Whatevr.ProtocolController.copyToClipboard(msgDelegate.item.text || "")
                }
                QQC2.MenuSeparator {}
                QQC2.MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu react to channel message", "Like")
                    icon.name: "heart-symbolic"
                    onTriggered: Whatevr.ProtocolController.reactToChannelMessage(
                        root.channelJid, msgDelegate.item.server_id || 0, "❤️")
                }
                QQC2.MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu remove channel reaction", "Remove reaction")
                    icon.name: "edit-clear-symbolic"
                    onTriggered: Whatevr.ProtocolController.reactToChannelMessage(
                        root.channelJid, msgDelegate.item.server_id || 0, "")
                }
            }

            TapHandler {
                acceptedButtons: Qt.RightButton
                onTapped: messageContextMenu.popup()
            }
        }
    }
}
