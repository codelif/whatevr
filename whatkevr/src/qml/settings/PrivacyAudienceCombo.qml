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

    // PrivacyAudience.CONTACTS_EXCEPT — "My contacts except…". Shown only for
    // display when the account already has this mode (set on the phone); it
    // can't be chosen from here, so selecting it just reverts.
    readonly property int contactsExcept: 3

    // Sync the dropdown to the stored audience. Guard against indexOfValue
    // returning -1 (value not in this combo's model) so the dropdown never
    // blanks out — keep the previous selection instead.
    function syncFromSettings() {
        const idx = combo.indexOfValue(Whatevr.AppController.privacySettings[combo.categoryKey] ?? 0)
        if (idx >= 0)
            combo.currentIndex = idx
    }

    // indexOfValue() needs the inner ComboBox model ready (see AppearancePage).
    Component.onCompleted: combo.syncFromSettings()
    onActivated: {
        // "My contacts except…" requires choosing the excluded contacts, which
        // isn't supported here — revert the selection instead of applying it.
        if (combo.currentValue === combo.contactsExcept) {
            combo.syncFromSettings()
            return
        }
        Whatevr.AppController.setPrivacyAudience(combo.categoryKey, combo.currentValue)
    }

    Connections {
        target: Whatevr.AppController
        function onPrivacySettingsChanged() {
            combo.syncFromSettings();
        }
    }
}
