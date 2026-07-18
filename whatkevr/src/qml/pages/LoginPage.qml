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

    // Padding consumed by the QrPanel frame and its inner quiet-zone margin, so
    // the QR's width budget reflects what's actually free inside the card.
    readonly property real qrPanelChrome: Kirigami.Units.largeSpacing * 1.5 * 2
                                          + Kirigami.Units.largeSpacing * 3
    readonly property real qrWidthBudget: (root.wideLayout ? root.pageContentWidth * 0.40
                                                           : root.pageContentWidth)
                                          - root.qrPanelChrome
    // Vertical room for the code: viewport height minus the QR panel's own
    // heading/expiry chrome and page margins. The stacked instructions panel
    // below can scroll, so we don't reserve its full height here — that kept the
    // QR tiny on short windows.
    readonly property real qrHeightReserve: Kirigami.Units.gridUnit * 12
    readonly property real qrHeightBudget: root.height - root.qrHeightReserve
    // Square, fully on-screen at every window size; floor fits the 420x680
    // minimum window (Main.qml), cap keeps it sane on large screens.
    readonly property real qrSide: Math.max(Kirigami.Units.gridUnit * 8,
                                            Math.min(Kirigami.Units.gridUnit * 26,
                                                     root.qrWidthBudget,
                                                     root.qrHeightBudget))

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

        Item {
            Layout.fillWidth: true
            implicitHeight: root.wideLayout ? heroRow.implicitHeight : heroColumn.implicitHeight

            RowLayout {
                id: heroRow

                visible: root.wideLayout
                anchors.fill: parent
                spacing: Kirigami.Units.largeSpacing * 2

                QrPanel {}
                LeftPanel {}
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
        Layout.preferredWidth: root.wideLayout ? root.pageContentWidth * 0.56 : -1
        Layout.alignment: Qt.AlignTop
        padding: Kirigami.Units.largeSpacing * 2

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
                        Layout.fillWidth: true
                        level: 1
                        text: Whatevr.ProtocolController.statusTitle
                        wrapMode: Text.WordWrap
                    }

                    Label {
                        Layout.fillWidth: true
                        text: Whatevr.ProtocolController.statusText
                        wrapMode: Text.WordWrap
                        color: Kirigami.Theme.disabledTextColor
                    }
                }

                StatusChip {
                    Layout.alignment: Qt.AlignTop
                    text: Whatevr.I18n.i18n("Sign in")
                    foregroundColor: Kirigami.Theme.highlightColor
                    backgroundColor: Qt.alpha(Kirigami.Theme.highlightColor, 0.14)
                }
            }

            Frame {
                Layout.fillWidth: true
                Layout.topMargin: Kirigami.Units.smallSpacing
                padding: Kirigami.Units.largeSpacing * 1.5

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
                            Whatevr.I18n.i18nc("@info", "Tap Linked Devices, then Link a device"),
                            Whatevr.I18n.i18nc("@info", "Point your phone at this QR code to pair the daemon session")
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
                                Layout.alignment: Qt.AlignTop
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
                                Layout.alignment: Qt.AlignVCenter
                                text: stepDelegate.modelData
                                wrapMode: Text.WordWrap
                            }
                        }
                    }
                }
            }

            Label {
                Layout.fillWidth: true
                visible: Whatevr.ProtocolController.detailText.length > 0
                text: Whatevr.ProtocolController.detailText
                wrapMode: Text.WrapAnywhere
                textFormat: Text.PlainText
                font.pointSize: Kirigami.Theme.smallFont.pointSize
                color: Kirigami.Theme.disabledTextColor
            }

            Item {
                Layout.fillHeight: true
            }

            RowLayout {
                Layout.fillWidth: true
                Layout.topMargin: Kirigami.Units.smallSpacing

                Button {
                    text: Whatevr.ProtocolController.primaryActionText
                    enabled: Whatevr.ProtocolController.primaryActionEnabled
                    icon.name: Whatevr.ProtocolController.primaryActionText === Whatevr.I18n.i18n("Reconnect")
                               ? "network-connect-symbolic"
                               : "view-refresh-symbolic"
                    onClicked: Whatevr.ProtocolController.triggerPrimaryAction()
                }

                Item {
                    Layout.fillWidth: true
                }
            }
        }
    }

    component QrPanel: Frame {
        Layout.alignment: Qt.AlignHCenter | Qt.AlignTop
        Layout.preferredWidth: root.wideLayout ? root.pageContentWidth * 0.40 : -1
        Layout.fillWidth: !root.wideLayout
        padding: Kirigami.Units.largeSpacing * 1.5

        background: Rectangle {
            radius: Kirigami.Units.cornerRadius
            gradient: Gradient {
                GradientStop { position: 0.0; color: Kirigami.Theme.backgroundColor }
                GradientStop { position: 1.0; color: Qt.alpha(Kirigami.Theme.highlightColor, 0.05) }
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

            // White card sized to qrSide. Prison paints the matrix at integer
            // pixels-per-module and centers it, so letting it size to the card
            // makes the code jump in module-sized steps and the white margin
            // vary. Instead we render the barcode at an integer multiple of its
            // natural module resolution (gap-free, crisp) and apply a continuous
            // scale transform to a fixed fraction of the card — so the code
            // scales smoothly and the margin ratio is constant at every size.
            Rectangle {
                Layout.alignment: Qt.AlignHCenter
                Layout.preferredWidth: root.qrSide
                Layout.preferredHeight: root.qrSide
                radius: Kirigami.Units.cornerRadius
                color: "white"
                border.color: Qt.alpha(Kirigami.Theme.textColor, 0.10)

                Prison.Barcode {
                    id: barcode

                    // Displayed code = 92% of the card → a constant 4%-per-side
                    // white margin regardless of size.
                    readonly property real targetPx: root.qrSide * 0.92
                    readonly property real renderSize: implicitWidth
                        * Math.max(1, Math.ceil(targetPx / Math.max(1, implicitWidth)))

                    anchors.centerIn: parent
                    visible: Whatevr.ProtocolController.qrAvailable
                    width: renderSize
                    height: renderSize
                    transformOrigin: Item.Center
                    scale: targetPx / renderSize
                    smooth: false
                    barcodeType: Prison.Barcode.QRCode
                    content: Whatevr.ProtocolController.qrCode
                    foregroundColor: "black"
                    backgroundColor: "white"
                }

                BusyIndicator {
                    anchors.centerIn: parent
                    running: !Whatevr.ProtocolController.qrAvailable
                    visible: running
                }
            }

            StatusChip {
                Layout.alignment: Qt.AlignHCenter
                visible: Whatevr.ProtocolController.qrAvailable && Whatevr.ProtocolController.qrExpiryText.length > 0
                text: Whatevr.ProtocolController.qrExpiryText
                foregroundColor: Kirigami.Theme.neutralTextColor
                backgroundColor: Qt.alpha(Kirigami.Theme.neutralTextColor, 0.14)
            }

            Label {
                Layout.alignment: Qt.AlignHCenter
                visible: !Whatevr.ProtocolController.qrAvailable
                text: Whatevr.I18n.i18nc("@info", "Waiting for a fresh QR code")
                color: Kirigami.Theme.disabledTextColor
            }
        }
    }
}
