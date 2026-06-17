import QtQuick
import Qt.labs.platform as Platform
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr
import "../components/Wallpapers.js" as Wallpapers

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Chats")

    Component.onCompleted: Whatevr.AppController.refreshAppPreferences()

    // "#rrggbb" for a QML color, so the tint persists as a plain hex string.
    function colorToHex(c) {
        function h(x) { return ("0" + Math.round(x * 255).toString(16)).slice(-2); }
        return "#" + h(c.r) + h(c.g) + h(c.b);
    }

    // Doodle-pattern options for the combo below the colour presets.
    readonly property var patternOptions: [
        { text: Whatevr.I18n.i18nc("@item:inlistbox no chat pattern", "None"), value: "" },
        { text: Whatevr.I18n.i18nc("@item:inlistbox built-in doodle pattern", "Doodle"), value: "doodle" },
        { text: Whatevr.I18n.i18nc("@item:inlistbox custom uploaded pattern", "Custom SVG…"), value: "custom" }
    ]

    Platform.FileDialog {
        id: patternFileDialog
        title: Whatevr.I18n.i18nc("@title:window", "Choose a pattern SVG")
        nameFilters: [Whatevr.I18n.i18nc("@item:inlistbox", "SVG images (*.svg)")]
        fileMode: Platform.FileDialog.OpenFile
        onAccepted: {
            const dest = Whatevr.Settings.importWallpaperSvg(file);
            if (dest.length > 0) {
                Whatevr.Settings.chatWallpaperPath = dest;
                Whatevr.Settings.chatWallpaperPattern = "custom";
            }
        }
    }

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
        title: Whatevr.I18n.i18nc("@title:group", "Pattern")
    }

    FormCard.FormCard {
        // A subtle doodle motif drawn over the wallpaper colour. The tint adapts
        // to the background automatically unless overridden below.
        FormCard.FormComboBoxDelegate {
            id: patternCombo
            objectName: "chats.wallpaperPattern"
            text: Whatevr.I18n.i18nc("@label:listbox", "Doodle pattern")
            description: Whatevr.I18n.i18nc("@info", "Tile a faint motif over the wallpaper. Pick the built-in doodle or upload your own SVG.")
            model: page.patternOptions
            textRole: "text"
            valueRole: "value"

            Component.onCompleted: currentIndex = indexOfValue(Whatevr.Settings.chatWallpaperPattern)

            onActivated: {
                Whatevr.Settings.chatWallpaperPattern = currentValue;
                if (currentValue === "custom" && Whatevr.Settings.chatWallpaperPath.length === 0) {
                    patternFileDialog.open();
                }
            }

            Connections {
                target: Whatevr.Settings
                function onChatWallpaperPatternChanged() {
                    patternCombo.currentIndex = patternCombo.indexOfValue(Whatevr.Settings.chatWallpaperPattern);
                }
            }
        }

        FormCard.FormDelegateSeparator { visible: customPatternButton.visible }

        FormCard.FormButtonDelegate {
            id: customPatternButton
            objectName: "chats.wallpaperCustomSvg"
            visible: Whatevr.Settings.chatWallpaperPattern === "custom"
            icon.name: "document-open"
            text: Whatevr.I18n.i18nc("@action:button", "Choose SVG…")
            description: Whatevr.Settings.chatWallpaperPath.length > 0
                ? Whatevr.Settings.chatWallpaperPath
                : Whatevr.I18n.i18nc("@info", "No file selected.")
            onClicked: patternFileDialog.open()
        }

        FormCard.FormDelegateSeparator { visible: patternCombo.currentValue !== "" }

        FormSliderDelegate {
            objectName: "chats.wallpaperScale"
            visible: patternCombo.currentValue !== ""
            label: Whatevr.I18n.i18nc("@label:slider", "Pattern scale")
            description: Whatevr.I18n.i18nc("@info", "Tile size of the motif.")
            from: 50
            to: 300
            stepSize: 5
            suffix: "%"
            value: Whatevr.Settings.chatWallpaperScale
            onMoved: Whatevr.Settings.chatWallpaperScale = value
        }

        FormCard.FormDelegateSeparator { visible: patternCombo.currentValue !== "" }

        FormSliderDelegate {
            objectName: "chats.wallpaperOpacity"
            visible: patternCombo.currentValue !== ""
            label: Whatevr.I18n.i18nc("@label:slider", "Pattern opacity")
            description: Whatevr.I18n.i18nc("@info", "How prominent the motif is. Lower is subtler.")
            from: 0
            to: 25
            stepSize: 1
            suffix: "%"
            value: Whatevr.Settings.chatWallpaperOpacity
            onMoved: Whatevr.Settings.chatWallpaperOpacity = value
        }

        FormCard.FormDelegateSeparator { visible: patternCombo.currentValue !== "" }

        FormCard.FormSwitchDelegate {
            id: autoTintSwitch
            objectName: "chats.wallpaperTintAuto"
            visible: patternCombo.currentValue !== ""
            text: Whatevr.I18n.i18nc("@option:check", "Adapt colour to background")
            description: Whatevr.I18n.i18nc("@info", "Tint the motif automatically so it stays visible on any background.")
            checked: Whatevr.Settings.chatWallpaperTint.length === 0
            onToggled: Whatevr.Settings.chatWallpaperTint = checked ? "" : "#000000"
        }

        FormCard.FormDelegateSeparator {
            visible: patternCombo.currentValue !== "" && !autoTintSwitch.checked
        }

        FormCard.FormColorDelegate {
            objectName: "chats.wallpaperTintColor"
            visible: patternCombo.currentValue !== "" && !autoTintSwitch.checked
            text: Whatevr.I18n.i18nc("@label", "Pattern colour")
            color: Whatevr.Settings.chatWallpaperTint.length > 0 ? Whatevr.Settings.chatWallpaperTint : "#000000"
            // Persist only genuine user picks: stay quiet while "auto" is on (the
            // binding above still evaluates then) so we don't clobber the empty tint.
            onColorChanged: {
                if (autoTintSwitch.checked) {
                    return;
                }
                const hex = page.colorToHex(color);
                if (hex !== Whatevr.Settings.chatWallpaperTint) {
                    Whatevr.Settings.chatWallpaperTint = hex;
                }
            }
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
