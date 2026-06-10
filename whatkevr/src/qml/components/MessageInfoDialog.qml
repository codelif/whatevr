pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Delivery/read details for one of our own messages: sent/delivered/read rows
// for direct chats, per-member sections (Read by / Delivered to / Pending) for
// groups. Data arrives asynchronously via AppController.requestMessageInfo.
Kirigami.Dialog {
    id: root

    property string messageId: ""
    property var info: null
    property bool loading: false

    readonly property bool infoValid: info !== null
    readonly property bool isGroup: infoValid && Boolean(info.isGroup)
    readonly property var receipts: infoValid && info.receipts ? info.receipts : []
    readonly property var readBy: receipts.filter(r => Number(r.readTsUnix) > 0)
    readonly property var deliveredTo: receipts.filter(r => Number(r.readTsUnix) <= 0 && Number(r.deliveredTsUnix) > 0)
    readonly property var pendingFor: receipts.filter(r => Number(r.deliveredTsUnix) <= 0)

    title: Whatevr.I18n.i18nc("@title:dialog delivery details of a message", "Message Info")
    standardButtons: Kirigami.Dialog.Close
    padding: Kirigami.Units.largeSpacing
    preferredWidth: Kirigami.Units.gridUnit * 22
    maximumHeight: Kirigami.Units.gridUnit * 28

    function openFor(id) {
        messageId = id
        info = null
        loading = true
        Whatevr.AppController.requestMessageInfo(id)
        open()
    }

    function formatTimestamp(ts) {
        const value = Number(ts)
        if (!value || value <= 0) {
            return "—"
        }
        const date = new Date(value * 1000)
        const today = new Date()
        const sameDay = date.getFullYear() === today.getFullYear()
                        && date.getMonth() === today.getMonth()
                        && date.getDate() === today.getDate()
        return sameDay
            ? Qt.formatTime(date, Qt.locale().timeFormat(Locale.ShortFormat))
            : Qt.formatDateTime(date, Qt.locale().dateTimeFormat(Locale.ShortFormat))
    }

    function initialsFor(name) {
        const parts = String(name || "").trim().split(/\s+/)
        let initials = ""
        for (const part of parts) {
            if (part.length > 0) {
                initials += part.charAt(0).toUpperCase()
            }
            if (initials.length >= 2) {
                break
            }
        }
        return initials.length > 0 ? initials : "?"
    }

    Connections {
        target: Whatevr.AppController

        function onMessageInfoReceived(id, receivedInfo) {
            if (id !== root.messageId) {
                return
            }
            root.info = receivedInfo
            root.loading = false
        }
    }

    component StatusRow: RowLayout {
        id: statusRow

        required property string label
        required property string value
        property string iconName: ""
        property color iconColor: Kirigami.Theme.disabledTextColor

        Layout.fillWidth: true
        spacing: Kirigami.Units.largeSpacing

        Kirigami.Icon {
            visible: statusRow.iconName.length > 0
            source: statusRow.iconName
            Layout.preferredWidth: Kirigami.Units.iconSizes.smallMedium
            Layout.preferredHeight: Kirigami.Units.iconSizes.smallMedium
            color: statusRow.iconColor
            isMask: true
        }

        Label {
            text: statusRow.label
            font.weight: Font.DemiBold
            Layout.preferredWidth: Kirigami.Units.gridUnit * 5
        }

        Label {
            text: statusRow.value
            color: statusRow.value === "—" ? Kirigami.Theme.disabledTextColor : Kirigami.Theme.textColor
            elide: Text.ElideRight
            Layout.fillWidth: true
            horizontalAlignment: Text.AlignRight
        }
    }

    component ParticipantRow: RowLayout {
        id: participantRow

        required property var receipt
        // Which timestamp this section shows (readTsUnix / deliveredTsUnix / none).
        property string timestampKey: ""

        Layout.fillWidth: true
        spacing: Kirigami.Units.smallSpacing

        AvatarImage {
            Layout.preferredWidth: Kirigami.Units.gridUnit * 1.5
            Layout.preferredHeight: Kirigami.Units.gridUnit * 1.5
            avatarLocalPath: String(participantRow.receipt.avatarLocalPath || "")
            initials: root.initialsFor(participantRow.receipt.displayName || participantRow.receipt.jid)
        }

        Label {
            text: {
                const name = String(participantRow.receipt.displayName || "").trim()
                return name.length > 0 ? name : String(participantRow.receipt.jid).split("@")[0]
            }
            elide: Text.ElideRight
            Layout.fillWidth: true
        }

        Label {
            visible: participantRow.timestampKey.length > 0
            text: root.formatTimestamp(participantRow.receipt[participantRow.timestampKey])
            color: Kirigami.Theme.disabledTextColor
            font.pointSize: Kirigami.Theme.smallFont.pointSize
        }
    }

    component SectionHeading: RowLayout {
        id: sectionHeading

        required property string label
        property string iconName: ""
        property color iconColor: Kirigami.Theme.disabledTextColor

        Layout.fillWidth: true
        Layout.topMargin: Kirigami.Units.largeSpacing
        spacing: Kirigami.Units.smallSpacing

        Kirigami.Icon {
            visible: sectionHeading.iconName.length > 0
            source: sectionHeading.iconName
            Layout.preferredWidth: Kirigami.Units.iconSizes.small
            Layout.preferredHeight: Kirigami.Units.iconSizes.small
            color: sectionHeading.iconColor
            isMask: true
        }

        Kirigami.Heading {
            text: sectionHeading.label
            level: 4
            Layout.fillWidth: true
        }
    }

    ColumnLayout {
        spacing: Kirigami.Units.smallSpacing

        BusyIndicator {
            visible: root.loading
            running: visible
            Layout.alignment: Qt.AlignHCenter
            Layout.margins: Kirigami.Units.largeSpacing
        }

        StatusRow {
            visible: root.infoValid
            iconName: "clock"
            label: Whatevr.I18n.i18nc("@label time the message was sent", "Sent")
            value: root.infoValid ? root.formatTimestamp(root.info.sentTsUnix) : "—"
        }

        StatusRow {
            visible: root.infoValid && !root.isGroup
            iconName: "qrc:/data/icons/checkmark-bold.svg"
            label: Whatevr.I18n.i18nc("@label time the message was delivered", "Delivered")
            value: root.infoValid ? root.formatTimestamp(root.info.deliveredTsUnix) : "—"
        }

        StatusRow {
            visible: root.infoValid && !root.isGroup
            iconName: "qrc:/data/icons/checkmark-bold.svg"
            iconColor: Kirigami.Theme.highlightColor
            label: Whatevr.I18n.i18nc("@label time the message was read", "Read")
            value: root.infoValid ? root.formatTimestamp(root.info.readTsUnix) : "—"
        }

        // Group sections: read / delivered / pending member lists.
        SectionHeading {
            visible: root.isGroup && root.readBy.length > 0
            iconName: "qrc:/data/icons/checkmark-bold.svg"
            iconColor: Kirigami.Theme.highlightColor
            label: Whatevr.I18n.i18nc("@title group members who read the message", "Read by %1", root.readBy.length)
        }

        Repeater {
            model: root.isGroup ? root.readBy : []
            delegate: ParticipantRow {
                required property var modelData
                receipt: modelData
                timestampKey: "readTsUnix"
            }
        }

        SectionHeading {
            visible: root.isGroup && root.deliveredTo.length > 0
            iconName: "qrc:/data/icons/checkmark-bold.svg"
            label: Whatevr.I18n.i18nc("@title group members the message reached", "Delivered to %1", root.deliveredTo.length)
        }

        Repeater {
            model: root.isGroup ? root.deliveredTo : []
            delegate: ParticipantRow {
                required property var modelData
                receipt: modelData
                timestampKey: "deliveredTsUnix"
            }
        }

        SectionHeading {
            visible: root.isGroup && root.pendingFor.length > 0
            iconName: "clock"
            label: Whatevr.I18n.i18nc("@title group members still waiting for the message", "Pending %1", root.pendingFor.length)
        }

        Repeater {
            model: root.isGroup ? root.pendingFor : []
            delegate: ParticipantRow {
                required property var modelData
                receipt: modelData
            }
        }

        Label {
            visible: root.isGroup && root.receipts.length === 0 && !root.loading
            text: Whatevr.I18n.i18nc("@info", "No delivery details are available for this message yet.")
            wrapMode: Text.Wrap
            color: Kirigami.Theme.disabledTextColor
            Layout.fillWidth: true
        }
    }
}
