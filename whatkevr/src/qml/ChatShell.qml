import QtQuick
import org.kde.kirigami as Kirigami

Kirigami.Page {
    id: root

    readonly property bool wideLayout: width >= Kirigami.Units.gridUnit * 42

    title: i18nc("@title", "Chats")
    padding: 0

    Component {
        id: chatListPaneComponent

        ChatListPane {}
    }

    Component {
        id: conversationPaneComponent

        ConversationPane {}
    }

    Kirigami.PageRow {
        id: pageRow

        property var chatListPage: null
        property var conversationPage: null

        anchors.fill: parent
        globalToolBar.style: Kirigami.ApplicationHeaderStyle.ToolBar
        globalToolBar.showNavigationButtons: !root.wideLayout && currentIndex > 0
                                              ? Kirigami.ApplicationHeaderStyle.ShowBackButton
                                              : Kirigami.ApplicationHeaderStyle.NoNavigationButtons
        columnView.columnResizeMode: root.wideLayout ? Kirigami.ColumnView.DynamicColumns : Kirigami.ColumnView.SingleColumn

        function createChatListPage() {
            if (chatListPage) {
                return chatListPage
            }

            chatListPage = chatListPaneComponent.createObject(pageRow)
            if (!chatListPage) {
                console.warn("Failed to create chat list page")
                return null
            }

            return chatListPage
        }

        function createConversationPage() {
            if (conversationPage) {
                return conversationPage
            }

            conversationPage = conversationPaneComponent.createObject(pageRow)
            if (!conversationPage) {
                console.warn("Failed to create conversation page")
                return null
            }
            conversationPage.heavyContentEnabled = root.wideLayout

            return conversationPage
        }

        function ensureChatListPage() {
            const page = createChatListPage()
            if (page && depth === 0) {
                push(page)
            }
        }

        function ensureConversationPage() {
            const page = createConversationPage()
            if (page && depth < 2) {
                push(page)
            }
        }

        function openConversation() {
            if (root.wideLayout || !AppController.hasSelectedChat) {
                return
            }

            const page = createConversationPage()
            if (!page) {
                return
            }
            page.heavyContentEnabled = false

            if (depth < 2) {
                push(page)
            } else if (currentIndex === 0) {
                goForward()
            }

            Qt.callLater(() => {
                if (!pageRow.columnView.moving) {
                    pageRow.finishMobileMovement()
                }
            })
        }

        function closeConversation() {
            if (!root.wideLayout && currentIndex > 0) {
                goBack()
            }
        }

        function scheduleMobileConversationCleanup() {
            if (columnView.moving) {
                return
            }

            Qt.callLater(cleanupMobileConversation)
        }

        function cleanupMobileConversation() {
            if (root.wideLayout || columnView.moving || AppController.hasSelectedChat || currentIndex !== 0 || depth < 2) {
                return
            }

            pop()
            conversationPage = null
        }

        function syncPagesToLayout() {
            ensureChatListPage()

            if (root.wideLayout) {
                ensureConversationPage()
                if (conversationPage) {
                    conversationPage.heavyContentEnabled = true
                }
                return
            }

            if (AppController.hasSelectedChat) {
                ensureConversationPage()
                currentIndex = 1
                if (conversationPage) {
                    conversationPage.heavyContentEnabled = true
                }
            } else {
                currentIndex = 0
            }
        }

        function syncNavigationToSelection() {
            if (root.wideLayout) {
                ensureConversationPage()
                if (conversationPage) {
                    conversationPage.heavyContentEnabled = true
                }
            } else if (AppController.hasSelectedChat) {
                openConversation()
            }
        }

        function finishMobileMovement() {
            if (root.wideLayout) {
                if (conversationPage) {
                    conversationPage.heavyContentEnabled = true
                }
                return
            }

            if (currentIndex > 0 && AppController.hasSelectedChat && conversationPage) {
                conversationPage.heavyContentEnabled = true
            } else if (currentIndex === 0) {
                scheduleMobileConversationCleanup()
            }
        }

        onCurrentIndexChanged: {
            if (!root.wideLayout && currentIndex === 0) {
                if (AppController.hasSelectedChat) {
                    AppController.selectChat("")
                }
                scheduleMobileConversationCleanup()
            }
        }

        Connections {
            target: root

            function onWideLayoutChanged() {
                pageRow.syncPagesToLayout()
            }
        }

        Connections {
            target: pageRow.columnView

            function onMovingChanged() {
                if (!pageRow.columnView.moving) {
                    pageRow.finishMobileMovement()
                }
            }
        }

        Component.onCompleted: {
            syncPagesToLayout()
        }

        Connections {
            target: AppController

            function onSelectionChanged() {
                pageRow.syncNavigationToSelection()
            }
        }
    }
}
