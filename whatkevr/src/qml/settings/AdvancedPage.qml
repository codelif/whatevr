import QtQuick
import QtQuick.Controls as QQC2
import org.kde.kirigami as Kirigami
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Advanced")

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Video playback")
    }

    FormCard.FormCard {
        FormCard.FormComboBoxDelegate {
            id: gifPlayerCombo
            objectName: "advanced.gifPlayerLimit"
            text: Whatevr.I18n.i18nc("@label:listbox", "GIFs playing at once")
            description: Whatevr.I18n.i18nc("@info", "Each one holds a decoder. \"None\" keeps GIF bubbles as still thumbnails.")
            textRole: "label"
            valueRole: "limit"
            model: [
                { label: Whatevr.I18n.i18nc("@item:inlistbox", "None"), limit: 0 },
                { label: Whatevr.I18n.i18nc("@item:inlistbox", "1"), limit: 1 },
                { label: Whatevr.I18n.i18nc("@item:inlistbox", "2"), limit: 2 },
                { label: Whatevr.I18n.i18nc("@item:inlistbox", "3"), limit: 3 }
            ]
            currentIndex: Math.max(0, Math.min(3, Whatevr.Settings.gifPlayerLimit))
            onActivated: index => Whatevr.Settings.gifPlayerLimit = model[index].limit
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

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "App lock")
    }

    FormCard.FormCard {
        FormCard.FormButtonDelegate {
            text: Whatevr.Settings.appLockEnabled
                ? Whatevr.I18n.i18nc("@action:button lock app", "Lock now")
                : Whatevr.I18n.i18nc("@action:button set app lock", "Set app lock PIN")
            icon.name: "object-locked-symbolic"
            onClicked: {
                if (Whatevr.Settings.appLockEnabled) {
                    Whatevr.Settings.lockApp()
                } else {
                    pinDialog.open()
                }
            }
        }
    }

    Kirigami.PromptDialog {
        id: pinDialog
        title: Whatevr.I18n.i18nc("@title:dialog", "Set app lock PIN")
        subtitle: Whatevr.I18n.i18nc("@info", "Use at least four characters. This locks the frontend only; the daemon remains available to same-user processes.")
        standardButtons: Kirigami.Dialog.Ok | Kirigami.Dialog.Cancel
        QQC2.TextField {
            id: appLockPin
            placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "PIN")
            echoMode: QQC2.TextInput.Password
        }
        onAccepted: {
            if (Whatevr.Settings.setAppLockPin(appLockPin.text)) {
                appLockPin.clear()
                pinDialog.close()
            }
        }
    }
}
