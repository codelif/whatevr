pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Daemon logs page: subscribes the `daemon.logs` view for the lifetime of the
// page. Rows carry time, level, and text; error/warn levels get a tinted bg.
Kirigami.ScrollablePage {
    id: root

    title: Whatevr.I18n.i18nc("@title", "Logs")
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    Component.onCompleted: Whatevr.ProtocolController.openLogs()
    // Guarded: at engine teardown the singleton may already be null.
    Component.onDestruction: { const c = Whatevr.ProtocolController; if (c) c.closeLogs() }

    header: RowLayout {
        width: parent.width
        spacing: Kirigami.Units.smallSpacing
        Layout.margins: Kirigami.Units.smallSpacing

        QQC2.Button {
            icon.name: "edit-copy-symbolic"
            text: Whatevr.I18n.i18nc("@action:button copy all visible log lines", "Copy all")
            onClicked: root.copyAll()
        }
        QQC2.Button {
            icon.name: "edit-select-all-symbolic"
            text: Whatevr.I18n.i18nc("@action:button select all visible log text", "Select all")
            onClicked: root.selectAll()
        }
        QQC2.Button {
            icon.name: "folder-open-symbolic"
            text: Whatevr.I18n.i18nc("@action:button open the daemon log folder", "Open folder")
            onClicked: Whatevr.ProtocolController.openLogDirectory()
        }
    }

    function copyAll() {
        const lines = []
        for (let i = 0; i < logsList.count; ++i) {
            const entry = logsList.model.itemById(logsList.model.idAt(i))
            if (entry)
                lines.push(((entry.time || "") + " " + (entry.level || "") + " " + (entry.text || "")).trim())
        }
        if (lines.length > 0)
            Whatevr.ProtocolController.copyToClipboard(lines.join("\n"))
    }

    function selectAll() {
        for (let i = 0; i < logsList.count; ++i) {
            const delegate = logsList.itemAtIndex(i)
            if (delegate && delegate.logText) {
                delegate.logText.forceActiveFocus()
                delegate.logText.selectAll()
            }
        }
    }

    actions: [
        Kirigami.Action {
            icon.name: "edit-copy-symbolic"
            text: Whatevr.I18n.i18nc("@action:button copy all visible log lines", "Copy all")
            onTriggered: {
                const lines = []
                const count = logsList.count
                for (let i = 0; i < count; ++i) {
                    const entry = logsList.model.itemById(logsList.model.idAt(i))
                    if (entry) {
                        lines.push(((entry.time || "") + " " + (entry.level || "") + " " + (entry.text || "")).trim())
                    }
                }
                if (lines.length > 0) {
                    Whatevr.ProtocolController.copyToClipboard(lines.join("\n"))
                }
            }
        },
        Kirigami.Action {
            icon.name: "edit-select-all-symbolic"
            text: Whatevr.I18n.i18nc("@action:button select all visible log text", "Select all")
            onTriggered: {
                for (let i = 0; i < logsList.count; ++i) {
                    const delegate = logsList.itemAtIndex(i)
                    if (delegate && delegate.logText) {
                        delegate.logText.forceActiveFocus()
                        delegate.logText.selectAll()
                    }
                }
            }
        },
        Kirigami.Action {
            icon.name: "folder-open-symbolic"
            text: Whatevr.I18n.i18nc("@action:button open the daemon log folder", "Open log folder")
            onTriggered: Whatevr.ProtocolController.openLogDirectory()
        }
    ]

    ListView {
        id: logsList

        model: Whatevr.ProtocolController.logsModel
        currentIndex: -1
        reuseItems: true
        Component.onDestruction: logsList.model = null

        Kirigami.PlaceholderMessage {
            anchors.centerIn: parent
            width: parent.width - Kirigami.Units.gridUnit * 4
            visible: !Whatevr.ProtocolController.logsLoading && logsList.count === 0
            icon.name: "document-properties-symbolic"
            text: Whatevr.ProtocolController.logsErrorText.length > 0
                  ? Whatevr.I18n.i18nc("@info placeholder for the logs list", "Could not load logs")
                  : Whatevr.I18n.i18nc("@info placeholder for the logs list", "No log entries")
            explanation: Whatevr.ProtocolController.logsErrorText.length > 0
                         ? Whatevr.ProtocolController.logsErrorText
                         : Whatevr.I18n.i18nc("@info:placeholder", "Daemon log entries will appear here when available.")
        }

        QQC2.BusyIndicator {
            anchors.centerIn: parent
            running: Whatevr.ProtocolController.logsLoading
            visible: running
        }

        delegate: QQC2.ItemDelegate {
            id: logDelegate

            required property var item

            width: ListView.view.width
            hoverEnabled: false

            // Long-press copies the row (time + level + text).
            onPressAndHold: {
                const parts = []
                if (logDelegate.item) {
                    if (logDelegate.item.time) {
                        parts.push(logDelegate.item.time)
                    }
                    if (logDelegate.level) {
                        parts.push(logDelegate.level.toUpperCase())
                    }
                    if (logDelegate.item.text) {
                        parts.push(logDelegate.item.text)
                    }
                }
                if (parts.length > 0) {
                    Whatevr.ProtocolController.copyToClipboard(parts.join(" "))
                }
            }

            readonly property string level: (item && item.level) ? item.level : ""
            readonly property bool isError: level === "error" || level === "fatal" || level === "panic"
            readonly property bool isWarn: level === "warn" || level === "warning"

            background: Rectangle {
                color: logDelegate.isError
                       ? Qt.alpha(Kirigami.Theme.negativeTextColor, 0.08)
                       : logDelegate.isWarn
                         ? Qt.alpha(Kirigami.Theme.neutralTextColor, 0.08)
                         : "transparent"
            }

            contentItem: RowLayout {
                spacing: Kirigami.Units.largeSpacing

                // Time and level are read-only TextEdits, not Labels, so the
                // whole row — not just the message — is mouse/keyboard
                // selectable. Fixed single-line metrics keep the columns
                // aligned; overflow clips instead of eliding.
                TextEdit {
                    Layout.preferredWidth: Kirigami.Units.gridUnit * 8
                    Layout.alignment: Qt.AlignTop
                    text: (logDelegate.item && logDelegate.item.time) ? logDelegate.item.time : ""
                    font.family: "monospace"
                    font.pixelSize: Kirigami.Theme.smallFont.pixelSize
                    color: Kirigami.Theme.disabledTextColor
                    wrapMode: TextEdit.NoWrap
                    readOnly: true
                    selectByMouse: true
                    selectByKeyboard: true
                    persistentSelection: true
                    clip: true
                }

                TextEdit {
                    Layout.preferredWidth: Kirigami.Units.gridUnit * 3
                    Layout.alignment: Qt.AlignTop
                    text: logDelegate.level.toUpperCase()
                    font.family: "monospace"
                    font.pixelSize: Kirigami.Theme.smallFont.pixelSize
                    font.weight: Font.DemiBold
                    color: logDelegate.isError
                           ? Kirigami.Theme.negativeTextColor
                           : logDelegate.isWarn
                             ? Kirigami.Theme.neutralTextColor
                             : Kirigami.Theme.disabledTextColor
                    wrapMode: TextEdit.NoWrap
                    readOnly: true
                    selectByMouse: true
                    selectByKeyboard: true
                    persistentSelection: true
                    clip: true
                }

                TextEdit {
                    id: logText
                    objectName: "logText"
                    Layout.fillWidth: true
                    Layout.alignment: Qt.AlignTop
                    text: (logDelegate.item && logDelegate.item.text) ? logDelegate.item.text : ""
                    font.family: "monospace"
                    font.pixelSize: Kirigami.Theme.smallFont.pixelSize
                    color: Kirigami.Theme.textColor
                    wrapMode: TextEdit.Wrap
                    readOnly: true
                    selectByMouse: true
                    selectByKeyboard: true
                    persistentSelection: true
                }
            }

            QQC2.Menu {
                id: logContextMenu

                QQC2.MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu select log row", "Select row")
                    icon.name: "edit-select-all-symbolic"
                    onTriggered: {
                        logText.forceActiveFocus()
                        logText.selectAll()
                    }
                }

                QQC2.MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu copy log row", "Copy row")
                    icon.name: "edit-copy-symbolic"
                    onTriggered: Whatevr.ProtocolController.copyToClipboard(
                        ((logDelegate.item.time || "") + " " + logDelegate.level.toUpperCase() + " "
                         + (logDelegate.item.text || "")).trim())
                }

                QQC2.MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu copy all logs", "Copy all")
                    icon.name: "edit-copy-symbolic"
                    onTriggered: {
                        const lines = []
                        for (let i = 0; i < logsList.count; ++i) {
                            const entry = logsList.model.itemById(logsList.model.idAt(i))
                            if (entry)
                                lines.push(((entry.time || "") + " " + (entry.level || "") + " " + (entry.text || "")).trim())
                        }
                        Whatevr.ProtocolController.copyToClipboard(lines.join("\n"))
                    }
                }
            }

            TapHandler {
                acceptedButtons: Qt.RightButton
                onTapped: logContextMenu.popup()
            }
        }
    }
}
