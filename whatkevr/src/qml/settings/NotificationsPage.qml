import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Notifications")

    Component.onCompleted: Whatevr.AppController.refreshAppPreferences()

    readonly property bool notificationsOn: Whatevr.AppController.appPreferences.notificationsEnabled ?? true

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Desktop notifications")
    }

    FormCard.FormCard {
        FormCard.FormSwitchDelegate {
            objectName: "notifications.enabled"
            text: Whatevr.I18n.i18nc("@option:check", "Show notifications")
            description: Whatevr.I18n.i18nc("@info", "Notify me about new messages while the window is in the background.")
            checked: page.notificationsOn
            onToggled: Whatevr.AppController.setAppPreference("notificationsEnabled", checked)
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "notifications.preview"
            text: Whatevr.I18n.i18nc("@option:check", "Show message preview")
            description: Whatevr.I18n.i18nc("@info", "Include the message text in the notification. Turn off to show only the sender.")
            enabled: page.notificationsOn
            checked: Whatevr.AppController.appPreferences.notificationPreview ?? true
            onToggled: Whatevr.AppController.setAppPreference("notificationPreview", checked)
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "notifications.sound"
            text: Whatevr.I18n.i18nc("@option:check", "Play a sound")
            enabled: page.notificationsOn
            checked: Whatevr.AppController.appPreferences.notificationSound ?? false
            onToggled: Whatevr.AppController.setAppPreference("notificationSound", checked)
        }
    }
}
