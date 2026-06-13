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
    required property string jid
    required property bool registered

    readonly property bool isMessage: kind === "message"
    readonly property bool isNumber: kind === "number"
    readonly property string query: Whatevr.AppController.searchQuery
    readonly property color highlightBg: Kirigami.Theme.highlightColor
    readonly property color highlightFg: Kirigami.Theme.highlightedTextColor

    signal chatActivated(string chatId)
    signal messageActivated(string chatId, string messageId)
    signal numberActivated(string jid)

    width: ListView.view ? ListView.view.width : implicitWidth
    padding: Kirigami.Units.largeSpacing
    highlighted: false
    // A number that isn't on WhatsApp can't be messaged.
    enabled: !root.isNumber || root.registered

    onClicked: {
        if (root.isNumber) {
            if (root.registered) {
                root.numberActivated(root.jid)
            }
        } else if (root.isMessage) {
            root.messageActivated(root.chatId, root.messageId)
        } else {
            root.chatActivated(root.chatId)
        }
    }

    contentItem: RowLayout {
        spacing: Kirigami.Units.largeSpacing

        AvatarImage {
            Layout.alignment: Qt.AlignVCenter
            visible: !root.isNumber
            avatarLocalPath: root.avatarLocalPath
            initials: root.initials
        }

        Kirigami.Icon {
            visible: root.isNumber
            Layout.alignment: Qt.AlignVCenter
            Layout.preferredWidth: Kirigami.Units.iconSizes.medium
            Layout.preferredHeight: Kirigami.Units.iconSizes.medium
            source: root.registered ? "contact-new-symbolic" : "dialog-cancel-symbolic"
            color: root.registered ? Kirigami.Theme.highlightColor : Kirigami.Theme.disabledTextColor
        }

        ColumnLayout {
            Layout.fillWidth: true
            spacing: 0

            RowLayout {
                Layout.fillWidth: true
                spacing: Kirigami.Units.smallSpacing

                Label {
                    Layout.fillWidth: true
                    // Number rows: "Message <name/number>". Chat rows highlight the
                    // matched name; message rows show the chat name plainly (the
                    // match is in the snippet below).
                    text: {
                        if (root.isNumber) {
                            return root.registered
                                ? Whatevr.I18n.i18nc("@action:button start a chat with a number", "Message %1", root.title)
                                : root.title
                        }
                        return root.isMessage
                            ? root.title
                            : Highlight.highlight(root.title, root.query, root.highlightBg, root.highlightFg)
                    }
                    textFormat: (root.isMessage || root.isNumber) ? Text.PlainText : Text.RichText
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
                // Number rows: the phone number, or an "on WhatsApp" status.
                // Message rows: a highlighted snippet (prefixed with the sender).
                // Chat rows: the last-message preview, plain.
                text: {
                    if (root.isNumber) {
                        return root.registered
                            ? root.subtitle
                            : Whatevr.I18n.i18nc("@info phone number not registered", "Not on WhatsApp")
                    }
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
