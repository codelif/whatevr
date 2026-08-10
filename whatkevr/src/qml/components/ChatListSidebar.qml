import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami

import Whatevr as Whatevr

// Slim vertical rail on the left of the chat list. Top group switches the chat
// filter (Home / DMs / Groups) and opens the starred-messages view; the bottom
// pins Settings and the logged-in user's avatar (→ Settings → Account).
Item {
    id: root

    // 0 = Home (all), 1 = DMs, 2 = Groups. Two-way bound to the pane.
    property int activeFilter: 0

    readonly property string userName: Whatevr.ProtocolController.currentUserName

    function initialsFor(name) {
        const parts = (name || "").trim().split(/\s+/).filter(p => p.length > 0)
        if (parts.length === 0)
            return "?"
        let initials = parts[0].charAt(0)
        if (parts.length > 1)
            initials += parts[parts.length - 1].charAt(0)
        return initials.toUpperCase()
    }

    // Shared icon size for the rail buttons — bigger than the default small
    // toolbutton glyph so they don't look lost in the bar.
    readonly property int railIconSize: Kirigami.Units.iconSizes.smallMedium

    implicitWidth: Kirigami.Units.gridUnit * 2.5
    Layout.preferredWidth: implicitWidth
    Layout.fillHeight: true

    Kirigami.Theme.inherit: false
    Kirigami.Theme.colorSet: Kirigami.Theme.Window

    Rectangle {
        anchors.fill: parent
        color: Kirigami.Theme.backgroundColor

        // Subtle separator from the chat list on the right.
        Rectangle {
            anchors.top: parent.top
            anchors.bottom: parent.bottom
            anchors.right: parent.right
            width: 1
            color: Qt.alpha(Kirigami.Theme.textColor, 0.12)
        }
    }

    ColumnLayout {
        anchors.fill: parent
        anchors.topMargin: Kirigami.Units.smallSpacing
        anchors.bottomMargin: Kirigami.Units.largeSpacing
        spacing: Kirigami.Units.smallSpacing

        QQC2.ToolButton {
            Layout.alignment: Qt.AlignHCenter
            icon.name: "go-home-symbolic"
            display: QQC2.AbstractButton.IconOnly
            icon.width: root.railIconSize
            icon.height: root.railIconSize
            text: Whatevr.I18n.i18nc("@action:button chat filter", "Home")
            checkable: true
            checked: root.activeFilter === 0
            onClicked: root.activeFilter = 0

            QQC2.ToolTip.visible: hovered
            QQC2.ToolTip.text: text
            QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
        }

        QQC2.ToolButton {
            Layout.alignment: Qt.AlignHCenter
            icon.name: "user-symbolic"
            display: QQC2.AbstractButton.IconOnly
            icon.width: root.railIconSize
            icon.height: root.railIconSize
            text: Whatevr.I18n.i18nc("@action:button chat filter", "Direct messages")
            checkable: true
            checked: root.activeFilter === 1
            onClicked: root.activeFilter = 1

            QQC2.ToolTip.visible: hovered
            QQC2.ToolTip.text: text
            QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
        }

        QQC2.ToolButton {
            Layout.alignment: Qt.AlignHCenter
            icon.name: "system-users-symbolic"
            display: QQC2.AbstractButton.IconOnly
            icon.width: root.railIconSize
            icon.height: root.railIconSize
            text: Whatevr.I18n.i18nc("@action:button chat filter", "Groups")
            checkable: true
            checked: root.activeFilter === 2
            onClicked: root.activeFilter = 2

            QQC2.ToolTip.visible: hovered
            QQC2.ToolTip.text: text
            QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
        }

        QQC2.ToolButton {
            Layout.alignment: Qt.AlignHCenter
            icon.name: "starred-symbolic"
            display: QQC2.AbstractButton.IconOnly
            icon.width: root.railIconSize
            icon.height: root.railIconSize
            text: Whatevr.I18n.i18nc("@action:button open the starred-messages view", "Starred messages")
            onClicked: {
                // The page owns its own `starred` subscription for as long as
                // it is on screen; pushing it is all this has to do.
                applicationWindow().pageStack.layers.push(Qt.resolvedUrl("StarredMessagesPage.qml"), {
                    chatId: "",
                    headerTitle: Whatevr.I18n.i18nc("@title", "Starred messages")
                })
            }

            QQC2.ToolTip.visible: hovered
            QQC2.ToolTip.text: text
            QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
        }

        Item {
            Layout.fillHeight: true
        }

        QQC2.ToolButton {
            Layout.alignment: Qt.AlignHCenter
            icon.name: "settings-configure-symbolic"
            display: QQC2.AbstractButton.IconOnly
            icon.width: root.railIconSize
            icon.height: root.railIconSize
            text: Whatevr.I18n.i18nc("@action:button", "Settings")
            onClicked: applicationWindow().openSettings()

            QQC2.ToolTip.visible: hovered
            QQC2.ToolTip.text: text
            QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
        }

        QQC2.AbstractButton {
            Layout.alignment: Qt.AlignHCenter
            Layout.topMargin: Kirigami.Units.smallSpacing
            implicitWidth: Kirigami.Units.gridUnit * 1.9
            implicitHeight: implicitWidth
            hoverEnabled: true

            Accessible.role: Accessible.Button
            Accessible.name: Whatevr.I18n.i18nc("@action:button", "Open account settings")

            onClicked: applicationWindow().openSettings("account")

            contentItem: AvatarImage {
                avatarLocalPath: Whatevr.ProtocolController.currentUserAvatarPath
                initials: root.initialsFor(root.userName)
            }

            QQC2.ToolTip.visible: hovered
            QQC2.ToolTip.text: root.userName.length > 0
                ? root.userName
                : Whatevr.I18n.i18nc("@info placeholder for own profile name", "You")
            QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
        }
    }
}
