pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Scheduled-messages viewer session: pending one-shot sends for one chat
// (or every chat), soonest first, with per-row cancel. The page refreshes on
// open; sent rows disappear via the daemon scheduler, cancelled rows via the
// cancel action below.
Kirigami.ScrollablePage {
    id: root

    property string chatId: ""
    property string chatName: ""

    title: chatName.length > 0
        ? Whatevr.I18n.i18nc("@title scheduled messages for one chat", "Scheduled in %1", chatName)
        : Whatevr.I18n.i18nc("@title", "Scheduled messages")
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    Component.onCompleted: Whatevr.ProtocolController.refreshScheduledMessages(root.chatId)

    Connections {
        target: Whatevr.ProtocolController
        function onScheduledMessagesChanged() { scheduledList.model = Whatevr.ProtocolController.scheduledMessages }
    }
    Component.onDestruction: scheduledList.model = null

    ListView {
        id: scheduledList

        model: Whatevr.ProtocolController.scheduledMessages
        currentIndex: -1
        reuseItems: true

        Kirigami.PlaceholderMessage {
            anchors.centerIn: parent
            width: parent.width - Kirigami.Units.gridUnit * 4
            visible: scheduledList.count === 0
            icon.name: "appointment-new-symbolic"
            text: Whatevr.I18n.i18nc("@info placeholder for scheduled messages", "No scheduled messages")
            explanation: Whatevr.I18n.i18nc("@info:placeholder", "Schedule a message from the composer to see it here.")
        }

        delegate: QQC2.ItemDelegate {
            id: scheduledDelegate

            required property var modelData
            readonly property var row: modelData

            width: ListView.view.width

            contentItem: RowLayout {
                spacing: Kirigami.Units.largeSpacing

                ColumnLayout {
                    Layout.fillWidth: true
                    spacing: Kirigami.Units.smallSpacing / 2

                    QQC2.Label {
                        Layout.fillWidth: true
                        text: scheduledDelegate.row.text || ""
                        wrapMode: Text.Wrap
                        maximumLineCount: 3
                        elide: Text.ElideRight
                    }

                    QQC2.Label {
                        Layout.fillWidth: true
                        text: {
                            const ts = Number(scheduledDelegate.row.send_at || 0)
                            if (ts <= 0)
                                return ""
                            return Qt.formatDateTime(new Date(ts * 1000), Qt.DefaultLocaleShortDate)
                        }
                        color: Kirigami.Theme.disabledTextColor
                        font: Kirigami.Theme.smallFont
                        elide: Text.ElideRight
                    }
                }

                QQC2.ToolButton {
                    Layout.alignment: Qt.AlignVCenter
                    icon.name: "edit-delete-remove-symbolic"
                    display: QQC2.AbstractButton.IconOnly
                    text: Whatevr.I18n.i18nc("@action:button cancel scheduled message", "Cancel")
                    onClicked: Whatevr.ProtocolController.cancelScheduledMessage(
                        Number(scheduledDelegate.row.id || 0), root.chatId)
                }
            }
        }
    }
}
