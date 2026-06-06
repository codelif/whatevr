import QtQuick
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Kirigami.ApplicationWindow {
    id: root

    readonly property bool chatWideLayout: pageStack.width >= pageStack.defaultColumnWidth * 2
    readonly property bool chatSingleColumnLayout: !chatWideLayout
    property string currentMode: ""

    // The chat-list and conversation panes are created once on entering chat
    // mode and kept alive for the whole session. Navigation happens purely by
    // moving between columns, so opening a chat never recreates a pane and
    // nothing is ever orphaned. transientPageItem holds the single login/status
    // page when not in chat mode.
    property var chatListPageItem: null
    property var conversationPageItem: null
    property var transientPageItem: null
    property bool pendingSelectionClear: false
    // Set when a deep link (e.g. notification click) selected a chat; consumed
    // by syncChatLayout once the chat pages exist so we navigate to it.
    property bool pendingShowConversation: false

    width: 1180
    height: 760
    minimumWidth: 420
    minimumHeight: 680
    title: Whatevr.I18n.i18nc("@title:window", "Whatevr")
    visible: true

    pageStack.columnView.columnResizeMode: chatWideLayout ? Kirigami.ColumnView.FixedColumns : Kirigami.ColumnView.SingleColumn
    pageStack.globalToolBar.style: Kirigami.ApplicationHeaderStyle.ToolBar
    pageStack.globalToolBar.showNavigationButtons: currentMode === "chat"
                                                  ? (chatSingleColumnLayout && pageStack.currentIndex > 0
                                                     ? Kirigami.ApplicationHeaderStyle.ShowBackButton
                                                     : Kirigami.ApplicationHeaderStyle.NoNavigationButtons)
                                                  : Kirigami.ApplicationHeaderStyle.NoNavigationButtons

    Timer {
        id: pageStackRebuildTimer

        interval: 50
        repeat: false
        onTriggered: root.rebuildPageStack()
    }

    function appMode() {
        if (Whatevr.AppController.loginRequired) {
            return "login"
        }
        if (!Whatevr.AppController.shellVisible) {
            return "status"
        }
        return "chat"
    }

    Component {
        id: loginPageComponent

        LoginPage {}
    }

    Component {
        id: statusPageComponent

        StatusPage {}
    }

    Component {
        id: chatListPaneComponent

        ChatListPane {}
    }

    Component {
        id: conversationPaneComponent

        ConversationPane {}
    }

    function scheduleRebuildPageStack() {
        pageStackRebuildTimer.restart()
    }

    function destroyChatPages() {
        pageStack.clear()
        if (conversationPageItem) {
            conversationPageItem.destroy()
            conversationPageItem = null
        }
        if (chatListPageItem) {
            chatListPageItem.destroy()
            chatListPageItem = null
        }
    }

    function clearTransientPage() {
        if (transientPageItem) {
            transientPageItem.destroy()
            transientPageItem = null
        }
    }

    function resetToPage(mode, pageComponent) {
        destroyChatPages()
        clearTransientPage()
        pageStack.clear()
        currentMode = mode

        const page = pageComponent.createObject(pageStack)
        if (!page) {
            console.warn("Failed to create page")
            return
        }
        transientPageItem = page
        pageStack.push(page)
    }

    function ensureChatPages() {
        if (!chatListPageItem) {
            const listPage = chatListPaneComponent.createObject(pageStack)
            if (listPage) {
                chatListPageItem = listPage
                pageStack.push(listPage)
                if (listPage.chatSelected) {
                    listPage.chatSelected.connect(showConversation)
                }
            }
        }

        if (!conversationPageItem) {
            const conversationPage = conversationPaneComponent.createObject(pageStack)
            if (conversationPage) {
                conversationPageItem = conversationPage
                pageStack.push(conversationPage)
                if (conversationPage.closeChatRequested) {
                    conversationPage.closeChatRequested.connect(closeConversation)
                }
            }
        }

        // Pushing the conversation page leaves currentIndex at 1. Anchor it to
        // the actual selection so the very first wide -> single-column switch
        // shows the right column instead of an empty conversation pane.
        pageStack.currentIndex = Whatevr.AppController.hasSelectedChat ? 1 : 0
    }

    function showConversation() {
        if (currentMode !== "chat") {
            return
        }
        if (chatSingleColumnLayout) {
            // Defer the column move so selectChat()'s synchronous model rebuild
            // and the MessageView bottom-pin settle first; otherwise the slide
            // animation is starved of frames and appears to snap.
            Qt.callLater(navigateToConversation)
        } else {
            pageStack.currentIndex = 1
        }
        updateCloseChatActionVisibility()
    }

    function navigateToConversation() {
        if (currentMode === "chat" && Whatevr.AppController.hasSelectedChat) {
            pageStack.currentIndex = 1
        }
    }

    function closeConversation() {
        if (Whatevr.AppController.hasSelectedChat) {
            Whatevr.AppController.selectChat("")
        }
        if (pageStack.currentIndex > 0) {
            pageStack.currentIndex = 0
        }
        updateCloseChatActionVisibility()
    }

    function syncChatLayout() {
        if (currentMode !== "chat") {
            return
        }

        ensureChatPages()

        // Keep currentIndex in sync in every layout so layout switches never
        // reveal the wrong column. In wide mode both columns are visible, so
        // this only sets which one is focused.
        pageStack.currentIndex = Whatevr.AppController.hasSelectedChat ? 1 : 0
        updateCloseChatActionVisibility()

        if (pendingShowConversation && Whatevr.AppController.hasSelectedChat) {
            pendingShowConversation = false
            showConversation()
        }
    }

    function activateWindow() {
        root.show()
        root.raise()
        root.requestActivate()
    }

    function updateCloseChatActionVisibility() {
        if (!conversationPageItem) {
            return
        }

        conversationPageItem.closeChatActionVisible = currentMode === "chat"
                && Whatevr.AppController.hasSelectedChat
                && (!chatSingleColumnLayout || pageStack.currentIndex > 0)
    }

    function rebuildPageStack() {
        const nextMode = appMode()
        if (nextMode === currentMode) {
            if (nextMode === "chat") {
                syncChatLayout()
            }
            return
        }

        switch (nextMode) {
        case "login":
            resetToPage(nextMode, loginPageComponent)
            break
        case "status":
            resetToPage(nextMode, statusPageComponent)
            break
        case "chat":
            clearTransientPage()
            pageStack.clear()
            currentMode = nextMode
            syncChatLayout()
            break
        }
    }

    onChatWideLayoutChanged: syncChatLayout()

    Connections {
        target: root.pageStack

        function onCurrentIndexChanged() {
            root.updateCloseChatActionVisibility()

            if (root.currentMode !== "chat"
                    || !root.chatSingleColumnLayout
                    || root.pageStack.currentIndex !== 0
                    || !Whatevr.AppController.hasSelectedChat) {
                return
            }

            // Navigated back to the chat list (button or swipe). Drop the
            // selection once any in-flight column animation settles so the
            // conversation does not visibly empty mid-transition.
            if (root.pageStack.columnView.moving) {
                root.pendingSelectionClear = true
            } else {
                Whatevr.AppController.selectChat("")
            }
        }
    }

    Connections {
        target: root.pageStack.columnView

        function onMovingChanged() {
            if (root.pageStack.columnView.moving) {
                return
            }

            if (root.pendingSelectionClear
                    && root.pageStack.currentIndex === 0
                    && Whatevr.AppController.hasSelectedChat) {
                Whatevr.AppController.selectChat("")
            }
            root.pendingSelectionClear = false
            root.updateCloseChatActionVisibility()
        }
    }

    Component.onCompleted: scheduleRebuildPageStack()

    Connections {
        target: Whatevr.AppController

        function onStateChanged() {
            root.scheduleRebuildPageStack()
        }

        function onSelectionChanged() {
            root.updateCloseChatActionVisibility()
        }

        function onActivateWindowRequested() {
            root.activateWindow()
        }

        function onOpenChatRequested(chatId) {
            root.pendingShowConversation = true
            root.activateWindow()
            root.scheduleRebuildPageStack()
        }
    }
}
