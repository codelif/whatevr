pragma ComponentBehavior: Bound

import QtQml
import org.kde.kirigamiaddons.settings as KirigamiSettings

import Whatevr as Whatevr

// The application's settings window. Wraps KirigamiAddons ConfigurationView
// (category sidebar on desktop, page stack on mobile) and adds a global
// "jump to a setting" capability used by the settings search in the drawer:
// openAt(moduleId, rowId) opens the right category and flashes the matched row.
KirigamiSettings.ConfigurationView {
    id: root

    title: Whatevr.I18n.i18nc("@title:window", "Settings")

    // Consumed by each module's initialProperties so the freshly created page
    // knows which row to flash. Set immediately before open().
    property string pendingModuleId: ""
    property string pendingRowId: ""

    function _initialFor(moduleId) {
        return {
            pendingRowId: root.pendingModuleId === moduleId ? root.pendingRowId : ""
        }
    }

    function openAt(moduleId, rowId) {
        root.pendingModuleId = moduleId
        root.pendingRowId = rowId
        root.open(moduleId)
    }

    // Flat index of every searchable option, consumed by the settings search.
    // Each record points at a delegate (objectName) inside a category page.
    readonly property var searchIndex: [
        { moduleId: "appearance", category: Whatevr.I18n.i18nc("@title settings category", "Appearance"),
          rowId: "appearance.colorScheme", label: Whatevr.I18n.i18nc("@label", "Color scheme"),
          keywords: ["theme", "dark", "light", "color", "colour", "appearance"] },
        { moduleId: "appearance", category: Whatevr.I18n.i18nc("@title settings category", "Appearance"),
          rowId: "appearance.showAvatars", label: Whatevr.I18n.i18nc("@label", "Show avatars"),
          keywords: ["avatar", "picture", "photo", "chat list"] },
        { moduleId: "appearance", category: Whatevr.I18n.i18nc("@title settings category", "Appearance"),
          rowId: "appearance.compactMode", label: Whatevr.I18n.i18nc("@label", "Compact mode"),
          keywords: ["density", "compact", "spacing", "tight"] },
        { moduleId: "appearance", category: Whatevr.I18n.i18nc("@title settings category", "Appearance"),
          rowId: "appearance.messageFontSize", label: Whatevr.I18n.i18nc("@label", "Message font size"),
          keywords: ["font", "text", "size", "zoom", "bigger", "smaller"] },

        { moduleId: "behavior", category: Whatevr.I18n.i18nc("@title settings category", "Behavior"),
          rowId: "behavior.enterToSend", label: Whatevr.I18n.i18nc("@label", "Press Enter to send"),
          keywords: ["enter", "send", "return", "newline", "ctrl"] },
        { moduleId: "behavior", category: Whatevr.I18n.i18nc("@title settings category", "Behavior"),
          rowId: "behavior.persistDrafts", label: Whatevr.I18n.i18nc("@label", "Save unsent drafts"),
          keywords: ["draft", "drafts", "unsent", "restore", "persist"] },

        { moduleId: "window", category: Whatevr.I18n.i18nc("@title settings category", "Window & Layout"),
          rowId: "window.rememberGeometry", label: Whatevr.I18n.i18nc("@label", "Remember window size and position"),
          keywords: ["window", "geometry", "size", "position", "remember"] },
        { moduleId: "window", category: Whatevr.I18n.i18nc("@title settings category", "Window & Layout"),
          rowId: "window.rememberColumnWidth", label: Whatevr.I18n.i18nc("@label", "Remember chat list width"),
          keywords: ["column", "width", "chat list", "sidebar", "remember"] },

        { moduleId: "account", category: Whatevr.I18n.i18nc("@title settings category", "Account"),
          rowId: "account.status", label: Whatevr.I18n.i18nc("@label", "Account status"),
          keywords: ["account", "status", "connected", "login"] },
        { moduleId: "account", category: Whatevr.I18n.i18nc("@title settings category", "Account"),
          rowId: "account.logout", label: Whatevr.I18n.i18nc("@label", "Log out"),
          keywords: ["logout", "log out", "sign out", "disconnect"] },

        { moduleId: "storage", category: Whatevr.I18n.i18nc("@title settings category", "Storage & Cache"),
          rowId: "storage.cacheSize", label: Whatevr.I18n.i18nc("@label", "Cache size"),
          keywords: ["cache", "storage", "disk", "media", "size"] },
        { moduleId: "storage", category: Whatevr.I18n.i18nc("@title settings category", "Storage & Cache"),
          rowId: "storage.clearCache", label: Whatevr.I18n.i18nc("@label", "Clear media cache"),
          keywords: ["clear", "delete", "cache", "media", "free space"] },

        { moduleId: "emoji", category: Whatevr.I18n.i18nc("@title settings category", "Emoji & Stickers"),
          rowId: "emoji.skinTone", label: Whatevr.I18n.i18nc("@label", "Default skin tone"),
          keywords: ["emoji", "skin", "tone", "color"] },
        { moduleId: "emoji", category: Whatevr.I18n.i18nc("@title settings category", "Emoji & Stickers"),
          rowId: "emoji.resetRecent", label: Whatevr.I18n.i18nc("@label", "Reset recently used emoji"),
          keywords: ["emoji", "recent", "reset", "clear", "history"] },

        { moduleId: "shortcuts", category: Whatevr.I18n.i18nc("@title settings category", "Keyboard Shortcuts"),
          rowId: "shortcuts.settings", label: Whatevr.I18n.i18nc("@label", "Keyboard shortcuts"),
          keywords: ["keyboard", "shortcut", "keys", "hotkey", "accelerator"] }
    ]

    modules: [
        KirigamiSettings.ConfigurationModule {
            moduleId: "appearance"
            text: Whatevr.I18n.i18nc("@title settings category", "Appearance")
            icon.name: "preferences-desktop-color-symbolic"
            page: () => Qt.createComponent("Whatevr", "AppearancePage")
            initialProperties: () => root._initialFor("appearance")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "behavior"
            text: Whatevr.I18n.i18nc("@title settings category", "Behavior")
            icon.name: "preferences-system-symbolic"
            page: () => Qt.createComponent("Whatevr", "BehaviorPage")
            initialProperties: () => root._initialFor("behavior")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "window"
            text: Whatevr.I18n.i18nc("@title settings category", "Window & Layout")
            icon.name: "preferences-system-windows-symbolic"
            page: () => Qt.createComponent("Whatevr", "WindowLayoutPage")
            initialProperties: () => root._initialFor("window")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "account"
            text: Whatevr.I18n.i18nc("@title settings category", "Account")
            icon.name: "preferences-desktop-user-symbolic"
            page: () => Qt.createComponent("Whatevr", "AccountPage")
            initialProperties: () => root._initialFor("account")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "storage"
            text: Whatevr.I18n.i18nc("@title settings category", "Storage & Cache")
            icon.name: "drive-harddisk-symbolic"
            page: () => Qt.createComponent("Whatevr", "StoragePage")
            initialProperties: () => root._initialFor("storage")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "emoji"
            text: Whatevr.I18n.i18nc("@title settings category", "Emoji & Stickers")
            icon.name: "smiley-symbolic"
            page: () => Qt.createComponent("Whatevr", "EmojiStickersPage")
            initialProperties: () => root._initialFor("emoji")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "shortcuts"
            text: Whatevr.I18n.i18nc("@title settings category", "Keyboard Shortcuts")
            icon.name: "configure-shortcuts-symbolic"
            page: () => Qt.createComponent("Whatevr", "KeyboardShortcutsPage")
            initialProperties: () => root._initialFor("shortcuts")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "about"
            text: Whatevr.I18n.i18nc("@title settings category", "About")
            icon.name: "help-about-symbolic"
            page: () => Qt.createComponent("org.kde.kirigamiaddons.formcard", "AboutPage")
        }
    ]
}
