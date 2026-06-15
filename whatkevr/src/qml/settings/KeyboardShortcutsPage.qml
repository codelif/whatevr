import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Keyboard Shortcuts")

    component ShortcutRow: FormCard.FormTextDelegate {
        property string keys: ""
        description: keys
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Application")
    }

    FormCard.FormCard {
        ShortcutRow {
            objectName: "shortcuts.settings"
            text: Whatevr.I18n.i18nc("@label keyboard shortcut", "Open settings")
            keys: "Ctrl+,"
        }
        FormCard.FormDelegateSeparator {}
        ShortcutRow {
            objectName: "shortcuts.quit"
            text: Whatevr.I18n.i18nc("@label keyboard shortcut", "Quit")
            keys: "Ctrl+Q"
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Conversation")
    }

    FormCard.FormCard {
        ShortcutRow {
            objectName: "shortcuts.send"
            text: Whatevr.I18n.i18nc("@label keyboard shortcut", "Send message")
            keys: Whatevr.Settings.enterToSend ? "Enter" : "Ctrl+Enter"
        }
        FormCard.FormDelegateSeparator {}
        ShortcutRow {
            objectName: "shortcuts.newline"
            text: Whatevr.I18n.i18nc("@label keyboard shortcut", "Insert new line")
            keys: Whatevr.Settings.enterToSend ? "Shift+Enter" : "Enter"
        }
        FormCard.FormDelegateSeparator {}
        ShortcutRow {
            objectName: "shortcuts.pasteImage"
            text: Whatevr.I18n.i18nc("@label keyboard shortcut", "Paste image from clipboard")
            keys: "Ctrl+V"
        }
    }
}
