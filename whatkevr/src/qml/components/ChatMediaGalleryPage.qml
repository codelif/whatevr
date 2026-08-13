pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts

import org.kde.kirigami as Kirigami

import Whatevr as Whatevr

// Every photo, video, voice note and document in one chat, newest first.
//
// The rows are the same `messages` items the conversation renders, delivered
// through the `chat_media` view, so a thumbnail here is the same file the
// bubble shows and a download landing updates both at once.
Kirigami.ScrollablePage {
    id: root

    required property string chatId
    property string chatName: ""

    title: chatName.length > 0
        ? Whatevr.I18n.i18nc("@title:window", "Media in %1", chatName)
        : Whatevr.I18n.i18nc("@title:window", "Media")

    Component.onCompleted: Whatevr.ProtocolController.openChatMedia(chatId)
    Component.onDestruction: Whatevr.ProtocolController.closeChatMedia()

    GridView {
        id: grid

        readonly property int columns: Math.max(2, Math.floor(width / (Kirigami.Units.gridUnit * 7)))

        model: Whatevr.ProtocolController.chatMediaModel
        cellWidth: Math.floor(width / columns)
        cellHeight: cellWidth
        cacheBuffer: cellHeight * 2

        // The window grows into older media as the grid nears its end, the
        // same "extend older" a live-edge window always uses.
        onContentYChanged: {
            if (contentHeight <= 0 || Whatevr.ProtocolController.chatMediaExhausted)
                return
            if (contentY + height > contentHeight - cellHeight * 2)
                Whatevr.ProtocolController.extendChatMedia(60)
        }

        delegate: Item {
            id: cell

            required property var model

            readonly property var item: model.item ?? ({})
            readonly property var media: item.media ?? ({})
            readonly property string kind: item.kind ?? ""
            readonly property string thumbnailPath: media.thumbnail_path ?? ""
            readonly property string localPath: media.path ?? ""
            readonly property bool isVisual: kind === "image" || kind === "video" || kind === "gif" || kind === "video_note"

            width: grid.cellWidth
            height: grid.cellHeight

            Rectangle {
                anchors.fill: parent
                anchors.margins: 1
                color: Qt.alpha(Kirigami.Theme.textColor, 0.06)

                Image {
                    anchors.fill: parent
                    visible: cell.isVisual && source.toString().length > 0
                    source: {
                        if (cell.localPath.length > 0 && cell.kind === "image")
                            return Qt.resolvedUrl("file://" + cell.localPath)
                        if (cell.thumbnailPath.length > 0)
                            return Qt.resolvedUrl("file://" + cell.thumbnailPath)
                        return ""
                    }
                    fillMode: Image.PreserveAspectCrop
                    asynchronous: true
                    cache: true
                    sourceSize.width: grid.cellWidth
                    sourceSize.height: grid.cellHeight
                }

                // Non-visual kinds (voice notes, audio, documents) get a glyph
                // and a label rather than a blank tile.
                ColumnLayout {
                    anchors.centerIn: parent
                    width: parent.width - Kirigami.Units.smallSpacing * 2
                    visible: !cell.isVisual
                    spacing: Kirigami.Units.smallSpacing

                    Kirigami.Icon {
                        Layout.alignment: Qt.AlignHCenter
                        implicitWidth: Kirigami.Units.iconSizes.large
                        implicitHeight: Kirigami.Units.iconSizes.large
                        source: {
                            if (cell.kind === "voice")
                                return "audio-input-microphone-symbolic"
                            if (cell.kind === "audio")
                                return "audio-x-generic"
                            return "text-x-generic"
                        }
                    }

                    QQC2.Label {
                        Layout.fillWidth: true
                        text: cell.item.fallback ?? ""
                        horizontalAlignment: Text.AlignHCenter
                        elide: Text.ElideRight
                        maximumLineCount: 2
                        wrapMode: Text.Wrap
                        font.pointSize: Kirigami.Theme.smallFont.pointSize
                    }
                }

                Kirigami.Icon {
                    anchors.centerIn: parent
                    visible: cell.kind === "video" || cell.kind === "gif" || cell.kind === "video_note"
                    width: Kirigami.Units.iconSizes.medium
                    height: width
                    source: "media-playback-start-symbolic"
                    color: "white"
                }

                TapHandler {
                    onTapped: {
                        if (cell.localPath.length === 0) {
                            Whatevr.ProtocolController.downloadMessageMedia(cell.item.id ?? "")
                            return
                        }
                        if (cell.kind === "image") {
                            galleryViewer.showImage(cell.localPath)
                        } else if (cell.isVisual) {
                            galleryViewer.showVideo(cell.item.id ?? "", cell.localPath, "", cell.kind, cell.media.duration_secs ?? 0)
                        } else {
                            Whatevr.ProtocolController.openLocalFile(cell.localPath)
                        }
                    }
                }
            }
        }
    }

    MediaViewer {
        id: galleryViewer
    }

    QQC2.BusyIndicator {
        anchors.centerIn: parent
        running: Whatevr.ProtocolController.chatMediaLoading
        visible: running
    }

    Kirigami.PlaceholderMessage {
        anchors.centerIn: parent
        width: parent.width - Kirigami.Units.gridUnit * 4
        visible: !Whatevr.ProtocolController.chatMediaLoading && grid.count === 0
        icon.name: "folder-images-symbolic"
        text: Whatevr.I18n.i18nc("@info:placeholder", "No media in this chat yet")
    }
}
