pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import org.kde.prison as Prison
import Whatevr as Whatevr

Kirigami.ScrollablePage {
    id: root

    title: Whatevr.I18n.i18nc("@title", "Sign In")
    padding: 0

    readonly property bool wideLayout: width >= Kirigami.Units.gridUnit * 52
    readonly property real pageContentWidth: Math.min(width - Kirigami.Units.largeSpacing * 4,
                                                      Kirigami.Units.gridUnit * 76)
    readonly property real qrSide: Math.max(Kirigami.Units.gridUnit * 12,
                                            Math.min(wideLayout ? Kirigami.Units.gridUnit * 20
                                                                : width - Kirigami.Units.largeSpacing * 8,
                                                     Kirigami.Units.gridUnit * 20))

    ColumnLayout {
        id: layout

        width: root.pageContentWidth
        x: Math.max(0, (parent.width - width) / 2)
        y: Kirigami.Units.largeSpacing * 2
        spacing: Kirigami.Units.largeSpacing * 1.5

        Item {
            Layout.fillWidth: true
            implicitHeight: 0
        }

        Kirigami.InlineMessage {
            Layout.fillWidth: true
            visible: Whatevr.AppController.bannerText.length > 0
            type: Kirigami.MessageType.Warning
            showCloseButton: false
            text: Whatevr.AppController.bannerText
        }

        Item {
            Layout.fillWidth: true
            implicitHeight: root.wideLayout ? heroRow.implicitHeight : heroColumn.implicitHeight

            RowLayout {
                id: heroRow

                visible: root.wideLayout
                anchors.fill: parent
                spacing: Kirigami.Units.largeSpacing * 2

                LeftPanel {}
                QrPanel {}
            }

            ColumnLayout {
                id: heroColumn

                visible: !root.wideLayout
                anchors.fill: parent
                spacing: Kirigami.Units.largeSpacing * 1.5

                QrPanel {}
                LeftPanel {}
            }
        }

        Item {
            Layout.fillWidth: true
            implicitHeight: Kirigami.Units.largeSpacing * 2
        }
    }

    component LeftPanel: Frame {
        Layout.fillWidth: true
        Layout.preferredWidth: root.wideLayout ? root.pageContentWidth * 0.46 : -1
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
                spacing: Kirigami.Units.largeSpacing

                ColumnLayout {
                    Layout.fillWidth: true
                    spacing: Kirigami.Units.smallSpacing

                    Kirigami.Heading {
                        level: 1
                        text: Whatevr.I18n.i18nc("@title", "Whatevr")
                    }

                    Label {
                        Layout.fillWidth: true
                        text: Whatevr.I18n.i18nc("@info", "Sign in with WhatsApp to bring your daemon-backed session into the KDE-native interface.")
                        wrapMode: Text.WordWrap
                        color: Kirigami.Theme.disabledTextColor
                    }
                }

                StatusChip {
                    text: Whatevr.AppController.statusTitle
                    foregroundColor: Kirigami.Theme.positiveTextColor
                    backgroundColor: Qt.alpha(Kirigami.Theme.positiveTextColor, 0.14)
                }
            }

            Label {
                Layout.fillWidth: true
                text: Whatevr.AppController.statusText
                wrapMode: Text.WordWrap
            }

            Frame {
                Layout.fillWidth: true
                padding: Kirigami.Units.largeSpacing

                background: Rectangle {
                    radius: Kirigami.Units.cornerRadius
                    color: Kirigami.Theme.backgroundColor
                    border.color: Qt.alpha(Kirigami.Theme.textColor, 0.06)
                }

                contentItem: ColumnLayout {
                    spacing: Kirigami.Units.largeSpacing

                    Repeater {
                        model: [
                            Whatevr.I18n.i18nc("@info", "Open WhatsApp on your phone"),
                            Whatevr.I18n.i18nc("@info", "Choose Linked Devices"),
                            Whatevr.I18n.i18nc("@info", "Scan this QR code to pair the daemon session")
                        ]

                        delegate: RowLayout {
                            id: stepDelegate

                            required property int index
                            required property string modelData

                            Layout.fillWidth: true
                            spacing: Kirigami.Units.largeSpacing

                            Rectangle {
                                Layout.preferredWidth: Kirigami.Units.gridUnit * 1.6
                                Layout.preferredHeight: Layout.preferredWidth
                                radius: width / 2
                                color: Qt.alpha(Kirigami.Theme.highlightColor, 0.16)

                                Label {
                                    anchors.centerIn: parent
                                    text: String(stepDelegate.index + 1)
                                    color: Kirigami.Theme.highlightColor
                                    font.weight: Font.DemiBold
                                }
                            }

                            Label {
                                Layout.fillWidth: true
                                text: stepDelegate.modelData
                                wrapMode: Text.WordWrap
                            }
                        }
                    }
                }
            }

            Label {
                Layout.fillWidth: true
                visible: Whatevr.AppController.detailText.length > 0
                text: Whatevr.AppController.detailText
                wrapMode: Text.WordWrap
                textFormat: Text.PlainText
                color: Kirigami.Theme.disabledTextColor
            }

            Item {
                Layout.fillHeight: true
            }

            RowLayout {
                Layout.fillWidth: true

                Button {
                    text: Whatevr.AppController.primaryActionText
                    enabled: Whatevr.AppController.primaryActionEnabled
                    icon.name: Whatevr.AppController.primaryActionText === Whatevr.I18n.i18n("Reconnect")
                               ? "network-connect-symbolic"
                               : "view-refresh-symbolic"
                    onClicked: Whatevr.AppController.triggerPrimaryAction()
                }

                Item {
                    Layout.fillWidth: true
                }

                Label {
                    visible: Whatevr.AppController.qrAvailable && Whatevr.AppController.qrExpiryText.length > 0
                    text: Whatevr.AppController.qrExpiryText
                    color: Kirigami.Theme.disabledTextColor
                }
            }
        }
    }

    component QrPanel: Frame {
        Layout.alignment: Qt.AlignHCenter
        Layout.preferredWidth: root.wideLayout ? root.pageContentWidth * 0.40 : -1
        Layout.fillWidth: !root.wideLayout
        padding: Kirigami.Units.largeSpacing * 1.5

        background: Rectangle {
            radius: Kirigami.Units.cornerRadius
            gradient: Gradient {
                GradientStop { position: 0.0; color: Kirigami.Theme.backgroundColor }
                GradientStop { position: 1.0; color: Qt.alpha(Kirigami.Theme.highlightColor, 0.04) }
            }
            border.color: Qt.alpha(Kirigami.Theme.textColor, 0.08)
        }

        contentItem: ColumnLayout {
            spacing: Kirigami.Units.largeSpacing

            Kirigami.Heading {
                Layout.alignment: Qt.AlignHCenter
                level: 3
                text: Whatevr.I18n.i18nc("@title", "Pair your device")
            }

            Item {
                Layout.alignment: Qt.AlignHCenter
                implicitWidth: root.qrSide + Kirigami.Units.largeSpacing * 3
                implicitHeight: implicitWidth

                Rectangle {
                    anchors.fill: parent
                    radius: Kirigami.Units.cornerRadius
                    color: "white"
                    border.color: Qt.alpha(Kirigami.Theme.textColor, 0.10)
                }

                Prison.Barcode {
                    anchors.centerIn: parent
                    visible: Whatevr.AppController.qrAvailable
                    width: root.qrSide
                    height: root.qrSide
                    barcodeType: Prison.Barcode.QRCode
                    content: Whatevr.AppController.qrCode
                    foregroundColor: "black"
                    backgroundColor: "white"
                }

                BusyIndicator {
                    anchors.centerIn: parent
                    running: !Whatevr.AppController.qrAvailable
                    visible: running
                }
            }

            Label {
                Layout.alignment: Qt.AlignHCenter
                visible: !Whatevr.AppController.qrAvailable
                text: Whatevr.I18n.i18nc("@info", "Waiting for a fresh QR code")
                color: Kirigami.Theme.disabledTextColor
            }

            Label {
                Layout.alignment: Qt.AlignHCenter
                visible: Whatevr.AppController.qrAvailable && Whatevr.AppController.qrExpiryText.length > 0
                text: Whatevr.AppController.qrExpiryText
                color: Kirigami.Theme.disabledTextColor
            }
        }
    }
}
