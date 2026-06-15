import QtQuick
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
}
