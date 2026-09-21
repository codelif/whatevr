import QtQuick
import QtQuick.Controls as QQC2
import Qt.labs.platform as Platform
import org.kde.kirigami as Kirigami
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Storage & Cache")

    // Re-read after a clear, and once on open.
    property string cacheText: ""

    function refreshCache() {
        cacheText = Whatevr.Settings.formattedCacheSize()
    }

    Component.onCompleted: refreshCache()

    Connections {
        target: Whatevr.Settings
        function onCacheChanged() {
            page.refreshCache()
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Downloaded media")
    }

    FormCard.FormCard {
        FormCard.FormTextDelegate {
            objectName: "storage.cacheSize"
            text: Whatevr.I18n.i18nc("@label", "Cache size")
            description: page.cacheText

            leading: Kirigami.Icon {
                source: "drive-harddisk-symbolic"
                implicitWidth: Kirigami.Units.iconSizes.medium
                implicitHeight: Kirigami.Units.iconSizes.medium
            }
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormTextDelegate {
            objectName: "storage.cachePath"
            text: Whatevr.I18n.i18nc("@label", "Location")
            description: Whatevr.Settings.mediaCachePath()
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormButtonDelegate {
            objectName: "storage.clearCache"
            text: Whatevr.I18n.i18nc("@action:button", "Clear media cache")
            description: Whatevr.I18n.i18nc("@info", "Delete downloaded images, videos and other attachments. They are re-downloaded when needed.")
            icon.name: "edit-clear-all-symbolic"
            onClicked: Whatevr.Settings.clearMediaCache()
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Backup and restore")
    }

    FormCard.FormCard {
        FormCard.FormTextDelegate {
            text: Whatevr.I18n.i18nc("@label", "Encrypted backup")
            description: Whatevr.I18n.i18nc("@info", "Exports messages, WhatsApp session data, and media to an encrypted bundle when a passphrase is supplied.")
        }

        FormCard.FormButtonDelegate {
            text: Whatevr.I18n.i18nc("@action:button", "Export backup…")
            icon.name: "document-save-symbolic"
            onClicked: backupDialog.open()
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormButtonDelegate {
            text: Whatevr.I18n.i18nc("@action:button", "Store passphrase in keyring")
            icon.name: "dialog-password-symbolic"
            onClicked: keyringPassphraseDialog.open()
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormTextDelegate {
            text: Whatevr.I18n.i18nc("@label", "Restore")
            description: Whatevr.I18n.i18nc("@info", "Restore is offline: stop whatevrd, then run whatevrd --restore with the backup bundle and restart the service.")
        }
    }

    Platform.FileDialog {
        id: backupDialog
        title: Whatevr.I18n.i18nc("@title:window", "Export backup")
        fileMode: Platform.FileDialog.SaveFile
        nameFilters: [Whatevr.I18n.i18nc("@item:inlistbox", "Whatevr backup (*.tar.gz *.wvrbackup)")]
        onAccepted: backupPassphraseDialog.openFor(file)
    }

    Kirigami.PromptDialog {
        id: backupPassphraseDialog
        property url destination
        title: Whatevr.I18n.i18nc("@title:dialog", "Encrypt backup")
        subtitle: Whatevr.I18n.i18nc("@info", "Leave blank for an unencrypted archive, or enter a passphrase.")
        standardButtons: Kirigami.Dialog.Ok | Kirigami.Dialog.Cancel

        function openFor(url) {
            destination = url
            passphrase.clear()
            open()
        }

        QQC2.TextField {
            id: passphrase
            placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Optional passphrase")
            echoMode: QQC2.TextInput.Password
        }

        onAccepted: {
            Whatevr.ProtocolController.exportBackup(destination, passphrase.text, false)
            passphrase.clear()
        }
    }

    Kirigami.PromptDialog {
        id: keyringPassphraseDialog
        title: Whatevr.I18n.i18nc("@title:dialog", "Store backup passphrase")
        subtitle: Whatevr.I18n.i18nc("@info", "The passphrase is stored in the desktop keyring and is not sent to the daemon again.")
        standardButtons: Kirigami.Dialog.Ok | Kirigami.Dialog.Cancel

        QQC2.TextField {
            id: keyringPassphrase
            placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Passphrase")
            echoMode: QQC2.TextInput.Password
        }

        onAccepted: {
            Whatevr.ProtocolController.setBackupPassphrase(keyringPassphrase.text)
            keyringPassphrase.clear()
        }
    }
}
