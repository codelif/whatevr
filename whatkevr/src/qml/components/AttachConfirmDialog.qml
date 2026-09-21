pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Confirm-before-send for attachments: picking files (or dropping them)
// stages them here first — thumbnails, names, a caption field and a
// Send/Cancel choice — instead of sending immediately. Nothing leaves the
// device until Send is pressed.
CenteredDialog {
    id: root

    // Staged files: list of {url, name} maps. `url` is the file:// URL the
    // send path wants; `name` is the display basename.
    property var files: []
    property string forcedKind: ""
    property bool viewOnceArmed: false

    title: Whatevr.I18n.i18nc("@title:dialog confirm attachments before sending", "Send files")
    standardButtons: Kirigami.Dialog.NoButton
    padding: Kirigami.Units.largeSpacing
    preferredWidth: Kirigami.Units.gridUnit * 24
    maximumHeight: Kirigami.Units.gridUnit * 28

    signal confirmed(var fileUrls, string caption, string kind, bool viewOnce)

    function stage(urls, kind, viewOnce) {
        const staged = []
        for (let i = 0; i < urls.length; ++i) {
            const raw = String(urls[i] || "")
            if (!raw) {
                continue
            }
            let name = raw.substring(raw.lastIndexOf("/") + 1)
            try {
                name = decodeURI(name)
            } catch (e) {
            }
            staged.push({ url: raw, name: name.length > 0 ? name : raw })
        }
        root.files = staged
        root.forcedKind = kind || ""
        root.viewOnceArmed = viewOnce === true
        captionField.text = ""
        open()
    }

    function isImageName(name) {
        return /\.(png|jpe?g|gif|webp|bmp|svg)$/i.test(name)
    }

    ColumnLayout {
        implicitWidth: root.preferredWidth
        spacing: Kirigami.Units.smallSpacing

        Label {
            visible: root.files.length === 0
            text: Whatevr.I18n.i18nc("@info:placeholder no files staged", "No files selected.")
            color: Kirigami.Theme.disabledTextColor
            Layout.fillWidth: true
        }

        ListView {
            visible: root.files.length > 0
            model: root.files
            Layout.fillWidth: true
            Layout.preferredHeight: Math.min(contentHeight, Kirigami.Units.gridUnit * 12)
            clip: true
            spacing: Kirigami.Units.smallSpacing

            delegate: ItemDelegate {
                id: fileDelegate

                required property var modelData
                required property int index

                width: ListView.view.width
                hoverEnabled: false

                contentItem: RowLayout {
                    spacing: Kirigami.Units.smallSpacing

                    // Photo preview for images, generic icon otherwise.
                    Image {
                        Layout.preferredWidth: Kirigami.Units.gridUnit * 2.5
                        Layout.preferredHeight: Kirigami.Units.gridUnit * 2.5
                        source: root.isImageName(String(fileDelegate.modelData.name || ""))
                                ? fileDelegate.modelData.url : ""
                        fillMode: Image.PreserveAspectCrop
                        asynchronous: true
                        cache: true

                        Kirigami.Icon {
                            visible: !root.isImageName(String(fileDelegate.modelData.name || ""))
                            anchors.centerIn: parent
                            source: "document-open-symbolic"
                            width: Kirigami.Units.iconSizes.medium
                            height: width
                        }
                    }

                    Label {
                        text: String(fileDelegate.modelData.name || "")
                        elide: Text.ElideMiddle
                        Layout.fillWidth: true
                    }

                    ToolButton {
                        icon.name: "list-remove-symbolic"
                        text: Whatevr.I18n.i18nc("@action:button remove a staged file", "Remove")
                        display: AbstractButton.IconOnly
                        onClicked: {
                            const kept = root.files.slice()
                            kept.splice(fileDelegate.index, 1)
                            root.files = kept
                            if (kept.length === 0) {
                                root.close()
                            }
                        }

                        ToolTip.visible: hovered
                        ToolTip.text: text
                        ToolTip.delay: Kirigami.Units.toolTipDelay
                    }
                }
            }
        }

        TextField {
            id: captionField

            Layout.fillWidth: true
            placeholderText: Whatevr.I18n.i18nc("@info:placeholder caption for staged files", "Add a caption…")
        }

        RowLayout {
            Layout.fillWidth: true
            spacing: Kirigami.Units.smallSpacing

            Item {
                Layout.fillWidth: true
            }

            Button {
                text: Whatevr.I18n.i18nc("@action:button cancel sending staged files", "Cancel")
                onClicked: root.close()
            }

            Button {
                text: Whatevr.I18n.i18nc("@action:button send staged files", "Send")
                enabled: root.files.length > 0
                highlighted: true
                onClicked: {
                    const urls = root.files.map(f => f.url)
                    const caption = captionField.text
                    const kind = root.forcedKind
                    const once = root.viewOnceArmed
                    root.close()
                    root.confirmed(urls, caption, kind, once)
                }
            }
        }
    }
}
