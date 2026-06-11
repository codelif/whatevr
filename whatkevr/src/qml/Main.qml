import QtQuick
import QtQuick.Controls as QQC2
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
    // Back-navigation in the single-column layout clears the chat selection,
    // but only after the column slide settles, so the conversation does not
    // visibly empty mid-transition. maybeClearSelectionAfterBack() owns this.
    property bool pendingSelectionClear: false
    // Set when a deep link (e.g. notification click) arrives before the chat
    // pages exist; consumed by rebuildPageStack once they do.
    property bool pendingShowConversation: false

    width: 1180
    height: 760
    minimumWidth: 360
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

    function appMode() {
        // Initial pre-status window: show a neutral splash, not the daemon-status
        // page, so a normal sub-second connect doesn't flash "Connecting to
        // whatevrd" (which reads as the not-running screen).
        if (Whatevr.AppController.starting) {
            return "starting"
        }
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

    // Neutral loading page for the brief initial connect, so cold start never
    // flashes the daemon-status page before the chat shell appears.
    Component {
        id: splashPageComponent

        Kirigami.Page {
            padding: 0

            QQC2.BusyIndicator {
                anchors.centerIn: parent
                running: true
            }
        }
    }

    Component {
        id: chatListPaneComponent

        ChatListPane {}
    }

    Component {
        id: conversationPaneComponent

        ConversationPane {}
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
        if (currentMode !== "chat" || !Whatevr.AppController.hasSelectedChat) {
            return
        }
        // Re-opening before a back-slide settled supersedes the pending clear.
        pendingSelectionClear = false
        pageStack.currentIndex = 1
    }

    function closeConversation() {
        pendingSelectionClear = false
        if (Whatevr.AppController.hasSelectedChat) {
            Whatevr.AppController.selectChat("")
        }
        if (pageStack.currentIndex > 0) {
            pageStack.currentIndex = 0
        }
    }

    // Single owner of the clear-selection-on-back flow. Called when the
    // current column changes (back button or swipe) and again when the column
    // animation settles; the selection is only dropped once the chat list is
    // the settled, visible column, so the conversation never empties mid-slide.
    function maybeClearSelectionAfterBack() {
        if (currentMode !== "chat"
                || !chatSingleColumnLayout
                || pageStack.currentIndex !== 0
                || !Whatevr.AppController.hasSelectedChat) {
            pendingSelectionClear = false
            return
        }

        if (pageStack.columnView.moving) {
            pendingSelectionClear = true
            return
        }

        pendingSelectionClear = false
        Whatevr.AppController.selectChat("")
    }

    function rebuildPageStack() {
        const nextMode = appMode()
        if (nextMode === currentMode) {
            return
        }

        switch (nextMode) {
        case "starting":
            resetToPage(nextMode, splashPageComponent)
            break
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
            ensureChatPages()
            if (pendingShowConversation && Whatevr.AppController.hasSelectedChat) {
                pendingShowConversation = false
                showConversation()
            }
            break
        }
    }

    function activateWindow() {
        root.show()
        root.raise()
        root.requestActivate()
    }

    onChatWideLayoutChanged: {
        if (currentMode !== "chat") {
            return
        }
        // Land on the column matching the selection so a wide -> single-column
        // switch never reveals an empty conversation pane.
        pendingSelectionClear = false
        pageStack.currentIndex = Whatevr.AppController.hasSelectedChat ? 1 : 0
    }

    Connections {
        target: root.pageStack

        function onCurrentIndexChanged() {
            root.maybeClearSelectionAfterBack()
        }
    }

    Connections {
        target: root.pageStack.columnView

        function onMovingChanged() {
            if (!root.pageStack.columnView.moving && root.pendingSelectionClear) {
                root.maybeClearSelectionAfterBack()
            }
        }
    }

    Component.onCompleted: rebuildPageStack()

    Connections {
        target: Whatevr.AppController

        function onStateChanged() {
            // Coalesce bursts of state changes into one rebuild per frame.
            Qt.callLater(root.rebuildPageStack)
        }

        function onActivateWindowRequested() {
            root.activateWindow()
        }

        function onOpenChatRequested(chatId) {
            root.activateWindow()
            // The controller has already selected the chat at this point.
            if (root.currentMode === "chat") {
                root.showConversation()
            } else {
                root.pendingShowConversation = true
            }
        }
    }
}
