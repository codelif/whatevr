import QtQuick
import org.kde.kirigami as Kirigami

Kirigami.ApplicationWindow {
    id: root

    readonly property bool chatWideLayout: pageStack.width >= pageStack.defaultColumnWidth * 2
    readonly property bool chatSingleColumnLayout: !chatWideLayout
    property string currentMode: ""
    property bool conversationOnStack: false
    property bool closeChatAfterTransition: false
    property var chatListPageItem: null
    property var conversationPageItem: null

    width: 1180
    height: 760
    minimumWidth: 420
    minimumHeight: 680
    title: i18nc("@title:window", "Whatevr")
    visible: true

    pageStack.columnView.columnResizeMode: chatWideLayout ? Kirigami.ColumnView.FixedColumns : Kirigami.ColumnView.SingleColumn
    pageStack.globalToolBar.style: Kirigami.ApplicationHeaderStyle.ToolBar
    pageStack.globalToolBar.showNavigationButtons: currentMode === "chat"
                                                  ? (chatSingleColumnLayout && pageStack.currentIndex > 0
                                                     ? Kirigami.ApplicationHeaderStyle.ShowBackButton
                                                     : Kirigami.ApplicationHeaderStyle.NoNavigationButtons)
                                                  : Kirigami.ApplicationHeaderStyle.ShowBackButton | Kirigami.ApplicationHeaderStyle.ShowForwardButton

    Timer {
        id: pageStackRebuildTimer

        interval: 50
        repeat: false
        onTriggered: root.rebuildPageStack()
    }

    function appMode() {
        if (AppController.loginRequired) {
            return "login"
        }
        if (!AppController.shellVisible) {
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

    function createPage(component) {
        return component.createObject(pageStack)
    }

    function pushPage(component) {
        const page = createPage(component)
        if (!page) {
            console.warn("Failed to create page")
            return null
        }
        return pageStack.push(page)
    }

    function scheduleRebuildPageStack() {
        pageStackRebuildTimer.restart()
    }

    function resetToPage(mode, pageComponent) {
        currentMode = mode
        conversationOnStack = false
        closeChatAfterTransition = false
        chatListPageItem = null
        conversationPageItem = null
        pageStack.clear()
        pushPage(pageComponent)
    }

    function openChatList() {
        chatListPageItem = pushPage(chatListPaneComponent)
        if (chatListPageItem && chatListPageItem.chatSelected) {
            chatListPageItem.chatSelected.connect(openConversation)
        }
    }

    function openConversation() {
        closeChatAfterTransition = false
        if (!conversationOnStack) {
            conversationPageItem = pushPage(conversationPaneComponent)
            conversationOnStack = conversationPageItem !== null
            if (conversationPageItem) {
                conversationPageItem.closeChatRequested.connect(closeConversation)
            }
        } else {
            pageStack.goForward()
        }
        updateCloseChatActionVisibility()
    }

    function closeConversation() {
        if (!AppController.hasSelectedChat) {
            return
        }

        if (conversationPageItem) {
            conversationPageItem.closeChatActionVisible = false
        }

        if (chatSingleColumnLayout && pageStack.currentIndex > 0) {
            closeChatAfterTransition = true
            pageStack.goBack()
            Qt.callLater(finishCloseChatIfSettled)
            return
        }

        AppController.selectChat("")
        updateCloseChatActionVisibility()
    }

    function updateCloseChatActionVisibility() {
        if (!conversationPageItem) {
            return
        }

        conversationPageItem.closeChatActionVisible = currentMode === "chat"
                && AppController.hasSelectedChat
                && !closeChatAfterTransition
                && (!chatSingleColumnLayout || pageStack.currentIndex > 0)
    }

    function scheduleCloseChatAfterBack() {
        if (!chatSingleColumnLayout || pageStack.currentIndex !== 0 || !AppController.hasSelectedChat) {
            return
        }

        closeChatAfterTransition = true
        updateCloseChatActionVisibility()
        Qt.callLater(finishCloseChatIfSettled)
    }

    function finishCloseChatIfSettled() {
        if (!closeChatAfterTransition || pageStack.columnView.moving) {
            return
        }

        closeChatAfterTransition = false
        if (AppController.hasSelectedChat) {
            AppController.selectChat("")
        }
        cleanupMobileConversationPage()
        updateCloseChatActionVisibility()
    }

    function cleanupMobileConversationPage() {
        if (!chatSingleColumnLayout || pageStack.currentIndex !== 0 || !conversationOnStack) {
            return
        }

        pageStack.pop()
        conversationOnStack = false
        conversationPageItem = null
    }

    function ensureChatLayout() {
        if (currentMode !== "chat") {
            return
        }

        if (chatWideLayout && !conversationOnStack) {
            openConversation()
        }
    }

    function syncChatLayout() {
        if (currentMode !== "chat") {
            return
        }

        ensureChatLayout()

        if (chatSingleColumnLayout) {
            pageStack.currentIndex = AppController.hasSelectedChat && conversationOnStack ? 1 : 0
        }
    }

    function rebuildPageStack() {
        const nextMode = appMode()
        if (nextMode === currentMode) {
            ensureChatLayout()
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
            currentMode = nextMode
            conversationOnStack = false
            closeChatAfterTransition = false
            chatListPageItem = null
            conversationPageItem = null
            pageStack.clear()
            openChatList()
            ensureChatLayout()
            break
        }
    }

    onChatWideLayoutChanged: syncChatLayout()

    Connections {
        target: pageStack

        function onCurrentIndexChanged() {
            root.updateCloseChatActionVisibility()
            root.scheduleCloseChatAfterBack()
        }
    }

    Connections {
        target: pageStack.columnView

        function onMovingChanged() {
            root.updateCloseChatActionVisibility()
            if (!pageStack.columnView.moving) {
                root.finishCloseChatIfSettled()
            }
        }
    }

    Component.onCompleted: scheduleRebuildPageStack()

    Connections {
        target: AppController

        function onStateChanged() {
            root.scheduleRebuildPageStack()
        }

        function onSelectionChanged() {
            root.updateCloseChatActionVisibility()
        }
    }
}
