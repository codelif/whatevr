pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Drill-in page (pushed onto pageStack.layers) listing starred messages, either
// across all chats (chatId == "") or scoped to one. Each row shows who sent the
// message and, in the global view, which chat it belongs to, with a "Show in
// chat" affordance that jumps to the message in its conversation.
Kirigami.ScrollablePage {
    id: root

    // Set by the caller; "" = global. The model is loaded by the caller before
    // pushing this page (AppController.loadStarredMessages(chatId)).
    property string chatId: ""
    property string headerTitle: Whatevr.I18n.i18nc("@title", "Starred messages")
    // Hide the per-chat label when the list is already scoped to one chat.
    readonly property bool showChatName: chatId.length === 0

    title: headerTitle
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    function showInChat(targetChatId, messageId) {
        Whatevr.ProtocolController.showMessageInChat(targetChatId, messageId)
        Whatevr.AppController.selectChat(targetChatId)
        applicationWindow().pageStack.layers.pop()
        applicationWindow().showConversation()
    }

    function initialsForName(name) {
        const parts = name.trim().split(/\s+/)
        let initials = ""
        for (const part of parts) {
            if (part.length > 0) {
                initials += part[0].toUpperCase()
            }
            if (initials.length >= 2) {
                break
            }
        }
        return initials.length > 0 ? initials : "?"
    }

    ListView {
        id: starredList

        model: Whatevr.AppController.starredMessagesModel
        currentIndex: -1
        reuseItems: true

        // On layer pop the ListView is destroyed with its delegates still bound to
        // the shared model; detach first so there are no live delegates to cancel
        // (avoids "DelegateModel::cancel: index out range" and the cascade of
        // ScrollablePage flickable-null relayout warnings during teardown).
        Component.onDestruction: starredList.model = null

        Kirigami.PlaceholderMessage {
            anchors.centerIn: parent
            width: parent.width - Kirigami.Units.gridUnit * 4
            visible: starredList.count === 0
            icon.name: "starred-symbolic"
            text: Whatevr.I18n.i18nc("@info placeholder for the starred-messages list", "No starred messages")
            explanation: Whatevr.I18n.i18nc("@info:placeholder", "Star a message to keep it here for quick access.")
        }

        delegate: QQC2.ItemDelegate {
            id: starredDelegate

            required property string messageId
            required property string chatId
            required property string chatName
            required property string senderName
            required property string preview
            required property string timeText
            required property bool isOutgoing

            width: ListView.view.width
            // Two-line rows (header + preview) plus the chat-name line in the
            // global view; let the content drive the height.
            contentItem: RowLayout {
                spacing: Kirigami.Units.largeSpacing

                AvatarImage {
                    Layout.alignment: Qt.AlignTop
                    Layout.preferredWidth: Kirigami.Units.gridUnit * 2
                    Layout.preferredHeight: Kirigami.Units.gridUnit * 2
                    initials: root.initialsForName(starredDelegate.senderName)
                    backgroundColor: Qt.alpha(Kirigami.Theme.highlightColor, 0.18)
                }

                ColumnLayout {
                    Layout.fillWidth: true
                    spacing: Kirigami.Units.smallSpacing / 2

                    RowLayout {
                        Layout.fillWidth: true
                        spacing: Kirigami.Units.smallSpacing

                        QQC2.Label {
                            Layout.fillWidth: true
                            text: root.showChatName && starredDelegate.chatName.length > 0
                                  ? Whatevr.I18n.i18nc("@item starred message: sender in chat",
                                                       "%1 · %2", starredDelegate.senderName, starredDelegate.chatName)
                                  : starredDelegate.senderName
                            elide: Text.ElideRight
                            font.weight: Font.DemiBold
                        }

                        QQC2.Label {
                            text: starredDelegate.timeText
                            color: Kirigami.Theme.disabledTextColor
                            font: Kirigami.Theme.smallFont
                        }
                    }

                    QQC2.Label {
                        Layout.fillWidth: true
                        text: starredDelegate.preview
                        elide: Text.ElideRight
                        maximumLineCount: 2
                        wrapMode: Text.Wrap
                        color: Kirigami.Theme.textColor
                        opacity: 0.85
                    }
                }

                QQC2.ToolButton {
                    Layout.alignment: Qt.AlignVCenter
                    icon.name: "go-jump-symbolic"
                    display: QQC2.AbstractButton.IconOnly
                    text: Whatevr.I18n.i18nc("@action:button jump to the message in its conversation", "Show in chat")
                    QQC2.ToolTip.visible: hovered
                    QQC2.ToolTip.text: text
                    onClicked: root.showInChat(starredDelegate.chatId, starredDelegate.messageId)
                }
            }

            onClicked: root.showInChat(chatId, messageId)
        }
    }
}
