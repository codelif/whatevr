import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami

Frame {
    id: root

    visible: AppController.historySyncVisible
    Layout.fillWidth: true
    padding: Kirigami.Units.largeSpacing

    background: Rectangle {
        radius: Kirigami.Units.largeSpacing
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
                text: AppController.historySyncTitle
                font.weight: Font.DemiBold
                elide: Text.ElideRight
            }

            Label {
                text: i18nc("@info", "%1%", AppController.historySyncPercent)
                color: Kirigami.Theme.highlightColor
                font.weight: Font.DemiBold
            }
        }

        ProgressBar {
            Layout.fillWidth: true
            from: 0
            to: 100
            value: AppController.historySyncPercent
        }

        Label {
            Layout.fillWidth: true
            text: AppController.historySyncDetail
            color: Kirigami.Theme.disabledTextColor
            elide: Text.ElideRight
        }
    }
}
