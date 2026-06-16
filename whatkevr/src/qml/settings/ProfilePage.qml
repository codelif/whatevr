import QtQuick
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Profile & Account")

    readonly property bool connected: Whatevr.AppController.shellVisible

    Component.onCompleted: Whatevr.AppController.fetchSelfProfile()

    // Centered avatar + name header.
    FormCard.FormCard {
        Layout.topMargin: Kirigami.Units.largeSpacing

        FormCard.AbstractFormDelegate {
            background: null
            contentItem: ColumnLayout {
                spacing: Kirigami.Units.smallSpacing

                Whatevr.AvatarImage {
                    Layout.alignment: Qt.AlignHCenter
                    Layout.preferredWidth: Kirigami.Units.gridUnit * 5
                    Layout.preferredHeight: Kirigami.Units.gridUnit * 5
                    avatarLocalPath: Whatevr.AppController.currentUserAvatarPath
                    initials: {
                        const name = Whatevr.AppController.currentUserName.trim();
                        return name.length > 0 ? name.charAt(0).toUpperCase() : "?";
                    }
                }

                Kirigami.Heading {
                    Layout.alignment: Qt.AlignHCenter
                    level: 3
                    text: Whatevr.AppController.currentUserName
                }

                Kirigami.SelectableLabel {
                    Layout.alignment: Qt.AlignHCenter
                    visible: text.length > 0
                    opacity: 0.7
                    text: Whatevr.AppController.currentUserJid.split("@")[0]
                }
            }
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Profile")
    }

    FormCard.FormCard {
        FormCard.FormTextDelegate {
            objectName: "profile.name"
            text: Whatevr.I18n.i18nc("@label", "Name")
            description: Whatevr.AppController.currentUserName.length > 0
                ? Whatevr.AppController.currentUserName
                : Whatevr.I18n.i18nc("@info", "Not set")
            // WhatsApp does not allow changing the display name from a linked
            // device, so this is informational only.
            trailing: Kirigami.Icon {
                source: "documentinfo-symbolic"
                implicitWidth: Kirigami.Units.iconSizes.small
                implicitHeight: Kirigami.Units.iconSizes.small
                opacity: 0.5
            }
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormTextFieldDelegate {
            id: aboutField
            objectName: "profile.about"
            label: Whatevr.I18n.i18nc("@label", "About")
            text: Whatevr.AppController.currentUserStatusText
            enabled: page.connected
            onEditingFinished: {
                const trimmed = text.trim();
                if (trimmed !== Whatevr.AppController.currentUserStatusText) {
                    Whatevr.AppController.setProfileStatus(trimmed);
                }
            }
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormTextDelegate {
            objectName: "profile.phone"
            text: Whatevr.I18n.i18nc("@label", "Phone number")
            description: {
                const jid = Whatevr.AppController.currentUserJid;
                return jid.length > 0 ? "+" + jid.split("@")[0] : Whatevr.I18n.i18nc("@info", "Unknown");
            }
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Connection")
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
