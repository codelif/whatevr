pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

import "SearchHighlight.js" as Highlight

// One row of the unified search results list. Renders a chat-name match or a
// message-text match depending on `kind`, highlighting the query inside the
// name (chat rows) or a windowed snippet (message rows).
ItemDelegate {
    id: root

    required property string kind
    required property string avatarLocalPath
    required property string initials
    required property string title
    required property string subtitle
    required property string chatId
    required property string messageId
    required property string senderName
    required property string timeText
    required property bool isOutgoing

    readonly property bool isMessage: kind === "message"
    readonly property string query: Whatevr.AppController.searchQuery
    readonly property color highlightBg: Kirigami.Theme.highlightColor
    readonly property color highlightFg: Kirigami.Theme.highlightedTextColor

    signal chatActivated(string chatId)
    signal messageActivated(string chatId, string messageId)

    width: ListView.view ? ListView.view.width : implicitWidth
    padding: Kirigami.Units.largeSpacing
    highlighted: false

    onClicked: {
        if (root.isMessage) {
            root.messageActivated(root.chatId, root.messageId)
        } else {
            root.chatActivated(root.chatId)
        }
    }

    contentItem: RowLayout {
        spacing: Kirigami.Units.largeSpacing

        AvatarImage {
            Layout.alignment: Qt.AlignVCenter
            avatarLocalPath: root.avatarLocalPath
            initials: root.initials
        }

        ColumnLayout {
            Layout.fillWidth: true
            spacing: 0

            RowLayout {
                Layout.fillWidth: true
                spacing: Kirigami.Units.smallSpacing

                Label {
                    Layout.fillWidth: true
                    // Chat rows highlight the matched name; message rows show the
                    // chat name plainly (the match is in the snippet below).
                    text: root.isMessage
                          ? root.title
                          : Highlight.highlight(root.title, root.query, root.highlightBg, root.highlightFg)
                    textFormat: root.isMessage ? Text.PlainText : Text.RichText
                    font.weight: Font.DemiBold
                    elide: Text.ElideRight
                    maximumLineCount: 1
                }

                Label {
                    visible: root.isMessage && root.timeText.length > 0
                    text: root.timeText
                    color: Kirigami.Theme.disabledTextColor
                    font: Kirigami.Theme.smallFont
                }
            }

            Label {
                Layout.fillWidth: true
                visible: text.length > 0
                // Message rows: a highlighted snippet (prefixed with the sender).
                // Chat rows: the last-message preview, plain.
                text: {
                    if (!root.isMessage) {
                        return root.subtitle
                    }
                    const snippet = Highlight.snippet(root.subtitle, root.query, root.highlightBg, root.highlightFg, 40)
                    if (root.senderName.length > 0) {
                        return Highlight.escapeHtml(root.senderName + ": ") + snippet
                    }
                    return snippet
                }
                textFormat: root.isMessage ? Text.RichText : Text.PlainText
                color: Kirigami.Theme.disabledTextColor
                font: Kirigami.Theme.smallFont
                elide: Text.ElideRight
                maximumLineCount: 1
                wrapMode: Text.NoWrap
            }
        }
    }
}
