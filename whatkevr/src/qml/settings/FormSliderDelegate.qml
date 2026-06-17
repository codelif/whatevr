import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts

import org.kde.kirigami as Kirigami
import org.kde.kirigamiaddons.formcard as FormCard

// A FormCard delegate wrapping a Slider, mirroring FormSpinBoxDelegate's layout:
// a label with a live value readout on the right, the slider below, and an
// optional description. kirigami-addons ships no slider delegate, so this fills
// the gap with the same look as the rest of the form.
FormCard.AbstractFormDelegate {
    id: root

    required property string label
    property string description: ""
    property alias from: slider.from
    property alias to: slider.to
    property alias stepSize: slider.stepSize
    property alias value: slider.value
    // Appended to the value readout, e.g. "%".
    property string suffix: ""

    // Emitted while the user drags the slider; bind persistence here.
    signal moved()

    focusPolicy: Qt.NoFocus
    Accessible.description: description
    onClicked: slider.forceActiveFocus()
    background: null

    contentItem: ColumnLayout {
        spacing: Kirigami.Units.smallSpacing

        RowLayout {
            Layout.fillWidth: true
            spacing: Kirigami.Units.smallSpacing

            QQC2.Label {
                Layout.fillWidth: true
                text: root.label
                elide: Text.ElideRight
                color: root.enabled ? Kirigami.Theme.textColor : Kirigami.Theme.disabledTextColor
                wrapMode: Text.Wrap
                maximumLineCount: 2
            }

            QQC2.Label {
                text: Math.round(slider.value) + root.suffix
                color: Kirigami.Theme.disabledTextColor
            }
        }

        QQC2.Slider {
            id: slider
            Layout.fillWidth: true
            onMoved: root.moved()
        }

        QQC2.Label {
            Layout.fillWidth: true
            text: root.description
            color: Kirigami.Theme.disabledTextColor
            visible: root.description !== ""
            wrapMode: Text.Wrap
        }
    }
}
