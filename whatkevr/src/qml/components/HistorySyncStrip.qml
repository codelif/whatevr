import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Frame {
    id: root

    visible: Whatevr.ProtocolController.historySyncVisible
    Layout.fillWidth: true
    padding: Kirigami.Units.largeSpacing

    background: Rectangle {
        radius: Kirigami.Units.cornerRadius
        color: Qt.alpha(Kirigami.Theme.highlightColor, 0.08)
        border.color: Qt.alpha(Kirigami.Theme.highlightColor, 0.18)
    }

    contentItem: ColumnLayout {
        spacing: Kirigami.Units.smallSpacing

        RowLayout {
            Layout.fillWidth: true
            spacing: Kirigami.Units.largeSpacing

            Label {
                Layout.fillWidth: true
                text: Whatevr.ProtocolController.historySyncTitle
                font.weight: Font.DemiBold
                elide: Text.ElideRight
            }

            Label {
                text: Whatevr.I18n.i18nc("@info", "%1%", Whatevr.ProtocolController.historySyncPercent)
                color: Kirigami.Theme.highlightColor
                font.weight: Font.DemiBold
            }
        }

        ProgressBar {
            Layout.fillWidth: true
            from: 0
            to: 100
            value: Whatevr.ProtocolController.historySyncPercent
        }

        Label {
            Layout.fillWidth: true
            text: Whatevr.ProtocolController.historySyncDetail
            color: Kirigami.Theme.disabledTextColor
            elide: Text.ElideRight
        }
    }
}
