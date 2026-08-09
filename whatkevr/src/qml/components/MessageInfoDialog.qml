pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Delivery/read details for one of our own messages: sent/delivered/read rows
// for direct chats, per-member sections (Read by / Delivered to) for groups.
// Rows come from the daemon's `receipts` view, subscribed for exactly as long as
// this dialog is open, and update live while it is (a member reading the message
// re-upserts their row). The dialog keeps no receipt state of its own.
CenteredDialog {
    id: root

    property string messageId: ""

    // Always-active highlight so the read ticks stay vivid when the window is
    // unfocused (Kirigami.Theme.highlightColor greys out on focus loss).
    SystemPalette {
        id: activePalette
        colorGroup: SystemPalette.Active
    }

    readonly property bool loading: Whatevr.ProtocolController.messageReceiptsLoading
    readonly property string errorText: Whatevr.ProtocolController.messageReceiptsError
    readonly property bool isGroup: Whatevr.ProtocolController.messageReceiptsIsGroup
    // A bare invokable call is not reactive; keying it on the revision counter
    // re-evaluates these on every view change.
    readonly property var receipts: Whatevr.ProtocolController.messageReceiptsRevision >= 0
                                    ? Whatevr.ProtocolController.messageReceipts() : []
    readonly property var directReceipt: Whatevr.ProtocolController.messageReceiptsRevision >= 0
                                         ? Whatevr.ProtocolController.directMessageReceipt() : ({})
    // Presentation-only grouping of rows the frontend already has; the daemon
    // owns their order (rule 3), which each section preserves.
    readonly property var readBy: receipts.filter(r => Number(r.read_ts_unix) > 0)
    readonly property var deliveredTo: receipts.filter(r => Number(r.read_ts_unix) <= 0 && Number(r.delivered_ts_unix) > 0)

    title: Whatevr.I18n.i18nc("@title:dialog delivery details of a message", "Message Info")
    standardButtons: Kirigami.Dialog.Close
    padding: Kirigami.Units.largeSpacing
    preferredWidth: Kirigami.Units.gridUnit * 22
    maximumHeight: Kirigami.Units.gridUnit * 28

    function openFor(id) {
        messageId = id
        Whatevr.ProtocolController.openMessageReceipts(id)
        open()
    }

    onClosed: {
        messageId = ""
        Whatevr.ProtocolController.closeMessageReceipts()
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

    // Sent shows a single tick, delivered/read show overlapping double ticks,
    // mirroring the bubble footer. A fixed width is reserved for the second
    // tick so single- and double-tick rows keep their labels aligned.
    component DeliveryTicks: Item {
        id: ticks

        property string iconName: ""
        property color iconColor: Kirigami.Theme.disabledTextColor
        property bool doubleTick: false
        property real iconSize: Kirigami.Units.iconSizes.small
        readonly property real tickOffset: Math.round(iconSize * 0.36)

        visible: iconName.length > 0
        implicitWidth: iconSize + tickOffset
        implicitHeight: iconSize

        Kirigami.Icon {
            x: 0
            anchors.verticalCenter: parent.verticalCenter
            source: ticks.iconName
            width: ticks.iconSize
            height: ticks.iconSize
            color: ticks.iconColor
            isMask: true
        }

        Kirigami.Icon {
            visible: ticks.doubleTick
            x: ticks.tickOffset
            anchors.verticalCenter: parent.verticalCenter
            source: ticks.iconName
            width: ticks.iconSize
            height: ticks.iconSize
            color: ticks.iconColor
            isMask: true
        }
    }

    component StatusRow: RowLayout {
        id: statusRow

        required property string label
        required property string value
        property string iconName: ""
        property color iconColor: Kirigami.Theme.disabledTextColor
        property bool doubleTick: false

        Layout.fillWidth: true
        spacing: Kirigami.Units.largeSpacing

        DeliveryTicks {
            iconName: statusRow.iconName
            iconColor: statusRow.iconColor
            doubleTick: statusRow.doubleTick
            Layout.preferredWidth: implicitWidth
            Layout.preferredHeight: implicitHeight
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
            avatarLocalPath: String(participantRow.receipt.avatar_path || "")
            initials: root.initialsFor(participantRow.receipt.name || participantRow.receipt.id)
        }

        Label {
            text: {
                const name = String(participantRow.receipt.name || "").trim()
                return name.length > 0 ? name : String(participantRow.receipt.id).split("@")[0]
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
        property bool doubleTick: false

        Layout.fillWidth: true
        Layout.topMargin: Kirigami.Units.largeSpacing
        spacing: Kirigami.Units.smallSpacing

        DeliveryTicks {
            iconName: sectionHeading.iconName
            iconColor: sectionHeading.iconColor
            doubleTick: sectionHeading.doubleTick
            Layout.preferredWidth: implicitWidth
            Layout.preferredHeight: implicitHeight
        }

        Kirigami.Heading {
            text: sectionHeading.label
            level: 4
            Layout.fillWidth: true
        }
    }

    ColumnLayout {
        // Kirigami.Dialog does not give a bare Layout a width, which collapses
        // the fillWidth rows and leaves the dialog body blank; pin it to the
        // dialog's preferred width like the other dialogs do.
        implicitWidth: root.preferredWidth
        spacing: Kirigami.Units.smallSpacing

        BusyIndicator {
            visible: root.loading
            running: visible
            Layout.alignment: Qt.AlignHCenter
            Layout.margins: Kirigami.Units.largeSpacing
        }

        Label {
            visible: root.errorText.length > 0
            text: root.errorText
            wrapMode: Text.Wrap
            color: Kirigami.Theme.negativeTextColor
            Layout.fillWidth: true
        }

        StatusRow {
            visible: !root.loading && root.errorText.length === 0
            iconName: "qrc:/data/icons/checkmark-bold.svg"
            label: Whatevr.I18n.i18nc("@label time the message was sent", "Sent")
            value: root.formatTimestamp(Whatevr.ProtocolController.messageReceiptsSentTimestamp)
        }

        StatusRow {
            visible: !root.loading && root.errorText.length === 0 && !root.isGroup
            iconName: "qrc:/data/icons/checkmark-bold.svg"
            doubleTick: true
            label: Whatevr.I18n.i18nc("@label time the message was delivered", "Delivered")
            value: root.formatTimestamp(root.directReceipt.delivered_ts_unix)
        }

        StatusRow {
            visible: !root.loading && root.errorText.length === 0 && !root.isGroup
            iconName: "qrc:/data/icons/checkmark-bold.svg"
            doubleTick: true
            iconColor: activePalette.highlight
            label: Whatevr.I18n.i18nc("@label time the message was read", "Read")
            value: root.formatTimestamp(root.directReceipt.read_ts_unix)
        }

        // Group sections: read / delivered member lists.
        SectionHeading {
            visible: root.isGroup && root.readBy.length > 0
            iconName: "qrc:/data/icons/checkmark-bold.svg"
            doubleTick: true
            iconColor: activePalette.highlight
            label: Whatevr.I18n.i18nc("@title group members who read the message", "Read by %1", root.readBy.length)
        }

        Repeater {
            model: root.isGroup ? root.readBy : []
            delegate: ParticipantRow {
                required property var modelData
                receipt: modelData
                timestampKey: "read_ts_unix"
            }
        }

        SectionHeading {
            visible: root.isGroup && root.deliveredTo.length > 0
            iconName: "qrc:/data/icons/checkmark-bold.svg"
            doubleTick: true
            label: Whatevr.I18n.i18nc("@title group members the message reached", "Delivered to %1", root.deliveredTo.length)
        }

        Repeater {
            model: root.isGroup ? root.deliveredTo : []
            delegate: ParticipantRow {
                required property var modelData
                receipt: modelData
                timestampKey: "delivered_ts_unix"
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
