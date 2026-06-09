import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Kirigami.ScrollablePage {
    id: root

    title: Whatevr.AppController.statusTitle
    padding: 0

    readonly property real pageContentWidth: Math.min(width - Kirigami.Units.largeSpacing * 4,
                                                      Kirigami.Units.gridUnit * 42)

    ColumnLayout {
        id: layout

        width: root.pageContentWidth
        x: Math.max(0, (parent.width - width) / 2)
        y: Math.max(Kirigami.Units.largeSpacing * 2,
                    (parent.height - implicitHeight) / 2)
        spacing: Kirigami.Units.largeSpacing * 1.5

        Kirigami.InlineMessage {
            Layout.fillWidth: true
            visible: Whatevr.AppController.bannerText.length > 0
            type: Kirigami.MessageType.Warning
            showCloseButton: false
            text: Whatevr.AppController.bannerText
        }

        Frame {
            Layout.fillWidth: true
            padding: Kirigami.Units.largeSpacing * 1.5

                background: Rectangle {
                    radius: Kirigami.Units.cornerRadius
                    color: Kirigami.Theme.alternateBackgroundColor
                    border.color: Qt.alpha(Kirigami.Theme.textColor, 0.08)
                }

                contentItem: ColumnLayout {
                    spacing: Kirigami.Units.largeSpacing

                    RowLayout {
                        Layout.fillWidth: true

                        Kirigami.Heading {
                            Layout.fillWidth: true
                            level: 1
                            text: Whatevr.AppController.statusTitle
                        }

                        StatusChip {
                            text: {
                                switch (Whatevr.AppController.connectionPhase) {
                                case "not-running":
                                    return Whatevr.I18n.i18n("Not running")
                                case "connecting":
                                    return Whatevr.I18n.i18n("Connecting")
                                case "error":
                                    return Whatevr.I18n.i18n("Error")
                                default:
                                    return Whatevr.I18n.i18n("Daemon")
                                }
                            }
                        }
                    }

                    Label {
                        Layout.fillWidth: true
                        text: Whatevr.AppController.statusText
                        wrapMode: Text.WordWrap
                        color: Kirigami.Theme.disabledTextColor
                    }

                    // How to start the daemon, shown only when it isn't running.
                    Label {
                        Layout.fillWidth: true
                        visible: !Whatevr.AppController.daemonRunning
                        text: Whatevr.AppController.daemonInstructions
                        textFormat: Text.PlainText
                        wrapMode: Text.WrapAnywhere
                        font.family: "monospace"
                        color: Kirigami.Theme.textColor
                    }

                    Label {
                        Layout.fillWidth: true
                        visible: Whatevr.AppController.detailText.length > 0
                        text: Whatevr.AppController.detailText
                        textFormat: Text.PlainText
                        wrapMode: Text.WrapAnywhere
                        color: Kirigami.Theme.disabledTextColor
                    }

                    RowLayout {
                        Layout.fillWidth: true

                        Button {
                            visible: !Whatevr.AppController.daemonRunning
                            text: Whatevr.I18n.i18n("Start whatevrd")
                            icon.name: "media-playback-start-symbolic"
                            onClicked: Whatevr.AppController.startDaemon()
                        }

                        Button {
                            text: Whatevr.AppController.primaryActionText
                            enabled: Whatevr.AppController.primaryActionEnabled
                            onClicked: Whatevr.AppController.triggerPrimaryAction()
                        }

                        Item {
                            Layout.fillWidth: true
                        }

                        BusyIndicator {
                            running: Whatevr.AppController.loading
                            visible: running
                        }
                    }
                }
        }

        Item {
            Layout.fillWidth: true
            implicitHeight: Kirigami.Units.largeSpacing * 2
        }
    }
}
