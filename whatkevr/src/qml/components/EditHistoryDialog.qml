pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Every saved version of an edited message, oldest first in storage. The live
// row holds the current version, so the dialog shows it tagged "Current" on
// top and every superseded body below with its edit time — which is which at
// a glance.
CenteredDialog {
    id: root

    property string messageId: ""
    property string currentText: ""
    // List of {text, edited_at} maps, oldest first (daemon order).
    property var edits: []

    title: Whatevr.I18n.i18nc("@title:dialog previous versions of an edited message", "Edit history")
    standardButtons: Kirigami.Dialog.Close
    padding: Kirigami.Units.largeSpacing
    preferredWidth: Kirigami.Units.gridUnit * 22
    maximumHeight: Kirigami.Units.gridUnit * 26

    function openFor(msgId, current) {
        messageId = msgId
        currentText = current || ""
        edits = []
        Whatevr.ProtocolController.requestEditHistory(msgId)
        open()
    }

    function formatEditTime(millis) {
        const value = Number(millis)
        if (!value || value <= 0) {
            return ""
        }
        const date = new Date(value)
        const today = new Date()
        const sameDay = date.getFullYear() === today.getFullYear()
                        && date.getMonth() === today.getMonth()
                        && date.getDate() === today.getDate()
        return sameDay
            ? Qt.formatTime(date, Qt.locale().timeFormat(Locale.ShortFormat))
            : Qt.formatDateTime(date, Qt.locale().dateTimeFormat(Locale.ShortFormat))
    }

    Connections {
        target: Whatevr.ProtocolController

        function onEditHistoryReady(id, list) {
            if (id === root.messageId) {
                root.edits = list || []
            }
        }
    }

    ColumnLayout {
        implicitWidth: root.preferredWidth
        spacing: Kirigami.Units.smallSpacing

        // Current version first, tagged so it cannot be confused with history.
        Kirigami.Heading {
            text: Whatevr.I18n.i18nc("@label current message version", "Current version")
            level: 4
            Layout.fillWidth: true
        }

        Label {
            text: root.currentText
            wrapMode: Text.Wrap
            Layout.fillWidth: true
        }

        Kirigami.Separator {
            visible: root.edits.length > 0
            Layout.fillWidth: true
        }

        Kirigami.Heading {
            visible: root.edits.length > 0
            text: Whatevr.I18n.i18nc("@label superseded message versions", "Previous versions")
            level: 4
            Layout.fillWidth: true
        }

        ListView {
            visible: root.edits.length > 0
            // Newest superseded version first: the most relevant comparison.
            model: root.edits.slice().reverse()
            Layout.fillWidth: true
            Layout.preferredHeight: Math.min(contentHeight, Kirigami.Units.gridUnit * 14)
            clip: true

            delegate: ItemDelegate {
                id: editDelegate

                required property var modelData
                required property int index

                width: ListView.view.width
                hoverEnabled: false

                contentItem: ColumnLayout {
                    spacing: Kirigami.Units.smallSpacing / 2

                    Label {
                        text: root.formatEditTime(editDelegate.modelData.edited_at)
                        visible: text.length > 0
                        font.pointSize: Kirigami.Theme.smallFont.pointSize
                        color: Kirigami.Theme.disabledTextColor
                        Layout.fillWidth: true
                    }

                    Label {
                        text: String(editDelegate.modelData.text || "")
                        wrapMode: Text.Wrap
                        color: Kirigami.Theme.disabledTextColor
                        Layout.fillWidth: true
                    }
                }
            }
        }
    }
}
