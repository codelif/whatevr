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

    // Close the settings window if it is open. Used on logout so the window
    // does not linger over the login screen.
    function close() {
        if (root.configViewItem) {
            root.configViewItem.destroy()
            root.configViewItem = null
        }
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
          rowId: "appearance.themeMode", label: Whatevr.I18n.i18nc("@label", "Theme"),
          description: Whatevr.I18n.i18nc("@info", "Use a light, dark, or system color theme."),
          keywords: ["theme", "dark", "light", "color", "colour", "appearance", "system"] },
        { moduleId: "appearance", category: Whatevr.I18n.i18nc("@title settings category", "Appearance"),
          rowId: "appearance.density", label: Whatevr.I18n.i18nc("@label", "Density"),
          description: Whatevr.I18n.i18nc("@info", "Compact, standard, or comfortable spacing in conversations."),
          keywords: ["density", "compact", "comfortable", "spacing", "tight"] },
        { moduleId: "appearance", category: Whatevr.I18n.i18nc("@title settings category", "Appearance"),
          rowId: "appearance.messageFontSize", label: Whatevr.I18n.i18nc("@label", "Message text size"),
          description: Whatevr.I18n.i18nc("@info", "Small, default, large, or extra large message text."),
          keywords: ["font", "text", "size", "zoom", "bigger", "smaller", "point"] },
        { moduleId: "appearance", category: Whatevr.I18n.i18nc("@title settings category", "Appearance"),
          rowId: "appearance.showAvatars", label: Whatevr.I18n.i18nc("@label", "Show avatars in the chat list"),
          description: "",
          keywords: ["avatar", "picture", "photo", "chat list"] },
        { moduleId: "appearance", category: Whatevr.I18n.i18nc("@title settings category", "Appearance"),
          rowId: "appearance.colorScheme", label: Whatevr.I18n.i18nc("@label", "Color scheme"),
          description: Whatevr.I18n.i18nc("@info", "Pick a specific color scheme instead of just light or dark."),
          keywords: ["theme", "scheme", "color", "colour", "breeze"] },
        { moduleId: "appearance", category: Whatevr.I18n.i18nc("@title settings category", "Appearance"),
          rowId: "appearance.messageFontSizeExact", label: Whatevr.I18n.i18nc("@label:spinbox", "Exact message font size"),
          description: "",
          keywords: ["font", "text", "size", "exact", "point", "pixel", "zoom"] },

        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.enterToSend", label: Whatevr.I18n.i18nc("@label", "Press Enter to send"),
          description: Whatevr.I18n.i18nc("@info", "Enter sends the message; Shift+Enter inserts a new line."),
          keywords: ["enter", "send", "return", "newline", "ctrl", "shift"] },
        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.persistDrafts", label: Whatevr.I18n.i18nc("@label", "Save unsent drafts"),
          description: Whatevr.I18n.i18nc("@info", "Keep half-written messages when the app is closed and reopened."),
          keywords: ["draft", "drafts", "unsent", "restore", "persist"] },
        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.wallpaper", label: Whatevr.I18n.i18nc("@label", "Chat wallpaper"),
          description: Whatevr.I18n.i18nc("@info", "Background shown behind conversations."),
          keywords: ["wallpaper", "background", "chat", "conversation"] },
        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.wallpaperPattern", label: Whatevr.I18n.i18nc("@label:listbox", "Doodle pattern"),
          description: "",
          keywords: ["wallpaper", "pattern", "doodle", "motif", "background"] },
        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.wallpaperCustomSvg", label: Whatevr.I18n.i18nc("@action:button", "Choose SVG…"),
          description: "",
          keywords: ["wallpaper", "svg", "custom", "image", "background"] },
        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.wallpaperScale", label: Whatevr.I18n.i18nc("@label:slider", "Pattern scale"),
          description: Whatevr.I18n.i18nc("@info", "Tile size of the motif."),
          keywords: ["wallpaper", "scale", "size", "tile", "pattern"] },
        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.wallpaperOpacity", label: Whatevr.I18n.i18nc("@label:slider", "Pattern opacity"),
          description: Whatevr.I18n.i18nc("@info", "How prominent the motif is. Lower is subtler."),
          keywords: ["wallpaper", "opacity", "transparency", "prominent", "pattern"] },
        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.wallpaperTintAuto", label: Whatevr.I18n.i18nc("@option:check", "Adapt colour to background"),
          description: "",
          keywords: ["wallpaper", "tint", "colour", "color", "adapt", "auto", "pattern"] },
        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.wallpaperTintColor", label: Whatevr.I18n.i18nc("@label", "Pattern colour"),
          description: "",
          keywords: ["wallpaper", "tint", "colour", "color", "pattern"] },
        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.autoDownloadPhotos", label: Whatevr.I18n.i18nc("@label", "Auto-download photos"),
          description: Whatevr.I18n.i18nc("@info", "Download incoming photos automatically when they scroll into view."),
          keywords: ["media", "auto", "download", "photos", "images", "pictures"] },
        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.autoDownloadVideos", label: Whatevr.I18n.i18nc("@label", "Auto-download videos"),
          description: "",
          keywords: ["media", "auto", "download", "videos", "video", "mp4"] },
        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.autoDownloadAudio", label: Whatevr.I18n.i18nc("@label", "Auto-download voice and audio"),
          description: "",
          keywords: ["media", "auto", "download", "audio", "voice", "ptt", "music"] },
        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.autoDownloadDocuments", label: Whatevr.I18n.i18nc("@label", "Auto-download documents"),
          description: "",
          keywords: ["media", "auto", "download", "documents", "pdf", "files"] },
        { moduleId: "chats", category: Whatevr.I18n.i18nc("@title settings category", "Chats"),
          rowId: "chats.autoDownloadStickers", label: Whatevr.I18n.i18nc("@label", "Auto-download stickers"),
          description: "",
          keywords: ["media", "auto", "download", "stickers", "sticker"] },

        { moduleId: "notifications", category: Whatevr.I18n.i18nc("@title settings category", "Notifications"),
          rowId: "notifications.enabled", label: Whatevr.I18n.i18nc("@label", "Show notifications"),
          description: Whatevr.I18n.i18nc("@info", "Notify me about new messages while the window is in the background."),
          keywords: ["notification", "notify", "alert", "popup", "banner"] },
        { moduleId: "notifications", category: Whatevr.I18n.i18nc("@title settings category", "Notifications"),
          rowId: "notifications.preview", label: Whatevr.I18n.i18nc("@label", "Show message preview"),
          description: Whatevr.I18n.i18nc("@info", "Include the message text in the notification."),
          keywords: ["notification", "preview", "text", "privacy", "content"] },
        { moduleId: "notifications", category: Whatevr.I18n.i18nc("@title settings category", "Notifications"),
          rowId: "notifications.sound", label: Whatevr.I18n.i18nc("@label", "Play a sound"),
          description: "",
          keywords: ["notification", "sound", "audio", "beep"] },

        { moduleId: "privacy", category: Whatevr.I18n.i18nc("@title settings category", "Privacy"),
          rowId: "privacy.lastSeen", label: Whatevr.I18n.i18nc("@label", "Last seen"),
          description: Whatevr.I18n.i18nc("@info", "Who can see when you were last online."),
          keywords: ["privacy", "last seen", "online", "presence"] },
        { moduleId: "privacy", category: Whatevr.I18n.i18nc("@title settings category", "Privacy"),
          rowId: "privacy.online", label: Whatevr.I18n.i18nc("@label", "Online"),
          description: Whatevr.I18n.i18nc("@info", "Who can see when you are online."),
          keywords: ["privacy", "online", "presence"] },
        { moduleId: "privacy", category: Whatevr.I18n.i18nc("@title settings category", "Privacy"),
          rowId: "privacy.profilePhoto", label: Whatevr.I18n.i18nc("@label", "Profile photo"),
          description: Whatevr.I18n.i18nc("@info", "Who can see your profile photo."),
          keywords: ["privacy", "profile", "photo", "picture", "avatar"] },
        { moduleId: "privacy", category: Whatevr.I18n.i18nc("@title settings category", "Privacy"),
          rowId: "privacy.about", label: Whatevr.I18n.i18nc("@label", "About"),
          description: Whatevr.I18n.i18nc("@info", "Who can see your About text."),
          keywords: ["privacy", "about", "status"] },
        { moduleId: "privacy", category: Whatevr.I18n.i18nc("@title settings category", "Privacy"),
          rowId: "privacy.readReceipts", label: Whatevr.I18n.i18nc("@label", "Read receipts"),
          description: Whatevr.I18n.i18nc("@info", "Send and receive blue ticks for read messages."),
          keywords: ["privacy", "read", "receipts", "ticks", "blue"] },
        { moduleId: "privacy", category: Whatevr.I18n.i18nc("@title settings category", "Privacy"),
          rowId: "privacy.groupAdd", label: Whatevr.I18n.i18nc("@label", "Who can add me to groups"),
          description: "",
          keywords: ["privacy", "groups", "add", "invite"] },
        { moduleId: "privacy", category: Whatevr.I18n.i18nc("@title settings category", "Privacy"),
          rowId: "privacy.callAdd", label: Whatevr.I18n.i18nc("@label", "Who can call me"),
          description: "",
          keywords: ["privacy", "calls", "call"] },
        { moduleId: "privacy", category: Whatevr.I18n.i18nc("@title settings category", "Privacy"),
          rowId: "privacy.blocked", label: Whatevr.I18n.i18nc("@label", "Blocked contacts"),
          description: Whatevr.I18n.i18nc("@info", "Manage the contacts you have blocked."),
          keywords: ["privacy", "blocked", "block", "unblock", "contacts"] },

        { moduleId: "account", category: Whatevr.I18n.i18nc("@title settings category", "Profile & Account"),
          rowId: "profile.about", label: Whatevr.I18n.i18nc("@label", "About"),
          description: Whatevr.I18n.i18nc("@info", "Your WhatsApp status / About text."),
          keywords: ["profile", "about", "status", "bio"] },
        { moduleId: "account", category: Whatevr.I18n.i18nc("@title settings category", "Profile & Account"),
          rowId: "profile.name", label: Whatevr.I18n.i18nc("@label", "Name"),
          description: "",
          keywords: ["profile", "name", "display"] },
        { moduleId: "account", category: Whatevr.I18n.i18nc("@title settings category", "Profile & Account"),
          rowId: "profile.phone", label: Whatevr.I18n.i18nc("@label", "Phone number"),
          description: "",
          keywords: ["profile", "phone", "number", "msisdn"] },
        { moduleId: "account", category: Whatevr.I18n.i18nc("@title settings category", "Profile & Account"),
          rowId: "account.status", label: Whatevr.I18n.i18nc("@label", "Status"),
          description: "",
          keywords: ["account", "status", "connected", "login", "signed", "disconnect"] },
        { moduleId: "account", category: Whatevr.I18n.i18nc("@title settings category", "Profile & Account"),
          rowId: "account.logout", label: Whatevr.I18n.i18nc("@label", "Log out"),
          description: Whatevr.I18n.i18nc("@info", "Disconnect this device from your WhatsApp account."),
          keywords: ["logout", "log out", "sign out", "disconnect"] },

        { moduleId: "window", category: Whatevr.I18n.i18nc("@title settings category", "Window & Layout"),
          rowId: "window.rememberGeometry", label: Whatevr.I18n.i18nc("@label", "Remember window size and position"),
          description: "",
          keywords: ["window", "geometry", "size", "position", "remember"] },
        { moduleId: "window", category: Whatevr.I18n.i18nc("@title settings category", "Window & Layout"),
          rowId: "window.rememberColumnWidth", label: Whatevr.I18n.i18nc("@label", "Remember chat list width"),
          description: Whatevr.I18n.i18nc("@info", "Restore the width you set for the chat list column."),
          keywords: ["column", "width", "chat list", "sidebar", "remember"] },

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
            moduleId: "chats"
            text: Whatevr.I18n.i18nc("@title settings category", "Chats")
            icon.name: "dialog-messages-symbolic"
            page: () => Qt.createComponent("Whatevr", "ChatsPage")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "notifications"
            text: Whatevr.I18n.i18nc("@title settings category", "Notifications")
            icon.name: "preferences-desktop-notification-symbolic"
            page: () => Qt.createComponent("Whatevr", "NotificationsPage")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "privacy"
            text: Whatevr.I18n.i18nc("@title settings category", "Privacy")
            icon.name: "preferences-security-symbolic"
            page: () => Qt.createComponent("Whatevr", "PrivacyPage")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "account"
            text: Whatevr.I18n.i18nc("@title settings category", "Profile & Account")
            icon.name: "preferences-desktop-user-symbolic"
            page: () => Qt.createComponent("Whatevr", "ProfilePage")
        },
        KirigamiSettings.ConfigurationModule {
            moduleId: "window"
            text: Whatevr.I18n.i18nc("@title settings category", "Window & Layout")
            icon.name: "preferences-system-windows-symbolic"
            page: () => Qt.createComponent("Whatevr", "WindowLayoutPage")
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
            page: () => Qt.createComponent("Whatevr", "AboutPage")
        }
    ]
}
