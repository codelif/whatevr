import QtQuick
import Qt.labs.platform as Platform
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr
import "../components/Wallpapers.js" as Wallpapers

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Chats")

    function syncPreferenceBindings() {
        photosSwitch.checked = Qt.binding(() => Whatevr.ProtocolController.appPreferences.auto_download_photos ?? false)
        videosSwitch.checked = Qt.binding(() => Whatevr.ProtocolController.appPreferences.auto_download_videos ?? false)
        audioSwitch.checked = Qt.binding(() => Whatevr.ProtocolController.appPreferences.auto_download_audio ?? false)
        documentsSwitch.checked = Qt.binding(() => Whatevr.ProtocolController.appPreferences.auto_download_documents ?? false)
        stickersSwitch.checked = Qt.binding(() => Whatevr.ProtocolController.appPreferences.auto_download_stickers ?? false)
    }

    Connections {
        target: Whatevr.ProtocolController
        function onAppPreferencesChanged() { page.syncPreferenceBindings() }
        function onSettingsActionFailed() { page.syncPreferenceBindings() }
    }

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

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            objectName: "chats.snapToBottomOnSend"
            text: Whatevr.I18n.i18nc("@option:check", "Jump to the newest message when sending")
            description: Whatevr.I18n.i18nc("@info", "Scroll the conversation down to your message even if you were reading further up.")
            checked: Whatevr.Settings.snapToBottomOnSend
            onToggled: Whatevr.Settings.snapToBottomOnSend = checked
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
            id: photosSwitch
            objectName: "chats.autoDownloadPhotos"
            text: Whatevr.I18n.i18nc("@option:check", "Photos")
            description: Whatevr.I18n.i18nc("@info", "Download incoming photos automatically.")
            checked: Whatevr.ProtocolController.appPreferences.auto_download_photos ?? false
            onToggled: Whatevr.ProtocolController.setAppPreference("auto_download_photos", checked)
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            id: videosSwitch
            objectName: "chats.autoDownloadVideos"
            text: Whatevr.I18n.i18nc("@option:check", "Videos")
            checked: Whatevr.ProtocolController.appPreferences.auto_download_videos ?? false
            onToggled: Whatevr.ProtocolController.setAppPreference("auto_download_videos", checked)
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            id: audioSwitch
            objectName: "chats.autoDownloadAudio"
            text: Whatevr.I18n.i18nc("@option:check", "Voice messages and audio")
            checked: Whatevr.ProtocolController.appPreferences.auto_download_audio ?? false
            onToggled: Whatevr.ProtocolController.setAppPreference("auto_download_audio", checked)
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            id: documentsSwitch
            objectName: "chats.autoDownloadDocuments"
            text: Whatevr.I18n.i18nc("@option:check", "Documents")
            checked: Whatevr.ProtocolController.appPreferences.auto_download_documents ?? false
            onToggled: Whatevr.ProtocolController.setAppPreference("auto_download_documents", checked)
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            id: stickersSwitch
            objectName: "chats.autoDownloadStickers"
            text: Whatevr.I18n.i18nc("@option:check", "Stickers")
            checked: Whatevr.ProtocolController.appPreferences.auto_download_stickers ?? false
            onToggled: Whatevr.ProtocolController.setAppPreference("auto_download_stickers", checked)
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormComboBoxDelegate {
            id: sizeLimitCombo
            objectName: "chats.autoDownloadSizeLimit"
            text: Whatevr.I18n.i18nc("@label:listbox", "Size limit")
            description: Whatevr.I18n.i18nc("@info",
                "Nothing larger downloads by itself, whatever the switches above say.")
            textRole: "label"
            valueRole: "bytes"
            model: [
                { label: Whatevr.I18n.i18nc("@item:inlistbox", "2 MiB"), bytes: 2 * 1024 * 1024 },
                { label: Whatevr.I18n.i18nc("@item:inlistbox", "8 MiB"), bytes: 8 * 1024 * 1024 },
                { label: Whatevr.I18n.i18nc("@item:inlistbox", "16 MiB"), bytes: 16 * 1024 * 1024 },
                { label: Whatevr.I18n.i18nc("@item:inlistbox", "64 MiB"), bytes: 64 * 1024 * 1024 },
                { label: Whatevr.I18n.i18nc("@item:inlistbox", "No limit"), bytes: 0 }
            ]
            currentIndex: {
                const configured = Whatevr.ProtocolController.appPreferences.auto_download_max_bytes
                    ?? (16 * 1024 * 1024)
                for (let i = 0; i < model.length; ++i) {
                    if (model[i].bytes === configured)
                        return i
                }
                return 2
            }
            onActivated: index => Whatevr.ProtocolController.setAutoDownloadLimit(model[index].bytes)
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Media playback")
    }

    FormCard.FormCard {
        FormCard.FormSwitchDelegate {
            id: autoplaySwitch
            objectName: "chats.autoplayInlineMedia"
            text: Whatevr.I18n.i18nc("@option:check", "Autoplay GIFs and video messages")
            description: Whatevr.I18n.i18nc("@info",
                "Off shows a thumbnail with a play button instead, which uses less power.")
            checked: Whatevr.Settings.autoplayInlineMedia
            onToggled: Whatevr.Settings.autoplayInlineMedia = checked
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            id: loopGifsSwitch
            objectName: "chats.loopGifs"
            text: Whatevr.I18n.i18nc("@option:check", "Loop GIFs")
            checked: Whatevr.Settings.loopGifs
            onToggled: Whatevr.Settings.loopGifs = checked
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            id: advanceVoiceSwitch
            objectName: "chats.advanceVoiceMessages"
            text: Whatevr.I18n.i18nc("@option:check", "Continue to the next voice message")
            checked: Whatevr.Settings.advanceVoiceMessages
            onToggled: Whatevr.Settings.advanceVoiceMessages = checked
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormSwitchDelegate {
            id: rememberPositionSwitch
            objectName: "chats.rememberPlaybackPosition"
            text: Whatevr.I18n.i18nc("@option:check", "Remember where playback stopped")
            description: Whatevr.I18n.i18nc("@info",
                "A part-heard voice message picks up where you left it instead of at the start.")
            checked: Whatevr.Settings.rememberPlaybackPosition
            onToggled: Whatevr.Settings.rememberPlaybackPosition = checked
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormComboBoxDelegate {
            id: playbackSpeedCombo
            objectName: "chats.defaultPlaybackSpeed"
            text: Whatevr.I18n.i18nc("@label:listbox", "Voice message speed")
            textRole: "label"
            valueRole: "speed"
            model: [
                { label: Whatevr.I18n.i18nc("@item:inlistbox playback speed", "1x"), speed: 1.0 },
                { label: Whatevr.I18n.i18nc("@item:inlistbox playback speed", "1.5x"), speed: 1.5 },
                { label: Whatevr.I18n.i18nc("@item:inlistbox playback speed", "2x"), speed: 2.0 }
            ]
            currentIndex: {
                const speed = Whatevr.Settings.defaultPlaybackSpeed
                if (speed === 1.5)
                    return 1
                if (speed === 2.0)
                    return 2
                return 0
            }
            onActivated: index => Whatevr.Settings.defaultPlaybackSpeed = model[index].speed
        }

        FormCard.FormDelegateSeparator {}

        FormCard.FormButtonDelegate {
            id: saveFolderButton
            objectName: "chats.mediaSaveDirectory"
            text: Whatevr.I18n.i18nc("@action:button", "Save media to")
            description: Whatevr.Settings.mediaSaveDirectory.length > 0
                ? Whatevr.Settings.mediaSaveDirectory
                : Whatevr.I18n.i18nc("@info", "Ask each time, starting in the folder for that kind of file.")
            onClicked: mediaFolderDialog.open()
        }
    }

    Platform.FolderDialog {
        id: mediaFolderDialog

        title: Whatevr.I18n.i18nc("@title:window", "Choose where media is saved")
        onAccepted: Whatevr.Settings.mediaSaveDirectory = folder.toString()
    }
}
