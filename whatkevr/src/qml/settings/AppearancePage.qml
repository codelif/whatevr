import QtQuick
import org.kde.kirigami as Kirigami
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Appearance")

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Theme")
    }

    FormCard.FormCard {
        PreviewSelector {
            objectName: "appearance.themeMode"
            previewKind: "theme"
            currentValue: Whatevr.Settings.themeMode
            onActivated: value => Whatevr.Settings.themeMode = value
            options: [
                { value: 0, label: Whatevr.I18n.i18nc("@item theme", "System"),
                  base: Kirigami.Theme.backgroundColor, text: Kirigami.Theme.textColor, accent: Kirigami.Theme.highlightColor },
                { value: 1, label: Whatevr.I18n.i18nc("@item theme", "Light"),
                  base: "#fafafa", text: "#232629", accent: "#3daee9" },
                { value: 2, label: Whatevr.I18n.i18nc("@item theme", "Dark"),
                  base: "#232629", text: "#fcfcfc", accent: "#3daee9" }
            ]
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Density")
    }

    FormCard.FormCard {
        PreviewSelector {
            objectName: "appearance.density"
            previewKind: "density"
            currentValue: Whatevr.Settings.density
            onActivated: value => Whatevr.Settings.density = value
            options: [
                { value: 0, label: Whatevr.I18n.i18nc("@item density", "Compact"), gap: 3, avatar: 10 },
                { value: 1, label: Whatevr.I18n.i18nc("@item density", "Standard"), gap: 6, avatar: 14 },
                { value: 2, label: Whatevr.I18n.i18nc("@item density", "Comfortable"), gap: 10, avatar: 18 }
            ]
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Message text size")
    }

    FormCard.FormCard {
        PreviewSelector {
            objectName: "appearance.messageFontSize"
            previewKind: "font"
            currentValue: Whatevr.Settings.messageFontSize
            onActivated: value => Whatevr.Settings.messageFontSize = value
            options: [
                { value: 12, label: Whatevr.I18n.i18nc("@item font size", "Small"), sample: "Aa", pixelSize: 12 },
                { value: 0, label: Whatevr.I18n.i18nc("@item font size", "Default"), sample: "Aa", pixelSize: 15 },
                { value: 16, label: Whatevr.I18n.i18nc("@item font size", "Large"), sample: "Aa", pixelSize: 18 },
                { value: 20, label: Whatevr.I18n.i18nc("@item font size", "Extra large"), sample: "Aa", pixelSize: 22 }
            ]
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Chat list")
    }

    FormCard.FormCard {
        FormCard.FormSwitchDelegate {
            objectName: "appearance.showAvatars"
            text: Whatevr.I18n.i18nc("@option:check", "Show avatars in the chat list")
            checked: Whatevr.Settings.showAvatars
            onToggled: Whatevr.Settings.showAvatars = checked
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Advanced")
    }

    FormCard.FormCard {
        FormCard.FormComboBoxDelegate {
            id: schemeCombo

            objectName: "appearance.colorScheme"
            text: Whatevr.I18n.i18nc("@label:listbox", "Color scheme")
            description: Whatevr.I18n.i18nc("@info", "Pick a specific color scheme instead of just light or dark.")
            model: Whatevr.Settings.availableColorSchemes()
            textRole: "name"
            valueRole: "id"

            // indexOfValue() hits the inner ComboBox, whose model alias may not be
            // populated when a plain `currentIndex:` binding first evaluates -> it would
            // return -1 and never recover (blank text + a mislaid-out popup). Assign once
            // the component (and model) is ready.
            Component.onCompleted: currentIndex = indexOfValue(Whatevr.Settings.colorScheme)

            onActivated: Whatevr.Settings.colorScheme = currentValue

            // Re-sync if the scheme changes from elsewhere (e.g. the theme tiles above).
            Connections {
                target: Whatevr.Settings
                function onColorSchemeChanged() {
                    schemeCombo.currentIndex = schemeCombo.indexOfValue(Whatevr.Settings.colorScheme);
                }
            }
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSpinBoxDelegate {
            objectName: "appearance.messageFontSizeExact"
            label: Whatevr.I18n.i18nc("@label:spinbox", "Exact message font size")
            description: Whatevr.I18n.i18nc("@info", "Point size for message text. 0 follows the system font.")
            from: 0
            to: 32
            value: Whatevr.Settings.messageFontSize
            onValueChanged: Whatevr.Settings.messageFontSize = value
        }
    }
}
