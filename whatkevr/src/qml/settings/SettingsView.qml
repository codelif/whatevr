pragma ComponentBehavior: Bound

import QtQml
import org.kde.kirigami as Kirigami
import org.kde.kirigamiaddons.settings as KirigamiSettings

import Whatevr as Whatevr

// The application's settings entry point. Mirrors KirigamiAddons'
// ConfigurationView API (open/openAt) but spawns the project's own
// SettingsWindow, whose sidebar search matches every individual option
// (label + keywords + description) instead of only category names.
QtObject {
    id: root

    property Kirigami.ApplicationWindow window
    property string title: Whatevr.I18n.i18nc("@title:window", "Settings")

    // The live settings window, or null when closed.
    property QtObject configViewItem: null

    // Open the settings window, optionally preselecting a category moduleId.
    function open(defaultModule = "") {
        if (root.configViewItem) {
            if (typeof root.configViewItem.requestActivate === "function")
                Qt.callLater(root.configViewItem.requestActivate)
            return
        }

        const component = Qt.createComponent("Whatevr", "SettingsWindow")
        if (component.status === Component.Error) {
            console.error(component.errorString())
            return
        }
        root.configViewItem = component.createObject(root.window, {
            defaultModule: defaultModule,
            modules: root.modules,
            searchIndex: root.searchIndex,
            title: root.title,
            width: Kirigami.Units.gridUnit * 50,
            height: Kirigami.Units.gridUnit * 30,
            minimumWidth: Kirigami.Units.gridUnit * 50,
            minimumHeight: Kirigami.Units.gridUnit * 30
        })
        root.configViewItem.closing.connect(event => {
            if (event.accepted) {
                root.configViewItem.destroy()
                root.configViewItem = null
            }
        })
    }

    // Open settings at a specific option and flash it.
    function openAt(moduleId, rowId) {
        if (root.configViewItem) {
            root.configViewItem.navigateToRow(moduleId, rowId)
            if (typeof root.configViewItem.requestActivate === "function")
                Qt.callLater(root.configViewItem.requestActivate)
            return
        }

        const component = Qt.createComponent("Whatevr", "SettingsWindow")
        if (component.status === Component.Error) {
            console.error(component.errorString())
            return
        }
        root.configViewItem = component.createObject(root.window, {
            defaultModule: moduleId,
            pendingModuleId: moduleId,
            pendingRowId: rowId,
            modules: root.modules,
            searchIndex: root.searchIndex,
            title: root.title,
            width: Kirigami.Units.gridUnit * 50,
            height: Kirigami.Units.gridUnit * 30,
            minimumWidth: Kirigami.Units.gridUnit * 50,
            minimumHeight: Kirigami.Units.gridUnit * 30
        })
        root.configViewItem.closing.connect(event => {
            if (event.accepted) {
                root.configViewItem.destroy()
                root.configViewItem = null
            }
        })
    }

    // Flat index of every searchable option, consumed by the settings search.
    // Each record points at a delegate (objectName) inside a category page and
    // carries its description so a query matches option text the user can read.
    readonly property var searchIndex: [
        { moduleId: "appearance", category: Whatevr.I18n.i18nc("@title settings category", "Appearance"),
          rowId: "appearance.colorScheme", label: Whatevr.I18n.i18nc("@label", "Color scheme"),
          description: Whatevr.I18n.i18nc("@info", "Override the application colors or follow the system theme."),
          keywords: ["theme", "dark", "light", "color", "colour", "appearance", "system"] },
        { moduleId: "appearance", category: Whatevr.I18n.i18nc("@title settings category", "Appearance"),
          rowId: "appearance.showAvatars", label: Whatevr.I18n.i18nc("@label", "Show avatars in the chat list"),
          description: "",
          keywords: ["avatar", "picture", "photo", "chat list"] },
        { moduleId: "appearance", category: Whatevr.I18n.i18nc("@title settings category", "Appearance"),
          rowId: "appearance.compactMode", label: Whatevr.I18n.i18nc("@label", "Compact mode"),
          description: Whatevr.I18n.i18nc("@info", "Tighter spacing in lists and conversations."),
          keywords: ["density", "compact", "spacing", "tight"] },
        { moduleId: "appearance", category: Whatevr.I18n.i18nc("@title settings category", "Appearance"),
          rowId: "appearance.messageFontSize", label: Whatevr.I18n.i18nc("@label", "Message font size"),
          description: Whatevr.I18n.i18nc("@info", "Point size for message text. 0 follows the system font."),
          keywords: ["font", "text", "size", "zoom", "bigger", "smaller", "point"] },

        { moduleId: "behavior", category: Whatevr.I18n.i18nc("@title settings category", "Behavior"),
          rowId: "behavior.enterToSend", label: Whatevr.I18n.i18nc("@label", "Press Enter to send"),
          description: Whatevr.I18n.i18nc("@info", "Enter sends the message; Shift+Enter inserts a new line."),
          keywords: ["enter", "send", "return", "newline", "ctrl", "shift"] },
        { moduleId: "behavior", category: Whatevr.I18n.i18nc("@title settings category", "Behavior"),
          rowId: "behavior.persistDrafts", label: Whatevr.I18n.i18nc("@label", "Save unsent drafts"),
          description: Whatevr.I18n.i18nc("@info", "Keep half-written messages when the app is closed and reopened."),
          keywords: ["draft", "drafts", "unsent", "restore", "persist"] },

        { moduleId: "window", category: Whatevr.I18n.i18nc("@title settings category", "Window & Layout"),
          rowId: "window.rememberGeometry", label: Whatevr.I18n.i18nc("@label", "Remember window size and position"),
          description: "",
          keywords: ["window", "geometry", "size", "position", "remember"] },
        { moduleId: "window", category: Whatevr.I18n.i18nc("@title settings category", "Window & Layout"),
          rowId: "window.rememberColumnWidth", label: Whatevr.I18n.i18nc("@label", "Remember chat list width"),
          description: Whatevr.I18n.i18nc("@info", "Restore the width you set for the chat list column."),
          keywords: ["column", "width", "chat list", "sidebar", "remember"] },

        { moduleId: "account", category: Whatevr.I18n.i18nc("@title settings category", "Account"),
          rowId: "account.status", label: Whatevr.I18n.i18nc("@label", "Status"),
          description: "",
          keywords: ["account", "status", "connected", "login", "signed", "disconnect"] },
        { moduleId: "account", category: Whatevr.I18n.i18nc("@title settings category", "Account"),
          rowId: "account.logout", label: Whatevr.I18n.i18nc("@label", "Log out"),
          description: Whatevr.I18n.i18nc("@info", "Disconnect this device from your WhatsApp account."),
          keywords: ["logout", "log out", "sign out", "disconnect"] },

        { moduleId: "storage", category: Whatevr.I18n.i18nc("@title settings category", "Storage & Cache"),
          rowId: "storage.cacheSize", label: Whatevr.I18n.i18nc("@label", "Cache size"),
          description: "",
          keywords: ["cache", "storage", "disk", "media", "size"] },
        { moduleId: "storage", category: Whatevr.I18n.i18nc("@title settings category", "Storage & Cache"),
          rowId: "storage.cachePath", label: Whatevr.I18n.i18nc("@label", "Location"),
          description: "",
          keywords: ["cache", "path", "location", "folder", "directory", "media"] },
        { moduleId: "storage", category: Whatevr.I18n.i18nc("@title settings category", "Storage & Cache"),
          rowId: "storage.clearCache", label: Whatevr.I18n.i18nc("@label", "Clear media cache"),
          description: Whatevr.I18n.i18nc("@info", "Delete downloaded images, videos and other attachments. They are re-downloaded when needed."),
          keywords: ["clear", "delete", "cache", "media", "free space"] },

        { moduleId: "emoji", category: Whatevr.I18n.i18nc("@title settings category", "Emoji & Stickers"),
          rowId: "emoji.skinTone", label: Whatevr.I18n.i18nc("@label", "Default skin tone"),
          description: "",
          keywords: ["emoji", "skin", "tone", "color"] },
        { moduleId: "emoji", category: Whatevr.I18n.i18nc("@title settings category", "Emoji & Stickers"),
          rowId: "emoji.resetRecent", label: Whatevr.I18n.i18nc("@label", "Reset recently used emoji"),
          description: "",
          keywords: ["emoji", "recent", "reset", "clear", "history"] },
        { moduleId: "emoji", category: Whatevr.I18n.i18nc("@title settings category", "Emoji & Stickers"),
          rowId: "stickers.favoritesInfo", label: Whatevr.I18n.i18nc("@label", "Favorite stickers"),
          description: Whatevr.I18n.i18nc("@info", "Favorite or unfavorite a sticker from its message, or in the sticker picker. They sync with your phone."),
          keywords: ["sticker", "stickers", "favorite", "favourite", "sync"] },

        { moduleId: "shortcuts", category: Whatevr.I18n.i18nc("@title settings category", "Keyboard Shortcuts"),
          rowId: "shortcuts.settings", label: Whatevr.I18n.i18nc("@label", "Open settings"),
          description: "Ctrl+,",
          keywords: ["keyboard", "shortcut", "keys", "hotkey", "settings", "preferences"] },
        { moduleId: "shortcuts", category: Whatevr.I18n.i18nc("@title settings category", "Keyboard Shortcuts"),
          rowId: "shortcuts.quit", label: Whatevr.I18n.i18nc("@label", "Quit"),
          description: "Ctrl+Q",
          keywords: ["keyboard", "shortcut", "quit", "exit", "close"] },
        { moduleId: "shortcuts", category: Whatevr.I18n.i18nc("@title settings category", "Keyboard Shortcuts"),
          rowId: "shortcuts.send", label: Whatevr.I18n.i18nc("@label", "Send message"),
          description: "",
          keywords: ["keyboard", "shortcut", "send", "enter", "message"] },
        { moduleId: "shortcuts", category: Whatevr.I18n.i18nc("@title settings category", "Keyboard Shortcuts"),
          rowId: "shortcuts.newline", label: Whatevr.I18n.i18nc("@label", "Insert new line"),
          description: "",
          keywords: ["keyboard", "shortcut", "newline", "new line", "enter", "shift"] },
        { moduleId: "shortcuts", category: Whatevr.I18n.i18nc("@title settings category", "Keyboard Shortcuts"),
          rowId: "shortcuts.pasteImage", label: Whatevr.I18n.i18nc("@label", "Paste image from clipboard"),
          description: "Ctrl+V",
          keywords: ["keyboard", "shortcut", "paste", "image", "clipboard"] }
    ]

    property list<KirigamiSettings.ConfigurationModule> modules: [
        KirigamiSettings.ConfigurationModule {
            moduleId: "appearance"
            text: Whatevr.I18n.i18nc("@title settings category", "Appearance")
            icon.name: "preferences-desktop-color-symbolic"
            page: () => Qt.createComponent("Whatevr", "AppearancePage")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "behavior"
            text: Whatevr.I18n.i18nc("@title settings category", "Behavior")
            icon.name: "preferences-system-symbolic"
            page: () => Qt.createComponent("Whatevr", "BehaviorPage")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "window"
            text: Whatevr.I18n.i18nc("@title settings category", "Window & Layout")
            icon.name: "preferences-system-windows-symbolic"
            page: () => Qt.createComponent("Whatevr", "WindowLayoutPage")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "account"
            text: Whatevr.I18n.i18nc("@title settings category", "Account")
            icon.name: "preferences-desktop-user-symbolic"
            page: () => Qt.createComponent("Whatevr", "AccountPage")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "storage"
            text: Whatevr.I18n.i18nc("@title settings category", "Storage & Cache")
            icon.name: "drive-harddisk-symbolic"
            page: () => Qt.createComponent("Whatevr", "StoragePage")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "emoji"
            text: Whatevr.I18n.i18nc("@title settings category", "Emoji & Stickers")
            icon.name: "smiley-symbolic"
            page: () => Qt.createComponent("Whatevr", "EmojiStickersPage")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "shortcuts"
            text: Whatevr.I18n.i18nc("@title settings category", "Keyboard Shortcuts")
            icon.name: "configure-shortcuts-symbolic"
            page: () => Qt.createComponent("Whatevr", "KeyboardShortcutsPage")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "about"
            text: Whatevr.I18n.i18nc("@title settings category", "About")
            icon.name: "help-about-symbolic"
            page: () => Qt.createComponent("org.kde.kirigamiaddons.formcard", "AboutPage")
        }
    ]
}
