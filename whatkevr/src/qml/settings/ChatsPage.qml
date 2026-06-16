import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr
import "../components/Wallpapers.js" as Wallpapers

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Chats")

    Component.onCompleted: Whatevr.AppController.refreshAppPreferences()

    // Wallpaper presets, augmented with preview colors for the tile renderer.
    readonly property var wallpaperOptions: {
        const out = [];
        const presets = Wallpapers.presets();
        for (let i = 0; i < presets.length; i++) {
            out.push({ value: presets[i].value, label: presets[i].label, bg: presets[i].bg });
        }
        return out;
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Composing")
    }

    FormCard.FormCard {
        FormCard.FormSwitchDelegate {
            objectName: "chats.enterToSend"
            text: Whatevr.I18n.i18nc("@option:check", "Press Enter to send")
            description: checked
                ? Whatevr.I18n.i18nc("@info", "Enter sends the message; Shift+Enter inserts a new line.")
                : Whatevr.I18n.i18nc("@info", "Enter inserts a new line; Ctrl+Enter sends the message.")
            checked: Whatevr.Settings.enterToSend
            onToggled: Whatevr.Settings.enterToSend = checked
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "chats.persistDrafts"
            text: Whatevr.I18n.i18nc("@option:check", "Save unsent drafts")
            description: Whatevr.I18n.i18nc("@info", "Keep half-written messages when the app is closed and reopened.")
            checked: Whatevr.Settings.persistDrafts
            onToggled: Whatevr.Settings.persistDrafts = checked
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Wallpaper")
    }

    FormCard.FormCard {
        PreviewSelector {
            objectName: "chats.wallpaper"
            previewKind: "wallpaper"
            options: page.wallpaperOptions
            currentValue: Whatevr.Settings.chatWallpaper
            onActivated: value => Whatevr.Settings.chatWallpaper = value
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Media auto-download")
    }

    FormCard.FormCard {
        FormCard.FormSwitchDelegate {
            objectName: "chats.autoDownloadPhotos"
            text: Whatevr.I18n.i18nc("@option:check", "Photos")
            description: Whatevr.I18n.i18nc("@info", "Download incoming photos automatically.")
            checked: Whatevr.AppController.appPreferences.autoDownloadPhotos ?? false
            onToggled: Whatevr.AppController.setAppPreference("autoDownloadPhotos", checked)
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "chats.autoDownloadVideos"
            text: Whatevr.I18n.i18nc("@option:check", "Videos")
            checked: Whatevr.AppController.appPreferences.autoDownloadVideos ?? false
            onToggled: Whatevr.AppController.setAppPreference("autoDownloadVideos", checked)
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "chats.autoDownloadAudio"
            text: Whatevr.I18n.i18nc("@option:check", "Voice messages and audio")
            checked: Whatevr.AppController.appPreferences.autoDownloadAudio ?? false
            onToggled: Whatevr.AppController.setAppPreference("autoDownloadAudio", checked)
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "chats.autoDownloadDocuments"
            text: Whatevr.I18n.i18nc("@option:check", "Documents")
            checked: Whatevr.AppController.appPreferences.autoDownloadDocuments ?? false
            onToggled: Whatevr.AppController.setAppPreference("autoDownloadDocuments", checked)
        }
    }
}
