import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami

Kirigami.ScrollablePage {
    id: root

    title: AppController.statusTitle
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
            visible: AppController.bannerText.length > 0
            type: Kirigami.MessageType.Warning
            showCloseButton: false
            text: AppController.bannerText
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
                            text: AppController.statusTitle
                        }

                        StatusChip {
                            text: AppController.loading ? i18n("Connecting") : i18n("Daemon")
                        }
                    }

                    Label {
                        Layout.fillWidth: true
                        text: AppController.statusText
                        wrapMode: Text.WordWrap
                        color: Kirigami.Theme.disabledTextColor
                    }

                    Label {
                        Layout.fillWidth: true
                        visible: AppController.detailText.length > 0
                        text: AppController.detailText
                        textFormat: Text.PlainText
                        wrapMode: Text.WrapAnywhere
                        color: Kirigami.Theme.disabledTextColor
                    }

                    RowLayout {
                        Layout.fillWidth: true

                        Button {
                            text: AppController.primaryActionText
                            enabled: AppController.primaryActionEnabled
                            onClicked: AppController.triggerPrimaryAction()
                        }

                        Item {
                            Layout.fillWidth: true
                        }

                        BusyIndicator {
                            running: AppController.loading
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
