import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Notifications")

    readonly property bool notificationsOn: Whatevr.ProtocolController.appPreferences.notifications_enabled ?? true

    function syncPreferenceBindings() {
        notificationsSwitch.checked = Qt.binding(() => Whatevr.ProtocolController.appPreferences.notifications_enabled ?? true)
        previewSwitch.checked = Qt.binding(() => Whatevr.ProtocolController.appPreferences.notification_preview ?? true)
        soundSwitch.checked = Qt.binding(() => Whatevr.ProtocolController.appPreferences.notification_sound ?? false)
    }

    Connections {
        target: Whatevr.ProtocolController
        function onAppPreferencesChanged() { page.syncPreferenceBindings() }
        function onSettingsActionFailed() { page.syncPreferenceBindings() }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Desktop notifications")
    }

    FormCard.FormCard {
        FormCard.FormSwitchDelegate {
            id: notificationsSwitch
            objectName: "notifications.enabled"
            text: Whatevr.I18n.i18nc("@option:check", "Show notifications")
            description: Whatevr.I18n.i18nc("@info", "Notify me about new messages while the window is in the background.")
            checked: page.notificationsOn
            onToggled: Whatevr.ProtocolController.setAppPreference("notifications_enabled", checked)
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            id: previewSwitch
            objectName: "notifications.preview"
            text: Whatevr.I18n.i18nc("@option:check", "Show message preview")
            description: Whatevr.I18n.i18nc("@info", "Include the message text in the notification. Turn off to show only the sender.")
            enabled: page.notificationsOn
            checked: Whatevr.ProtocolController.appPreferences.notification_preview ?? true
            onToggled: Whatevr.ProtocolController.setAppPreference("notification_preview", checked)
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            id: soundSwitch
            objectName: "notifications.sound"
            text: Whatevr.I18n.i18nc("@option:check", "Play a sound")
            enabled: page.notificationsOn
            checked: Whatevr.ProtocolController.appPreferences.notification_sound ?? false
            onToggled: Whatevr.ProtocolController.setAppPreference("notification_sound", checked)
        }
    }
}
