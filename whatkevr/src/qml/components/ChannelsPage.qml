pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Channels tab: followed channels, with a follow action and per-row tap to
// open the channel's messages. The page owns the daemon `channels`
// subscription while on screen.
Kirigami.ScrollablePage {
    id: root

    title: Whatevr.I18n.i18nc("@title", "Channels")
    property bool listOnly: false
    signal channelSelected(string channelId, string channelName)
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    Component.onCompleted: Whatevr.ProtocolController.openChannels()
    // Guarded: at engine teardown the singleton may already be null.
    Component.onDestruction: { const c = Whatevr.ProtocolController; if (c) c.closeChannels() }

    actions: [
        Kirigami.Action {
            icon.name: "list-add-symbolic"
            text: Whatevr.I18n.i18nc("@action:button follow a channel", "Follow Channel")
            onTriggered: followDialog.open()
        }
    ]

    ListView {
        id: channelsList

        model: Whatevr.ProtocolController.channelsModel
        currentIndex: -1
        reuseItems: true
        Component.onDestruction: channelsList.model = null

        QQC2.BusyIndicator {
            anchors.centerIn: parent
            running: Whatevr.ProtocolController.channelsLoading && channelsList.count === 0
            visible: running
        }

        Kirigami.PlaceholderMessage {
            anchors.centerIn: parent
            width: parent.width - Kirigami.Units.gridUnit * 4
            visible: channelsList.count === 0 && !Whatevr.ProtocolController.channelsLoading
            icon.name: "rss-symbolic"
            text: Whatevr.I18n.i18nc("@info placeholder for the channels list", "No channels")
            explanation: Whatevr.I18n.i18nc("@info:placeholder", "Follow a channel to see its broadcasts here.")
        }

        delegate: QQC2.ItemDelegate {
            id: channelDelegate

            required property var item

            width: ListView.view.width

            onClicked: {
                const jid = channelDelegate.item.jid || channelDelegate.item.id || ""
                const name = channelDelegate.item.name || ""
                if (root.listOnly) {
                    root.channelSelected(jid, name)
                } else {
                    applicationWindow().pageStack.layers.push(Qt.resolvedUrl("ChannelMessagesPage.qml"), {
                        "channelJid": jid, "channelName": name
                    })
                }
            }

            QQC2.Menu {
                id: channelContextMenu

                QQC2.MenuItem {
                    text: channelDelegate.item.muted === true
                        ? Whatevr.I18n.i18nc("@action:menu unmute channel", "Unmute channel")
                        : Whatevr.I18n.i18nc("@action:menu mute channel", "Mute channel")
                    icon.name: channelDelegate.item.muted === true
                        ? "audio-volume-high-symbolic" : "audio-volume-muted-symbolic"
                    onTriggered: Whatevr.ProtocolController.muteChannel(
                        channelDelegate.item.jid || channelDelegate.item.id || "",
                        channelDelegate.item.muted !== true)
                }

                QQC2.MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu unfollow channel", "Unfollow channel")
                    icon.name: "list-remove-symbolic"
                    onTriggered: Whatevr.ProtocolController.unfollowChannel(
                        channelDelegate.item.jid || channelDelegate.item.id || "")
                }
            }

            TapHandler {
                acceptedButtons: Qt.RightButton
                onTapped: channelContextMenu.popup()
            }

            contentItem: RowLayout {
                spacing: Kirigami.Units.largeSpacing

                Kirigami.Icon {
                    Layout.preferredWidth: Kirigami.Units.gridUnit * 2
                    Layout.preferredHeight: Kirigami.Units.gridUnit * 2
                    source: "rss-symbolic"
                }

                ColumnLayout {
                    Layout.fillWidth: true
                    spacing: Kirigami.Units.smallSpacing / 2

                    QQC2.Label {
                        Layout.fillWidth: true
                        text: channelDelegate.item.name || ""
                        font.weight: Font.DemiBold
                        elide: Text.ElideRight
                    }

                    QQC2.Label {
                        Layout.fillWidth: true
                        text: channelDelegate.item.description || ""
                        color: Kirigami.Theme.disabledTextColor
                        elide: Text.ElideRight
                        maximumLineCount: 1
                    }
                }

                Kirigami.Icon {
                    Layout.preferredWidth: Kirigami.Units.iconSizes.small
                    Layout.preferredHeight: Kirigami.Units.iconSizes.small
                    source: "audio-volume-muted-symbolic"
                    visible: channelDelegate.item.muted === true
                    opacity: 0.5
                }
            }
        }
    }

    Kirigami.PromptDialog {
        id: followDialog
        title: Whatevr.I18n.i18nc("@title:dialog", "Follow Channel")
        subtitle: Whatevr.I18n.i18nc("@info", "Paste a channel invite link or JID.")

        standardButtons: Kirigami.Dialog.Ok | Kirigami.Dialog.Cancel

        QQC2.TextField {
            id: followInput
            placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "https://whatsapp.com/channel/... or JID")
        }

        onAccepted: {
            const text = followInput.text.trim()
            if (text.length > 0) {
                Whatevr.ProtocolController.followChannel(text)
            }
            followInput.clear()
        }
        onRejected: followInput.clear()
    }
}
