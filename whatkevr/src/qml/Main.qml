import QtQuick
import org.kde.kirigami as Kirigami

Kirigami.ApplicationWindow {
    id: root

    readonly property bool wideLayout: width >= Kirigami.Units.gridUnit * 30
    property string currentMode: ""
    property var chatListPageItem: null
    property var conversationPageItem: null

    // width: 1180
    // height: 760
    minimumWidth: Kirigami.Units.gridUnit * 20
    minimumHeight: Kirigami.Units.gridUnit * 15
    title: i18nc("@title:window", "Whatevr")
    visible: true

    pageStack.columnView.columnResizeMode: wideLayout ? Kirigami.ColumnView.FixedColumns : Kirigami.ColumnView.SingleColumn
    pageStack.globalToolBar.style: Kirigami.ApplicationHeaderStyle.ToolBar
    pageStack.globalToolBar.showNavigationButtons: currentMode === "chat"
                                                  ? (!wideLayout && pageStack.currentIndex > 0
                                                     ? Kirigami.ApplicationHeaderStyle.ShowBackButton
                                                     : Kirigami.ApplicationHeaderStyle.NoNavigationButtons)
                                                  : Kirigami.ApplicationHeaderStyle.ShowBackButton | Kirigami.ApplicationHeaderStyle.ShowForwardButton

    Timer {
        id: pageStackRebuildTimer

        interval: 50
        repeat: false
        onTriggered: root.rebuildPageStack()
    }

    Timer {
        id: chatLayoutSyncTimer

        interval: 0
        repeat: false
        onTriggered: root.syncChatLayout()
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

    function scheduleSyncChatLayout() {
        chatLayoutSyncTimer.restart()
    }

    function resetToPage(mode, pageComponent) {
        currentMode = mode
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

    function ensureConversationPage() {
        if (!conversationPageItem) {
            conversationPageItem = pushPage(conversationPaneComponent)
            if (conversationPageItem) {
                conversationPageItem.backRequested.connect(event => {
                    if (root.currentMode === "chat" && !root.wideLayout && AppController.hasSelectedChat) {
                        AppController.selectChat("")
                    }
                })
            }
        }
    }

    function openConversation() {
        if (!conversationPageItem) {
            ensureConversationPage()
        } else if (pageStack.currentIndex === 0) {
            pageStack.goForward()
        }
    }

    function syncChatLayout() {
        if (currentMode !== "chat") {
            return
        }
        if (wideLayout) {
            ensureConversationPage()
            if (!AppController.hasSelectedChat && pageStack.currentIndex > 0) {
                pageStack.goBack()
            }
            return
        }
        if (!AppController.hasSelectedChat && pageStack.currentIndex > 0) {
            pageStack.goBack()
        }
    }

    function rebuildPageStack() {
        const nextMode = appMode()
        if (nextMode === currentMode) {
            scheduleSyncChatLayout()
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
            chatListPageItem = null
            conversationPageItem = null
            pageStack.clear()
            openChatList()
            scheduleSyncChatLayout()
            break
        }
    }

    onWideLayoutChanged: scheduleSyncChatLayout()

    Component.onCompleted: scheduleRebuildPageStack()

    Connections {
        target: AppController

        function onStateChanged() {
            root.scheduleRebuildPageStack()
        }

        function onSelectionChanged() {
            root.syncChatLayout()
        }
    }
}
