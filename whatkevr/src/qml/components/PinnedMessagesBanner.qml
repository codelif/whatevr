pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Thin banner above the message timeline showing the chat's pinned messages,
// one at a time, with chevrons to step through them. Clicking the banner jumps
// to the current pinned message in the conversation.
Item {
    id: root

    // Mirrors the backing model's row count so the parent can hide the banner
    // when nothing is pinned.
    readonly property int count: Whatevr.AppController.pinnedMessagesModel.count
    property int currentIndex: 0
    // Display fields of the currently shown pin (PinnedMessagesModel::get).
    property var current: null

    signal messageActivated(string messageId)

    // Keep the banner's layout height independent of async model/text changes.
    // The parent decides whether to reserve this height while pins are loading
    // or collapse it after a confirmed no-pins result.
    implicitHeight: Math.max(Kirigami.Units.gridUnit * 2.75,
                             Kirigami.Units.iconSizes.small + Kirigami.Units.smallSpacing * 2)

    // Keep the accent at full strength when the window loses focus, instead of
    // letting the highlight dim to its inactive variant (matches ChatListDelegate).
    SystemPalette {
        id: activePalette
        colorGroup: SystemPalette.Active
    }

    QQC2.Menu {
        id: contextMenu

        QQC2.MenuItem {
            icon.name: "window-unpin-symbolic"
            text: Whatevr.I18n.i18nc("@action:inmenu unpin the pinned message", "Unpin")
            onTriggered: {
                if (root.current) {
                    Whatevr.AppController.unpinMessage(String(root.current.messageId))
                }
            }
        }
    }

    function refresh() {
        const model = Whatevr.AppController.pinnedMessagesModel
        if (model.count === 0) {
            current = null
            return
        }
        if (currentIndex >= model.count) {
            currentIndex = model.count - 1
        }
        if (currentIndex < 0) {
            currentIndex = 0
        }
        current = model.get(currentIndex)
    }

    function showNext() {
        if (count === 0) {
            return
        }
        // Newest pin first feels natural when stepping; wrap around the list.
        currentIndex = (currentIndex + 1) % count
        refresh()
    }

    onCurrentIndexChanged: refresh()
    Component.onCompleted: refresh()

    Connections {
        target: Whatevr.AppController.pinnedMessagesModel

        function onCountChanged() { root.refresh() }
        function onModelReset() { root.currentIndex = 0; root.refresh() }
        function onDataChanged() { root.refresh() }
    }

    Rectangle {
        anchors.fill: parent
        Kirigami.Theme.colorSet: Kirigami.Theme.View
        Kirigami.Theme.inherit: false
        color: Kirigami.Theme.backgroundColor

        Kirigami.Separator {
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: parent.bottom
        }

        // Accent bar on the leading edge, a stack of segments hinting at how
        // many messages are pinned (WhatsApp-style).
        Row {
            id: accent
            anchors.left: parent.left
            anchors.top: parent.top
            anchors.bottom: parent.bottom
            anchors.topMargin: Kirigami.Units.smallSpacing
            anchors.bottomMargin: Kirigami.Units.smallSpacing
            anchors.leftMargin: Kirigami.Units.smallSpacing
            spacing: 2

            Rectangle {
                width: 3
                height: parent.height
                radius: 1.5
                color: activePalette.highlight
            }
        }

        RowLayout {
            id: bannerRow

            anchors.left: accent.right
            anchors.right: parent.right
            anchors.verticalCenter: parent.verticalCenter
            anchors.leftMargin: Kirigami.Units.largeSpacing
            anchors.rightMargin: Kirigami.Units.smallSpacing
            spacing: Kirigami.Units.smallSpacing

            Kirigami.Icon {
                source: "pin-symbolic"
                Layout.alignment: Qt.AlignVCenter
                implicitWidth: Kirigami.Units.iconSizes.small
                implicitHeight: Kirigami.Units.iconSizes.small
                color: activePalette.highlight
                isMask: true
            }

            ColumnLayout {
                Layout.fillWidth: true
                spacing: 0

                QQC2.Label {
                    Layout.fillWidth: true
                    text: root.count > 1
                          ? Whatevr.I18n.i18nc("@label pinned banner header with position",
                                               "Pinned message %1 of %2", root.currentIndex + 1, root.count)
                          : Whatevr.I18n.i18nc("@label pinned banner header", "Pinned message")
                    elide: Text.ElideRight
                    font: Kirigami.Theme.smallFont
                    color: activePalette.highlight
                }

                QQC2.Label {
                    Layout.fillWidth: true
                    text: root.current
                          ? (root.current.senderName.length > 0
                             ? Whatevr.I18n.i18nc("@label pinned message: sender and preview",
                                                  "%1: %2", root.current.senderName, root.current.preview)
                             : root.current.preview)
                          : ""
                    elide: Text.ElideRight
                    maximumLineCount: 1
                    color: Kirigami.Theme.textColor
                }
            }

            QQC2.ToolButton {
                Layout.alignment: Qt.AlignVCenter
                visible: root.count > 1
                icon.name: "go-up-symbolic"
                display: QQC2.AbstractButton.IconOnly
                text: Whatevr.I18n.i18nc("@action:button step to the next pinned message", "Next pinned message")
                QQC2.ToolTip.visible: hovered
                QQC2.ToolTip.text: text
                onClicked: root.showNext()
            }
        }

        // Whole-banner tap jumps to the current pin. Sits below the chevron so
        // the button keeps its own clicks.
        MouseArea {
            anchors.left: accent.right
            anchors.top: parent.top
            anchors.bottom: parent.bottom
            anchors.right: bannerRow.right
            z: -1
            cursorShape: Qt.PointingHandCursor
            acceptedButtons: Qt.LeftButton | Qt.RightButton
            onClicked: mouse => {
                if (!root.current) {
                    return
                }
                if (mouse.button === Qt.RightButton) {
                    contextMenu.popup()
                    return
                }
                root.messageActivated(String(root.current.messageId))
            }
        }
    }
}
