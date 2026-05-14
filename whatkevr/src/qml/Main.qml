import QtQuick
import org.kde.kirigami as Kirigami

Kirigami.ApplicationWindow {
    id: root

    readonly property bool chatWideLayout: pageStack.width >= pageStack.defaultColumnWidth * 2
    readonly property bool chatSingleColumnLayout: !chatWideLayout
    property string currentMode: ""
    property bool conversationOnStack: false
    property var chatListPageItem: null
    property var conversationPageItem: null

    width: 1180
    height: 760
    minimumWidth: 420
    minimumHeight: 680
    title: i18nc("@title:window", "Whatevr")
    visible: true

    pageStack.columnView.columnResizeMode: pageStack.wideMode ? Kirigami.ColumnView.FixedColumns : Kirigami.ColumnView.SingleColumn
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
        if (!conversationOnStack) {
            conversationPageItem = pushPage(conversationPaneComponent)
            conversationOnStack = conversationPageItem !== null
            if (conversationPageItem) {
                conversationPageItem.backRequested.connect(event => {
                    if (root.currentMode === "chat" && root.chatSingleColumnLayout && AppController.hasSelectedChat) {
                        AppController.selectChat("")
                    }
                })
            }
        } else {
            pageStack.goForward()
        }
    }

    function ensureChatLayout() {
        if (currentMode === "chat" && chatWideLayout && !conversationOnStack) {
            openConversation()
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
            chatListPageItem = null
            conversationPageItem = null
            pageStack.clear()
            openChatList()
            ensureChatLayout()
            break
        }
    }

    onChatWideLayoutChanged: ensureChatLayout()

    Component.onCompleted: scheduleRebuildPageStack()

    Connections {
        target: AppController

        function onStateChanged() {
            root.scheduleRebuildPageStack()
        }
    }
}
