pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr
import "Initials.js" as Initials

// Status tab: one row per contact with an unexpired status, newest ring first.
// The page owns the daemon `status` subscription while on screen and groups
// the flat status rows per contact itself (presentation-side, over rows it
// already holds). Tapping a contact opens its statuses in the viewer; the "+"
// action posts a text or photo status of your own.
Kirigami.ScrollablePage {
    id: root

    title: Whatevr.I18n.i18nc("@title", "Status")
    property bool listOnly: false
    signal statusSelected(string senderId, string senderName)
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    Component.onCompleted: {
        Whatevr.ProtocolController.openStatus()
        root.rebuildGroups()
    }
    // Guarded: at engine teardown the singleton may already be null.
    Component.onDestruction: { const c = Whatevr.ProtocolController; if (c) c.closeStatus() }

    // Contact groups rebuilt from the flat model: newest status first, so the
    // first time a sender appears is its recency rank. Each entry: {senderId,
    // senderName, latest, total, unviewed, statusIds, section, kept, muted}.
    // Kept contacts whose statuses all expired sort under "Archived" at the
    // top instead of vanishing; recent contacts (anything newer than 24h)
    // follow under "Recent" with unviewed contacts above watched ones; muted
    // contacts collect under "Muted" at the bottom instead of the main list.
    property var contactGroups: []
    // Keep-enabled sender ids from the `status.kept` view, as a lookup map.
    property var keptSenders: ({})
    // Muted sender ids from the `status.muted` view, as a lookup map.
    property var mutedSenders: ({})

    // WhatsApp statuses live 24 hours; older rows are archive material.
    readonly property int statusExpirySecs: 24 * 60 * 60

    function rebuildGroups() {
        const kept = {}
        const kmodel = Whatevr.ProtocolController.keptStatusModel
        const kcount = kmodel ? kmodel.count : 0
        for (let i = 0; i < kcount; ++i) {
            kept[kmodel.idAt(i)] = true
        }
        root.keptSenders = kept
        const muted = {}
        const mmodel = Whatevr.ProtocolController.mutedStatusModel
        const mcount = mmodel ? mmodel.count : 0
        for (let i = 0; i < mcount; ++i) {
            muted[mmodel.idAt(i)] = true
        }
        root.mutedSenders = muted

        const model = Whatevr.ProtocolController.statusModel
        const groups = []
        const bySender = {}
        const count = model ? model.count : 0
        for (let i = 0; i < count; ++i) {
            const item = model.itemById(model.idAt(i))
            if (!item || !item.id) {
                continue
            }
            const sender = item.sender || {}
            const senderId = sender.id || ""
            if (!senderId) {
                continue
            }
            let group = bySender[senderId]
            if (!group) {
                group = {
                    "senderId": senderId,
                    "senderName": sender.name || senderId,
                    "avatarPath": sender.avatar_path || "",
                    "thumbPath": "",
                    "latest": 0,
                    "total": 0,
                    "unviewed": 0,
                    "statusIds": []
                }
                bySender[senderId] = group
                groups.push(group)
            }
            // A later row of the same sender may carry an avatar the first one
            // lacked; keep the freshest non-empty value.
            if (!group.avatarPath && sender.avatar_path) {
                group.avatarPath = sender.avatar_path
            }
            // Rows arrive newest-first, so the first media thumbnail seen is
            // the latest status's: it becomes the ring's picture.
            if (!group.thumbPath && item.media && item.media.thumbnail_path) {
                group.thumbPath = item.media.thumbnail_path
            }
            group.statusIds.push(item.id)
            group.total += 1
            if (!item.viewed) {
                group.unviewed += 1
            }
            if (item.timestamp > group.latest) {
                group.latest = item.timestamp
            }
        }
        const now = Math.floor(Date.now() / 1000)
        const recentUnviewed = []
        const recentViewed = []
        const archived = []
        const mutedGroups = []
        for (let i = 0; i < groups.length; ++i) {
            const group = groups[i]
            const expired = (now - group.latest) > root.statusExpirySecs
            group.kept = Boolean(kept[group.senderId])
            group.muted = Boolean(muted[group.senderId])
            // Expiry first: muted contacts age out like everyone else —
            // expired rows vanish unless kept (which archives them). Only
            // unexpired muted contacts collect under Muted.
            if (expired && !group.kept) {
                continue
            }
            if (group.muted && !expired) {
                group.section = Whatevr.I18n.i18nc("@title:section muted statuses", "Muted")
                mutedGroups.push(group)
                continue
            }
            if (expired) {
                group.section = Whatevr.I18n.i18nc("@title:section expired kept statuses", "Archived")
                archived.push(group)
            } else {
                group.section = Whatevr.I18n.i18nc("@title:section recent statuses", "Recent")
                if (group.unviewed > 0) {
                    recentUnviewed.push(group)
                } else {
                    recentViewed.push(group)
                }
            }
        }
        root.contactGroups = archived.concat(recentUnviewed, recentViewed, mutedGroups)
    }

    function contactLabel(group) {
        if (group.senderId === "me") {
            return Whatevr.I18n.i18nc("@item status contact", "My status")
        }
        return group.senderName
    }

    // Entry point for the embedding column's header button: the page's own
    // actions never reach a toolbar when it is loaded list-only inside
    // ChatListPane rather than pushed on the page stack.
    function openPostDialog() {
        postDialog.open()
    }

    Connections {
        target: Whatevr.ProtocolController

        function onStatusChanged() {
            root.rebuildGroups()
        }
    }

    // The status subscription delivers rows through CollectionViewModel after
    // openStatus() resolves; row churn only raises the model's own signals, so
    // a rebuild keyed on statusChanged alone left the page showing whatever
    // was there on the last explicit event (often nothing, right after open).
    Connections {
        target: Whatevr.ProtocolController.statusModel

        function onCountChanged() {
            root.rebuildGroups()
        }

        function onReadyChanged() {
            root.rebuildGroups()
        }
    }

    actions: [
        Kirigami.Action {
            icon.name: "list-add-symbolic"
            text: Whatevr.I18n.i18nc("@action:button post a status", "New status")
            onTriggered: postDialog.open()
        }
    ]

    ListView {
        id: statusList

        model: root.contactGroups
        currentIndex: -1
        reuseItems: true

        section.property: "section"
        section.delegate: QQC2.Label {
            required property string section

            text: section
            font.weight: Font.DemiBold
            color: Kirigami.Theme.disabledTextColor
            leftPadding: Kirigami.Units.largeSpacing
            topPadding: Kirigami.Units.largeSpacing
        }

        onAtYEndChanged: if (atYEnd) {
            Whatevr.ProtocolController.loadMoreStatus()
        }
        Component.onDestruction: statusList.model = null

        QQC2.BusyIndicator {
            anchors.centerIn: parent
            running: Whatevr.ProtocolController.statusLoading && statusList.count === 0
            visible: running
        }

        Kirigami.PlaceholderMessage {
            anchors.centerIn: parent
            width: parent.width - Kirigami.Units.gridUnit * 4
            visible: statusList.count === 0 && !Whatevr.ProtocolController.statusLoading
            icon.name: "camera-photo-symbolic"
            text: Whatevr.I18n.i18nc("@info placeholder for the status list", "No recent statuses")
            explanation: Whatevr.I18n.i18nc("@info:placeholder", "Statuses from your contacts appear here for 24 hours.")
        }

        delegate: QQC2.ItemDelegate {
            id: statusDelegate

            required property var modelData
            readonly property var group: modelData

            width: ListView.view.width

            onClicked: {
                if (root.listOnly) {
                    root.statusSelected(statusDelegate.group.senderId, root.contactLabel(statusDelegate.group))
                } else {
                    applicationWindow().pageStack.layers.push(Qt.resolvedUrl("StatusViewerPage.qml"), {
                        "senderId": statusDelegate.group.senderId,
                        "senderName": root.contactLabel(statusDelegate.group)
                    })
                }
            }

            QQC2.Menu {
                id: statusContextMenu

                QQC2.MenuItem {
                    text: statusDelegate.group.kept
                        ? Whatevr.I18n.i18nc("@action:menu remove status archive", "Remove from archive")
                        : Whatevr.I18n.i18nc("@action:menu archive statuses", "Archive statuses")
                    icon.name: statusDelegate.group.kept ? "bookmark-remove-symbolic" : "bookmark-new-symbolic"
                    onTriggered: Whatevr.ProtocolController.setStatusKeepSender(
                        statusDelegate.group.senderId, !statusDelegate.group.kept)
                }

                QQC2.MenuItem {
                    text: statusDelegate.group.muted
                        ? Whatevr.I18n.i18nc("@action:menu show hidden statuses", "Show hidden statuses")
                        : Whatevr.I18n.i18nc("@action:menu hide statuses", "Hide statuses")
                    icon.name: statusDelegate.group.muted
                        ? "notifications-symbolic" : "notifications-disabled-symbolic"
                    onTriggered: Whatevr.ProtocolController.setStatusMuteSender(
                        statusDelegate.group.senderId, !statusDelegate.group.muted)
                }
            }

            TapHandler {
                acceptedButtons: Qt.RightButton
                onTapped: statusContextMenu.popup()
            }

            contentItem: RowLayout {
                spacing: Kirigami.Units.largeSpacing

                // Ring: highlighted while any of the contact's statuses is
                // unviewed, plain once all are seen.
                Rectangle {
                    Layout.preferredWidth: Kirigami.Units.gridUnit * 2.4
                    Layout.preferredHeight: Kirigami.Units.gridUnit * 2.4
                    radius: width / 2
                    color: "transparent"
                    border.width: statusDelegate.group.unviewed > 0 ? Math.max(2, Kirigami.Units.smallSpacing / 2) : 1
                    border.color: statusDelegate.group.unviewed > 0
                        ? Kirigami.Theme.highlightColor
                        : Qt.alpha(Kirigami.Theme.textColor, 0.25)

                    AvatarImage {
                        anchors.fill: parent
                        anchors.margins: parent.border.width + 1
                        // Newest status thumbnail first (low-res by nature),
                        // profile avatar as fallback.
                        avatarLocalPath: statusDelegate.group.thumbPath || statusDelegate.group.avatarPath
                        initials: Initials.firstTwo(statusDelegate.group.senderName)
                        backgroundColor: Qt.alpha(Kirigami.Theme.highlightColor, 0.18)
                    }
                }

                ColumnLayout {
                    Layout.fillWidth: true
                    spacing: Kirigami.Units.smallSpacing / 2

                    QQC2.Label {
                        Layout.fillWidth: true
                        text: root.contactLabel(statusDelegate.group)
                        elide: Text.ElideRight
                        font.weight: statusDelegate.group.unviewed > 0 ? Font.DemiBold : Font.Normal
                    }

                    QQC2.Label {
                        Layout.fillWidth: true
                        text: statusDelegate.group.unviewed > 0
                            ? Whatevr.I18n.i18nc("@info status count", "%1 new", statusDelegate.group.unviewed)
                            : Whatevr.I18n.i18nc("@info status count", "%1 total", statusDelegate.group.total)
                        color: Kirigami.Theme.disabledTextColor
                        elide: Text.ElideRight
                    }
                }

                // Per-contact keep: expired statuses of kept contacts collect
                // under Archived instead of vanishing after 24 hours.
                // onClicked (not onToggled): recycled delegates change groups
                // without user input, and a toggled edge then fires for the
                // wrong contact.
                QQC2.ToolButton {
                    icon.name: statusDelegate.group.kept ? "bookmark-symbolic" : "bookmark-new-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button keep a contact's expired statuses", "Keep")
                    display: QQC2.AbstractButton.IconOnly
                    checkable: true
                    checked: statusDelegate.group.kept
                    onClicked: Whatevr.ProtocolController.setStatusKeepSender(statusDelegate.group.senderId, checked)

                    QQC2.ToolTip.visible: hovered
                    QQC2.ToolTip.text: statusDelegate.group.kept
                        ? Whatevr.I18n.i18nc("@info:tooltip kept statuses", "Kept: expired statuses stay archived here")
                        : text
                    QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
                }

                // Per-contact mute: muted contacts collect under Muted at the
                // bottom instead of the main list. onClicked (not onToggled),
                // like Keep above: recycled delegates must not command for
                // the wrong contact.
                QQC2.ToolButton {
                    icon.name: statusDelegate.group.muted ? "notifications-disabled-symbolic" : "notifications-symbolic"
                    text: Whatevr.I18n.i18nc("@action:button mute a contact's statuses", "Mute")
                    display: QQC2.AbstractButton.IconOnly
                    checkable: true
                    checked: statusDelegate.group.muted
                    onClicked: Whatevr.ProtocolController.setStatusMuteSender(statusDelegate.group.senderId, checked)

                    QQC2.ToolTip.visible: hovered
                    QQC2.ToolTip.text: statusDelegate.group.muted
                        ? Whatevr.I18n.i18nc("@info:tooltip muted statuses", "Muted: statuses stay hidden here")
                        : text
                    QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
                }
            }
        }
    }

    StatusPostDialog {
        id: postDialog
    }
}
