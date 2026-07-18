pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Kirigami.ScrollablePage {
    id: root

    title: Whatevr.ProtocolController.statusTitle
    padding: 0

    readonly property bool wideLayout: width >= Kirigami.Units.gridUnit * 48
    readonly property real pageContentWidth: Math.min(width - Kirigami.Units.largeSpacing * 4,
                                                      Kirigami.Units.gridUnit * 64)

    // Phase → icon name + accent color, so the hero reads at a glance.
    readonly property string phaseIcon: {
        switch (Whatevr.ProtocolController.connectionPhase) {
        case "connected":
            return "network-connect"
        case "connecting":
            return "view-refresh"
        case "error":
            return "dialog-error"
        default:
            return "network-offline"
        }
    }
    readonly property color phaseColor: {
        switch (Whatevr.ProtocolController.connectionPhase) {
        case "connected":
            return Kirigami.Theme.positiveTextColor
        case "connecting":
            return Kirigami.Theme.highlightColor
        case "error":
            return Kirigami.Theme.negativeTextColor
        default:
            return Kirigami.Theme.neutralTextColor
        }
    }
    readonly property string phaseLabel: {
        switch (Whatevr.ProtocolController.connectionPhase) {
        case "connected":
            return Whatevr.I18n.i18n("Connected")
        case "connecting":
            return Whatevr.I18n.i18n("Connecting")
        case "error":
            return Whatevr.I18n.i18n("Error")
        default:
            return Whatevr.I18n.i18n("Not running")
        }
    }

    ColumnLayout {
        id: layout

        width: root.pageContentWidth
        x: Math.max(0, (parent.width - width) / 2)
        y: Math.max(Kirigami.Units.largeSpacing * 2,
                    (parent.height - implicitHeight) / 2)
        spacing: Kirigami.Units.largeSpacing * 1.5

        Kirigami.InlineMessage {
            Layout.fillWidth: true
            visible: Whatevr.ProtocolController.bannerText.length > 0
            type: Kirigami.MessageType.Warning
            showCloseButton: false
            text: Whatevr.ProtocolController.bannerText
        }

        Kirigami.InlineMessage {
            Layout.fillWidth: true
            visible: Whatevr.ProtocolController.actionError.length > 0
            type: Kirigami.MessageType.Error
            showCloseButton: false
            text: Whatevr.ProtocolController.actionError
        }

        // Hero: status block, and (when the daemon isn't running) a panel of
        // copyable start commands beside it on wide screens, stacked otherwise.
        Item {
            Layout.fillWidth: true
            implicitHeight: root.wideLayout ? heroRow.implicitHeight : heroColumn.implicitHeight

            RowLayout {
                id: heroRow

                visible: root.wideLayout
                anchors.fill: parent
                spacing: Kirigami.Units.largeSpacing * 2

                StatusPanel {}
                HowToStartPanel {
                    visible: !Whatevr.ProtocolController.daemonRunning
                }
            }

            ColumnLayout {
                id: heroColumn

                visible: !root.wideLayout
                anchors.fill: parent
                spacing: Kirigami.Units.largeSpacing * 1.5

                StatusPanel {}
                HowToStartPanel {
                    visible: !Whatevr.ProtocolController.daemonRunning
                }
            }
        }

        // Footer: subtle debug info (socket path always; daemon state only when
        // connected — see ProtocolController::detailText()).
        Label {
            Layout.fillWidth: true
            Layout.topMargin: Kirigami.Units.largeSpacing
            visible: Whatevr.ProtocolController.detailText.length > 0
            text: Whatevr.ProtocolController.detailText
            textFormat: Text.PlainText
            wrapMode: Text.WrapAnywhere
            font.pointSize: Kirigami.Theme.smallFont.pointSize
            color: Kirigami.Theme.disabledTextColor
        }

        Item {
            Layout.fillWidth: true
            implicitHeight: Kirigami.Units.largeSpacing * 2
        }
    }

    component StatusPanel: Frame {
        Layout.fillWidth: true
        Layout.preferredWidth: root.wideLayout && !Whatevr.ProtocolController.daemonRunning
                               ? root.pageContentWidth * 0.52 : -1
        padding: Kirigami.Units.largeSpacing * 2

        background: Rectangle {
            radius: Kirigami.Units.cornerRadius
            color: Kirigami.Theme.alternateBackgroundColor
            border.color: Qt.alpha(Kirigami.Theme.textColor, 0.08)
        }

        contentItem: ColumnLayout {
            spacing: Kirigami.Units.largeSpacing

            Kirigami.Icon {
                Layout.alignment: Qt.AlignHCenter
                Layout.preferredWidth: Kirigami.Units.iconSizes.huge
                Layout.preferredHeight: Kirigami.Units.iconSizes.huge
                source: root.phaseIcon
                color: root.phaseColor
            }

            Kirigami.Heading {
                Layout.fillWidth: true
                horizontalAlignment: Text.AlignHCenter
                level: 1
                text: Whatevr.ProtocolController.statusTitle
                wrapMode: Text.WordWrap
            }

            Label {
                Layout.fillWidth: true
                horizontalAlignment: Text.AlignHCenter
                text: Whatevr.ProtocolController.statusText
                wrapMode: Text.WordWrap
                color: Kirigami.Theme.disabledTextColor
            }

            StatusChip {
                Layout.alignment: Qt.AlignHCenter
                text: root.phaseLabel
                foregroundColor: root.phaseColor
                backgroundColor: Qt.alpha(root.phaseColor, 0.14)
            }

            Item {
                Layout.fillHeight: true
            }

            RowLayout {
                Layout.fillWidth: true
                Layout.topMargin: Kirigami.Units.smallSpacing
                spacing: Kirigami.Units.largeSpacing

                Item {
                    Layout.fillWidth: true
                }

                Button {
                    visible: !Whatevr.ProtocolController.daemonRunning
                    text: Whatevr.I18n.i18n("Start whatevrd")
                    icon.name: "media-playback-start-symbolic"
                    highlighted: true
                    onClicked: Whatevr.ProtocolController.startDaemon()
                }

                Button {
                    text: Whatevr.ProtocolController.primaryActionText
                    enabled: Whatevr.ProtocolController.primaryActionEnabled
                    icon.name: Whatevr.ProtocolController.primaryActionText === Whatevr.I18n.i18n("Reconnect")
                               ? "network-connect-symbolic"
                               : "view-refresh-symbolic"
                    onClicked: Whatevr.ProtocolController.triggerPrimaryAction()
                }

                BusyIndicator {
                    Layout.preferredHeight: Kirigami.Units.iconSizes.medium
                    Layout.preferredWidth: Kirigami.Units.iconSizes.medium
                    running: Whatevr.ProtocolController.loading
                    visible: running
                }

                Item {
                    Layout.fillWidth: true
                }
            }
        }
    }

    component HowToStartPanel: Frame {
        Layout.fillWidth: true
        Layout.preferredWidth: root.wideLayout ? root.pageContentWidth * 0.44 : -1
        Layout.alignment: Qt.AlignTop
        padding: Kirigami.Units.largeSpacing * 2

        background: Rectangle {
            radius: Kirigami.Units.cornerRadius
            color: Kirigami.Theme.alternateBackgroundColor
            border.color: Qt.alpha(Kirigami.Theme.textColor, 0.08)
        }

        contentItem: ColumnLayout {
            spacing: Kirigami.Units.largeSpacing

            Kirigami.Heading {
                Layout.fillWidth: true
                level: 3
                text: Whatevr.I18n.i18nc("@title", "How to start it")
            }

            Label {
                Layout.fillWidth: true
                text: Whatevr.I18n.i18nc("@info", "Start the background daemon and Whatevr connects automatically.")
                wrapMode: Text.WordWrap
                color: Kirigami.Theme.disabledTextColor
            }

            CommandBox {
                tag: Whatevr.I18n.i18nc("@label", "systemd")
                command: Whatevr.ProtocolController.daemonServiceCommand
            }

            CommandBox {
                tag: Whatevr.I18n.i18nc("@label", "direct")
                command: Whatevr.ProtocolController.daemonBinaryCommand
            }
        }
    }

    // A monospace "code box" for a single shell command, with a copy button.
    component CommandBox: Frame {
        id: commandBox

        required property string tag
        required property string command

        Layout.fillWidth: true
        padding: Kirigami.Units.smallSpacing

        background: Rectangle {
            radius: Kirigami.Units.cornerRadius
            color: Kirigami.Theme.backgroundColor
            border.color: Qt.alpha(Kirigami.Theme.textColor, 0.10)
        }

        contentItem: RowLayout {
            spacing: Kirigami.Units.smallSpacing

            Label {
                Layout.leftMargin: Kirigami.Units.smallSpacing
                text: commandBox.tag
                font.pointSize: Kirigami.Theme.smallFont.pointSize
                color: Kirigami.Theme.disabledTextColor
            }

            Label {
                Layout.fillWidth: true
                text: commandBox.command
                textFormat: Text.PlainText
                font.family: "monospace"
                wrapMode: Text.WrapAnywhere
                elide: Text.ElideNone
            }

            Button {
                icon.name: "edit-copy-symbolic"
                display: AbstractButton.IconOnly
                flat: true
                text: Whatevr.I18n.i18nc("@action:button", "Copy")
                ToolTip.visible: hovered
                ToolTip.text: text
                onClicked: Whatevr.ProtocolController.copyToClipboard(commandBox.command)
            }
        }
    }
}
