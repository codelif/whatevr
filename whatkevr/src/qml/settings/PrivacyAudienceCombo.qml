import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

// A privacy-category dropdown bound to AppController.privacySettings[categoryKey]
// (a PrivacyAudience int). audienceModel is a list of {text, value}.
FormCard.FormComboBoxDelegate {
    id: combo

    property string categoryKey: ""
    property var audienceModel: []

    textRole: "text"
    valueRole: "value"
    model: audienceModel

    // indexOfValue() needs the inner ComboBox model ready (see AppearancePage).
    Component.onCompleted: combo.currentIndex = combo.indexOfValue(Whatevr.AppController.privacySettings[combo.categoryKey] ?? 0)
    onActivated: Whatevr.AppController.setPrivacyAudience(combo.categoryKey, combo.currentValue)

    Connections {
        target: Whatevr.AppController
        function onPrivacySettingsChanged() {
            combo.currentIndex = combo.indexOfValue(Whatevr.AppController.privacySettings[combo.categoryKey] ?? 0);
        }
    }
}
