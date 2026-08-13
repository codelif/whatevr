// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as Controls
import QtQuick.Layouts

import org.kde.kirigami as Kirigami

import Whatevr as Whatevr

/**
 * A document: MIME icon, filename, and the facts that decide whether you want
 * to open it (size, page count, type).
 *
 * Tapping downloads it when it is not here yet, and otherwise hands it to the
 * system's default application. Saving it elsewhere is the context menu's job.
 */
Item {
    id: root

    required property ChatBubble row

    readonly property bool hasFile: row.mediaLocalPath.length > 0
    readonly property string displayName: row.mediaFileName.length > 0
        ? row.mediaFileName
        : Whatevr.I18n.i18nc("@label unnamed attachment", "Document")

    function humanSize(bytes) {
        if (!bytes || bytes <= 0)
            return ""
        const units = ["B", "kB", "MB", "GB"]
        let value = bytes
        let unit = 0
        while (value >= 1024 && unit < units.length - 1) {
            value /= 1024
            unit++
        }
        return (unit === 0 ? value.toFixed(0) : value.toFixed(1)) + " " + units[unit]
    }

    readonly property string detailText: {
        const parts = []
        const size = humanSize(row.mediaSizeBytes)
        if (size.length > 0)
            parts.push(size)
        if (row.mediaPageCount > 0)
            parts.push(Whatevr.I18n.i18ncp("@label document length", "%1 page", "%1 pages", row.mediaPageCount))
        const extension = displayName.includes(".")
            ? displayName.split(".").pop().toUpperCase()
            : ""
        if (extension.length > 0 && extension.length <= 5)
            parts.push(extension)
        return parts.join(" · ")
    }

    function activate() {
        if (row.messageId.length === 0)
            return
        if (hasFile) {
            if (!Whatevr.ProtocolController.openLocalFile(row.mediaLocalPath))
                root.row.conversationFocusRequested()
            return
        }
        if (!row.mediaDownloading)
            Whatevr.ProtocolController.downloadMessageMedia(row.messageId)
    }

    Rectangle {
        anchors.fill: parent
        radius: Kirigami.Units.cornerRadius
        color: Qt.alpha(Kirigami.Theme.textColor, 0.06)
        border.color: Qt.alpha(Kirigami.Theme.textColor, 0.12)
        border.width: 1
    }

    RowLayout {
        anchors.fill: parent
        anchors.margins: Kirigami.Units.smallSpacing
        spacing: Kirigami.Units.smallSpacing

        Item {
            Layout.alignment: Qt.AlignVCenter
            implicitWidth: Kirigami.Units.iconSizes.large
            implicitHeight: Kirigami.Units.iconSizes.large

            Kirigami.Icon {
                anchors.fill: parent
                visible: !root.row.mediaDownloading
                // The MIME type names the icon; the icon theme falls back to a
                // generic document glyph for anything it does not know.
                source: root.row.mediaMimeType.length > 0
                    ? root.row.mediaMimeType.replace("/", "-")
                    : "text-x-generic"
                fallback: "text-x-generic"
            }

            ProgressCircle {
                anchors.fill: parent
                visible: root.row.mediaDownloading && root.row.mediaDownloadProgress >= 0
                progress: Math.max(0, root.row.mediaDownloadProgress)
            }

            Controls.BusyIndicator {
                anchors.fill: parent
                visible: root.row.mediaDownloading && root.row.mediaDownloadProgress < 0
                running: visible
            }
        }

        ColumnLayout {
            Layout.fillWidth: true
            // The time and ticks sit at the bottom right of the block, on the
            // detail line, instead of taking a row of their own underneath it.
            Layout.rightMargin: root.row.tntReserveWidth
            spacing: 0

            Controls.Label {
                Layout.fillWidth: true
                text: root.displayName
                elide: Text.ElideMiddle
                maximumLineCount: 1
                font.pointSize: root.row.bodyPointSize
            }

            Controls.Label {
                Layout.fillWidth: true
                visible: text.length > 0
                text: root.row.mediaDownloadError.length > 0 ? root.row.mediaDownloadError : root.detailText
                color: root.row.mediaDownloadError.length > 0
                    ? Kirigami.Theme.negativeTextColor
                    : Kirigami.Theme.disabledTextColor
                elide: Text.ElideRight
                maximumLineCount: 1
                font.pointSize: Kirigami.Theme.smallFont.pointSize
            }
        }

        Kirigami.Icon {
            // Top-aligned so it sits beside the filename and leaves the corner
            // under it free for the timestamp.
            Layout.alignment: Qt.AlignTop
            visible: !root.row.mediaDownloading
            implicitWidth: Kirigami.Units.iconSizes.small
            implicitHeight: Kirigami.Units.iconSizes.small
            source: root.hasFile ? "document-open-symbolic" : "folder-download-symbolic"
            color: Kirigami.Theme.disabledTextColor
        }
    }

    MediaDragArea {
        anchors.fill: parent
        z: -1
        localPath: root.row.mediaLocalPath
        blocked: root.row.selectionModeActive
    }

    TapHandler {
        enabled: !root.row.selectionModeActive
        exclusiveSignals: TapHandler.SingleTap | TapHandler.DoubleTap
        onSingleTapped: {
            root.activate()
            root.row.conversationFocusRequested()
        }
    }
}
