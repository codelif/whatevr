import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Window & Layout")

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "On startup")
    }

    FormCard.FormCard {
        FormCard.FormSwitchDelegate {
            objectName: "window.rememberGeometry"
            text: Whatevr.I18n.i18nc("@option:check", "Remember window size and position")
            checked: Whatevr.Settings.rememberWindowGeometry
            onToggled: Whatevr.Settings.rememberWindowGeometry = checked
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "window.rememberColumnWidth"
            text: Whatevr.I18n.i18nc("@option:check", "Remember chat list width")
            description: Whatevr.I18n.i18nc("@info", "Restore the width you set for the chat list column.")
            checked: Whatevr.Settings.rememberColumnWidth
            onToggled: Whatevr.Settings.rememberColumnWidth = checked
        }
    }
}
