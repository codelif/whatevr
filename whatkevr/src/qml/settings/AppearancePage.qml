import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Appearance")

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Theme")
    }

    FormCard.FormCard {
        FormCard.FormComboBoxDelegate {
            id: schemeCombo

            objectName: "appearance.colorScheme"
            text: Whatevr.I18n.i18nc("@label:listbox", "Color scheme")
            description: Whatevr.I18n.i18nc("@info", "Override the application colors or follow the system theme.")
            model: Whatevr.Settings.availableColorSchemes()
            textRole: "name"
            valueRole: "id"
            currentIndex: indexOfValue(Whatevr.Settings.colorScheme)
            onActivated: index => Whatevr.Settings.colorScheme = currentValue
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "appearance.showAvatars"
            text: Whatevr.I18n.i18nc("@option:check", "Show avatars in the chat list")
            checked: Whatevr.Settings.showAvatars
            onToggled: Whatevr.Settings.showAvatars = checked
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "appearance.compactMode"
            text: Whatevr.I18n.i18nc("@option:check", "Compact mode")
            description: Whatevr.I18n.i18nc("@info", "Tighter spacing in lists and conversations.")
            checked: Whatevr.Settings.compactMode
            onToggled: Whatevr.Settings.compactMode = checked
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Text")
    }

    FormCard.FormCard {
        FormCard.FormSpinBoxDelegate {
            objectName: "appearance.messageFontSize"
            label: Whatevr.I18n.i18nc("@label:spinbox", "Message font size")
            description: Whatevr.I18n.i18nc("@info", "Point size for message text. 0 follows the system font.")
            from: 0
            to: 32
            value: Whatevr.Settings.messageFontSize
            onValueChanged: Whatevr.Settings.messageFontSize = value
        }
    }
}
