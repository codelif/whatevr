import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami

Item {
    id: root

    readonly property real contentWidth: Math.min(width - Kirigami.Units.largeSpacing * 4,
                                                  Kirigami.Units.gridUnit * 42)

    Flickable {
        id: statusFlickable

        anchors.fill: parent
        contentWidth: width
        contentHeight: layout.implicitHeight + Kirigami.Units.largeSpacing * 4
        clip: true
        boundsBehavior: Flickable.DragAndOvershootBounds
        boundsMovement: Flickable.FollowBoundsBehavior
        flickableDirection: Flickable.VerticalFlick
        flickDeceleration: 1400
        maximumFlickVelocity: 12000
        ScrollBar.vertical: ScrollBar {
            policy: ScrollBar.AlwaysOn
        }

        Kirigami.WheelHandler {
            target: statusFlickable
            filterMouseEvents: true
            keyNavigationEnabled: true
        }

        ColumnLayout {
            id: layout

            width: root.contentWidth
            anchors.horizontalCenter: parent.horizontalCenter
            anchors.verticalCenter: parent.height >= implicitHeight ? parent.verticalCenter : undefined
            anchors.top: parent.height < implicitHeight ? parent.top : undefined
            anchors.topMargin: Kirigami.Units.largeSpacing * 2
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
                    radius: Kirigami.Units.largeSpacing * 1.2
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
        }
    }
}
