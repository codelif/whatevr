import QtQuick
import org.kde.kirigami as Kirigami
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Advanced")

    readonly property var backendValues: ["auto", "mpv", "qt"]

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Video playback")
    }

    FormCard.FormCard {
        FormCard.FormComboBoxDelegate {
            objectName: "advanced.videoBackend"
            text: Whatevr.I18n.i18nc("@label", "Playback engine")
            description: Whatevr.I18n.i18nc("@info", "Takes effect after a restart. Automatic uses mpv where the system supports it, and Qt Multimedia otherwise.")
            model: [
                Whatevr.I18n.i18nc("@item playback engine", "Automatic"),
                Whatevr.I18n.i18nc("@item playback engine", "mpv"),
                Whatevr.I18n.i18nc("@item playback engine", "Qt Multimedia")
            ]
            currentIndex: Math.max(0, page.backendValues.indexOf(Whatevr.Settings.videoBackend))
            onActivated: index => {
                Whatevr.Settings.videoBackend = page.backendValues[index]
            }
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormTextDelegate {
            objectName: "advanced.activeBackend"
            text: Whatevr.I18n.i18nc("@label", "Currently in use")
            // Worth showing plainly: it turns "video is broken" into a report
            // that says which engine was live and why it was picked.
            description: Whatevr.MediaBackend.description

            leading: Kirigami.Icon {
                source: "video-x-generic"
                implicitWidth: Kirigami.Units.iconSizes.medium
                implicitHeight: Kirigami.Units.iconSizes.medium
            }
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "advanced.hardwareDecoding"
            text: Whatevr.I18n.i18nc("@option:check", "Hardware decoding")
            description: Whatevr.I18n.i18nc("@info", "Decode video on the GPU. Turn this off if video tears, stutters or shows artefacts.")
            checked: Whatevr.Settings.hardwareDecoding
            onToggled: Whatevr.Settings.hardwareDecoding = checked
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormComboBoxDelegate {
            id: inlineVideoCombo
            objectName: "advanced.inlineVideoLimit"
            text: Whatevr.I18n.i18nc("@label:listbox", "Videos playing at once")
            description: Whatevr.I18n.i18nc("@info", "Each one holds a decoder. \"None\" keeps every bubble a thumbnail and plays only in the full-screen viewer.")
            textRole: "label"
            valueRole: "limit"
            model: [
                { label: Whatevr.I18n.i18nc("@item:inlistbox", "None"), limit: 0 },
                { label: Whatevr.I18n.i18nc("@item:inlistbox", "1"), limit: 1 },
                { label: Whatevr.I18n.i18nc("@item:inlistbox", "2"), limit: 2 },
                { label: Whatevr.I18n.i18nc("@item:inlistbox", "3"), limit: 3 }
            ]
            currentIndex: Math.max(0, Math.min(3, Whatevr.Settings.inlineVideoLimit))
            onActivated: index => Whatevr.Settings.inlineVideoLimit = model[index].limit
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "advanced.pausePlaybackWhileScrolling"
            text: Whatevr.I18n.i18nc("@option:check", "Pause playback while scrolling fast")
            description: Whatevr.I18n.i18nc("@info", "Keeps a fast flick through a chat full of clips smooth.")
            checked: Whatevr.Settings.pausePlaybackWhileScrolling
            onToggled: Whatevr.Settings.pausePlaybackWhileScrolling = checked
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "advanced.streamWhileDownloading"
            text: Whatevr.I18n.i18nc("@option:check", "Play while downloading")
            description: Whatevr.I18n.i18nc("@info", "Start a video before the whole file has arrived, and seek anywhere in it. Off waits for the download to finish.")
            checked: Whatevr.Settings.streamWhileDownloading
            onToggled: Whatevr.Settings.streamWhileDownloading = checked
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Audio playback")
    }

    FormCard.FormCard {
        FormCard.FormTextDelegate {
            objectName: "advanced.audioEngine"
            text: Whatevr.I18n.i18nc("@label", "Voice notes and audio")
            // When mpv could not be created at all, saying so here is the
            // difference between a bug report and a shrug.
            description: Whatevr.AudioPlayer.available
                ? Whatevr.I18n.i18nc("@info", "Always played through mpv, which changes speed without changing pitch.")
                : Whatevr.I18n.i18nc("@info", "Unavailable: mpv could not be initialized, so nothing will play.")

            leading: Kirigami.Icon {
                source: "audio-x-generic"
                implicitWidth: Kirigami.Units.iconSizes.medium
                implicitHeight: Kirigami.Units.iconSizes.medium
            }
        }
    }
}
