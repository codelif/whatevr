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
    property string errorText: ""

    // Always-active highlight so the read ticks stay vivid when the window is
    // unfocused (Kirigami.Theme.highlightColor greys out on focus loss).
    SystemPalette {
        id: activePalette
        colorGroup: SystemPalette.Active
    }

    readonly property bool infoValid: info !== null
    readonly property bool isGroup: infoValid && Boolean(info.isGroup)
    readonly property var receipts: infoValid && info.receipts ? info.receipts : []
    readonly property var readBy: receipts.filter(r => Number(r.readTsUnix) > 0)
    readonly property var deliveredTo: receipts.filter(r => Number(r.readTsUnix) <= 0 && Number(r.deliveredTsUnix) > 0)

    title: Whatevr.I18n.i18nc("@title:dialog delivery details of a message", "Message Info")
    standardButtons: Kirigami.Dialog.Close
    padding: Kirigami.Units.largeSpacing
    preferredWidth: Kirigami.Units.gridUnit * 22
    maximumHeight: Kirigami.Units.gridUnit * 28

    function openFor(id) {
        messageId = id
        info = null
        errorText = ""
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

    function updateReceiptAvatar(senderId, avatarLocalPath) {
        if (!infoValid || !info.receipts || senderId.length === 0) {
            return
        }

        let changed = false
        const nextReceipts = []
        for (const receipt of info.receipts) {
            if (String(receipt.jid || "") === senderId
                    && String(receipt.avatarLocalPath || "") !== avatarLocalPath) {
                const nextReceipt = Object.assign({}, receipt)
                nextReceipt.avatarLocalPath = avatarLocalPath
                nextReceipts.push(nextReceipt)
                changed = true
            } else {
                nextReceipts.push(receipt)
            }
        }

        if (changed) {
            const nextInfo = Object.assign({}, info)
            nextInfo.receipts = nextReceipts
            info = nextInfo
        }
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

        function onMessageInfoFailed(id, error) {
            if (id !== root.messageId) {
                return
            }
            root.loading = false
            root.errorText = error
        }

        function onSenderAvatarUpdated(senderId, avatarLocalPath) {
            root.updateReceiptAvatar(senderId, avatarLocalPath)
        }
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
            visible: root.infoValid
            iconName: "qrc:/data/icons/checkmark-bold.svg"
            label: Whatevr.I18n.i18nc("@label time the message was sent", "Sent")
            value: root.infoValid ? root.formatTimestamp(root.info.sentTsUnix) : "—"
        }

        StatusRow {
            visible: root.infoValid && !root.isGroup
            iconName: "qrc:/data/icons/checkmark-bold.svg"
            doubleTick: true
            label: Whatevr.I18n.i18nc("@label time the message was delivered", "Delivered")
            value: root.infoValid ? root.formatTimestamp(root.info.deliveredTsUnix) : "—"
        }

        StatusRow {
            visible: root.infoValid && !root.isGroup
            iconName: "qrc:/data/icons/checkmark-bold.svg"
            doubleTick: true
            iconColor: activePalette.highlight
            label: Whatevr.I18n.i18nc("@label time the message was read", "Read")
            value: root.infoValid ? root.formatTimestamp(root.info.readTsUnix) : "—"
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
                timestampKey: "readTsUnix"
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
                timestampKey: "deliveredTsUnix"
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
