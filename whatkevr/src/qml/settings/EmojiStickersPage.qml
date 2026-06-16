import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Emoji & Stickers")

    // Index 0-5 maps to the Fitzpatrick skin-tone modifiers; the sample is a
    // hand-wave emoji so each tile shows the tone directly.
    readonly property var skinTones: [
        { value: 0, label: Whatevr.I18n.i18nc("@item:inlistbox skin tone", "Default"), sample: "👋" },
        { value: 1, label: Whatevr.I18n.i18nc("@item:inlistbox skin tone", "Light"), sample: "👋🏻" },
        { value: 2, label: Whatevr.I18n.i18nc("@item:inlistbox skin tone", "Medium-light"), sample: "👋🏼" },
        { value: 3, label: Whatevr.I18n.i18nc("@item:inlistbox skin tone", "Medium"), sample: "👋🏽" },
        { value: 4, label: Whatevr.I18n.i18nc("@item:inlistbox skin tone", "Medium-dark"), sample: "👋🏾" },
        { value: 5, label: Whatevr.I18n.i18nc("@item:inlistbox skin tone", "Dark"), sample: "👋🏿" }
    ]

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Emoji")
    }

    FormCard.FormCard {
        PreviewSelector {
            objectName: "emoji.skinTone"
            previewKind: "skintone"
            options: page.skinTones
            currentValue: Whatevr.Settings.defaultSkinTone
            onActivated: value => Whatevr.Settings.defaultSkinTone = value
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormButtonDelegate {
            objectName: "emoji.resetRecent"
            text: Whatevr.I18n.i18nc("@action:button", "Reset recently used emoji")
            icon.name: "edit-clear-history-symbolic"
            onClicked: Whatevr.AppController.emojiModel.resetRecents()
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Stickers")
    }

    FormCard.FormCard {
        FormCard.FormTextDelegate {
            objectName: "stickers.favoritesInfo"
            text: Whatevr.I18n.i18nc("@label", "Favorite stickers")
            description: Whatevr.I18n.i18nc("@info", "Favorite or unfavorite a sticker from its message, or in the sticker picker. They sync with your phone.")
        }
    }
}
