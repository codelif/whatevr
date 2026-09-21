pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Kirigami.Page {
    id: root

    signal closeChatRequested()
    readonly property Item conversationPane: conversation
    property int workspaceIndex: 0
    property string workspaceName: "conversation"
    property string statusSenderId: ""
    property string statusSenderName: ""
    property string channelId: ""
    property string channelName: ""

    padding: 0
    title: stack.currentIndex === 0
        ? (Whatevr.ProtocolController.hasSelectedChat ? Whatevr.ProtocolController.selectedChatName : "")
        : (stack.currentItem && stack.currentItem.item ? (stack.currentItem.item.title || "") : "")

    // Lazy secondary pages: only the visible index instantiates its page,
    // so hidden tabs hold no daemon subscriptions and cannot thrash the
    // models while the user works elsewhere. Conversation stays eager.
    StackLayout {
        id: stack
        anchors.fill: parent
        currentIndex: root.workspaceIndex

        ConversationPane {
            id: conversation
            onCloseChatRequested: root.closeChatRequested()
        }
        Loader {
            active: root.workspaceIndex === 1
            sourceComponent: CallsPage {}
        }
        Loader {
            active: root.workspaceIndex === 2
            sourceComponent: LogsPage {}
        }
        Loader {
            active: root.workspaceIndex === 3
            sourceComponent: StarredMessagesPage {
                chatId: ""
                headerTitle: Whatevr.I18n.i18nc("@title", "Starred messages")
            }
        }
        Loader {
            active: root.workspaceIndex === 4 && root.statusSenderId.length > 0
            sourceComponent: StatusViewerPage {
                senderId: root.statusSenderId
                senderName: root.statusSenderName
            }
        }
        Loader {
            active: root.workspaceIndex === 5 && root.channelId.length > 0
            sourceComponent: ChannelMessagesPage {
                channelJid: root.channelId
                channelName: root.channelName
            }
        }
    }

    function openConversation() {
        root.workspaceIndex = 0
        root.workspaceName = "conversation"
    }
    function openTab(name) {
        const indexes = {calls: 1, logs: 2, starred: 3}
        if (indexes[name] !== undefined) {
            root.workspaceIndex = indexes[name]
            root.workspaceName = name
        }
    }

    function openStatusViewer(senderId, senderName) {
        root.statusSenderId = senderId
        root.statusSenderName = senderName
        root.workspaceIndex = 4
        root.workspaceName = "status-viewer"
    }

    function openChannelMessages(channelId, channelName) {
        root.channelId = channelId
        root.channelName = channelName
        root.workspaceIndex = 5
        root.workspaceName = "channel-messages"
    }
}
