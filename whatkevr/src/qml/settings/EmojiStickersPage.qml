import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Emoji & Stickers")

    readonly property var skinTones: [
        Whatevr.I18n.i18nc("@item:inlistbox skin tone", "Default"),
        Whatevr.I18n.i18nc("@item:inlistbox skin tone", "Light"),
        Whatevr.I18n.i18nc("@item:inlistbox skin tone", "Medium-light"),
        Whatevr.I18n.i18nc("@item:inlistbox skin tone", "Medium"),
        Whatevr.I18n.i18nc("@item:inlistbox skin tone", "Medium-dark"),
        Whatevr.I18n.i18nc("@item:inlistbox skin tone", "Dark")
    ]

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Emoji")
    }

    FormCard.FormCard {
        FormCard.FormComboBoxDelegate {
            objectName: "emoji.skinTone"
            text: Whatevr.I18n.i18nc("@label:listbox", "Default skin tone")
            model: page.skinTones
            currentIndex: Whatevr.Settings.defaultSkinTone
            onActivated: index => Whatevr.Settings.defaultSkinTone = index
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
