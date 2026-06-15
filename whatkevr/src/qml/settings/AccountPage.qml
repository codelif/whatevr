import QtQuick
import org.kde.kirigami as Kirigami
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Account")

    readonly property bool connected: Whatevr.AppController.shellVisible

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "WhatsApp account")
    }

    FormCard.FormCard {
        FormCard.FormTextDelegate {
            objectName: "account.status"
            text: Whatevr.I18n.i18nc("@label", "Status")
            description: page.connected
                ? Whatevr.I18n.i18nc("@info account is connected", "Connected")
                : (Whatevr.AppController.loginRequired
                    ? Whatevr.I18n.i18nc("@info account needs sign-in", "Signed out")
                    : Whatevr.I18n.i18nc("@info account not connected", "Not connected"))

            leading: Kirigami.Icon {
                source: page.connected ? "network-connect-symbolic" : "network-offline-symbolic"
                implicitWidth: Kirigami.Units.iconSizes.medium
                implicitHeight: Kirigami.Units.iconSizes.medium
            }
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormButtonDelegate {
            objectName: "account.logout"
            text: Whatevr.I18n.i18nc("@action:button", "Log out")
            description: Whatevr.I18n.i18nc("@info", "Disconnect this device from your WhatsApp account.")
            icon.name: "system-log-out-symbolic"
            enabled: page.connected
            onClicked: Whatevr.AppController.logout()
        }
    }
}
