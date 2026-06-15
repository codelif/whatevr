import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Behavior")

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Composing")
    }

    FormCard.FormCard {
        FormCard.FormSwitchDelegate {
            objectName: "behavior.enterToSend"
            text: Whatevr.I18n.i18nc("@option:check", "Press Enter to send")
            description: checked
                ? Whatevr.I18n.i18nc("@info", "Enter sends the message; Shift+Enter inserts a new line.")
                : Whatevr.I18n.i18nc("@info", "Enter inserts a new line; Ctrl+Enter sends the message.")
            checked: Whatevr.Settings.enterToSend
            onToggled: Whatevr.Settings.enterToSend = checked
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "behavior.persistDrafts"
            text: Whatevr.I18n.i18nc("@option:check", "Save unsent drafts")
            description: Whatevr.I18n.i18nc("@info", "Keep half-written messages when the app is closed and reopened.")
            checked: Whatevr.Settings.persistDrafts
            onToggled: Whatevr.Settings.persistDrafts = checked
        }
    }
}
