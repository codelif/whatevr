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
    // The chat id the settled navigation state must show ("" = chat list).
    // Every open/close intent writes it; applyNavTarget() applies it once the
    // column view has been still for a quiet period. The last intent always
    // wins, so clicking a chat mid-close-transition can never resurrect the
    // previous chat.
    property string navTargetChatId: ""
    // Distinguishes our own pageStack.currentIndex writes from user back
    // navigation (back button / edge swipe) in onCurrentIndexChanged.
    property bool navProgrammaticIndexChange: false
    // Set when a deep link (e.g. notification click) arrives before the chat
    // pages exist; consumed by rebuildPageStack once they do.
    property bool pendingShowConversation: false

    width: 1180
    height: 760
    minimumWidth: 360
    minimumHeight: 680
    title: Whatevr.I18n.i18nc("@title:window", "Whatevr")
    visible: true

    SettingsView {
        id: settingsView

        window: root
    }

    // Ctrl+, — the KDE-standard accelerator for opening preferences. Lives at
    // window scope so it fires regardless of which column has focus.
    Shortcut {
        sequences: [StandardKey.Preferences]
        onActivated: settingsView.open()
    }

    function openSettings(moduleId) {
        if (moduleId)
            settingsView.open(moduleId)
        else
            settingsView.open()
    }

    // Window geometry persistence. Saves are debounced so a drag-resize burst
    // collapses into one write; restore happens in Component.onCompleted.
    Timer {
        id: geometrySaveTimer

        interval: 500
        onTriggered: if (Whatevr.Settings.rememberWindowGeometry)
            Whatevr.Settings.saveWindowGeometry(root.x, root.y, root.width, root.height)
    }

    onXChanged: geometrySaveTimer.restart()
    onYChanged: geometrySaveTimer.restart()
    onWidthChanged: geometrySaveTimer.restart()
    onHeightChanged: geometrySaveTimer.restart()

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
        if (Whatevr.ProtocolController.starting) {
            return "starting"
        }
        if (Whatevr.ProtocolController.loginRequired) {
            return "login"
        }
        if (!Whatevr.ProtocolController.shellVisible) {
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
        // Pushing pages moves currentIndex; none of it is user back-navigation.
        navProgrammaticIndexChange = true
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
        navTargetChatId = Whatevr.AppController.selectedChatId
        pageStack.currentIndex = Whatevr.AppController.hasSelectedChat ? 1 : 0
        navProgrammaticIndexChange = false
    }

    function showConversation(chatId) {
        if (currentMode !== "chat" || !Whatevr.AppController.hasSelectedChat) {
            return
        }
        navTargetChatId = chatId || Whatevr.AppController.selectedChatId
        navProgrammaticIndexChange = true
        pageStack.currentIndex = 1
        navProgrammaticIndexChange = false
        // Start message/pin loading asynchronously right after the click frame;
        // no wait-for-settle. The controller/model keep first paint atomic for
        // unread opens and staged for no-unread hydration.
        Qt.callLater(Whatevr.AppController.populateSelectedChat)
    }

    function closeConversation() {
        navTargetChatId = ""
        if (chatWideLayout && Whatevr.AppController.hasSelectedChat) {
            // No slide in the wide layout; clear immediately.
            Whatevr.AppController.selectChat("")
        }
        if (pageStack.currentIndex > 0) {
            navProgrammaticIndexChange = true
            pageStack.currentIndex = 0
            navProgrammaticIndexChange = false
        }
        // Clears the selection after the quiet period when there is no slide;
        // while one runs the settle handler re-arms, so the conversation never
        // empties mid-transition.
        scheduleNavSettle()
    }

    // Navigation state is applied only once the column view has been still for
    // a quiet period. Kirigami's ColumnView emits several moving cycles per
    // navigation, so acting on a single settle edge lands the work inside the
    // next animation phase — every nav intent funnels through this timer.
    function scheduleNavSettle() {
        if (currentMode !== "chat" || pageStack.columnView.moving) {
            // The settle edge (onMovingChanged) re-arms the timer.
            return
        }
        navQuietTimer.restart()
    }

    Timer {
        id: navQuietTimer

        interval: 150
        onTriggered: {
            // Lift the transition gate first: it flushes any populate/message
            // page deferred while the slide ran, then the nav target settles.
            Whatevr.AppController.uiTransitionActive = false
            root.applyNavTarget()
        }
    }

    // Single owner of settled navigation state; only ever runs from the quiet
    // timer. Applies navTargetChatId: clears the selection once the chat list
    // is the settled column, or ensures the conversation column is current and
    // populates the selected chat.
    function applyNavTarget() {
        if (currentMode !== "chat" || pageStack.columnView.moving) {
            return
        }
        if (navTargetChatId === "") {
            if (chatSingleColumnLayout
                    && pageStack.currentIndex === 0
                    && Whatevr.AppController.hasSelectedChat) {
                Whatevr.AppController.selectChat("")
            }
            return
        }
        if (pageStack.currentIndex !== 1) {
            navProgrammaticIndexChange = true
            pageStack.currentIndex = 1
            navProgrammaticIndexChange = false
            if (pageStack.columnView.moving) {
                // The re-target started another slide; populate at its settle.
                return
            }
        }
        Whatevr.AppController.populateSelectedChat()
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
        navTargetChatId = Whatevr.AppController.selectedChatId
        navProgrammaticIndexChange = true
        pageStack.currentIndex = Whatevr.AppController.hasSelectedChat ? 1 : 0
        navProgrammaticIndexChange = false
        scheduleNavSettle()
    }

    Connections {
        target: root.pageStack

        function onCurrentIndexChanged() {
            if (root.navProgrammaticIndexChange) {
                return
            }
            // Back button / edge swipe land here without closeConversation();
            // any user-initiated move to the chat list is a close intent.
            if (root.chatSingleColumnLayout && root.pageStack.currentIndex === 0) {
                root.navTargetChatId = ""
            }
            root.scheduleNavSettle()
        }
    }

    Connections {
        target: root.pageStack.columnView

        function onMovingChanged() {
            if (Whatevr.AppController.perfLogging) {
                console.log("[perf] slide", root.pageStack.columnView.moving ? "start" : "settle")
            }
            if (root.pageStack.columnView.moving) {
                // Gate model work for the whole animation; a bounce within the
                // quiet period simply re-gates and the timer re-arms at its
                // settle edge.
                navQuietTimer.stop()
                Whatevr.AppController.uiTransitionActive = true
            } else {
                navQuietTimer.restart()
            }
        }
    }

    // Frame-pacing diagnostics (WHATKEVR_PERF=1): while a column slide runs,
    // logs every frame gap above ~1.5 vsync intervals (with its offset from
    // the slide start), so animation hitches can be attributed instead of
    // guessed at. Idle-time swap gaps are meaningless and stay unlogged.
    Item {
        id: framePacingProbe

        visible: false

        readonly property bool sliding: Whatevr.AppController.perfLogging
                                        && root.pageStack.columnView.moving
        property double lastSwapMs: 0
        property double slideStartMs: 0

        onSlidingChanged: {
            lastSwapMs = 0
            slideStartMs = sliding ? Date.now() : 0
        }

        Connections {
            target: root
            enabled: framePacingProbe.sliding

            function onFrameSwapped() {
                const now = Date.now()
                const gap = now - framePacingProbe.lastSwapMs
                if (framePacingProbe.lastSwapMs > 0 && gap > 25) {
                    console.log("[perf] frame gap", gap.toFixed(0), "ms at",
                                (now - framePacingProbe.slideStartMs).toFixed(0), "ms into slide")
                }
                framePacingProbe.lastSwapMs = now
            }
        }
    }

    Component.onCompleted: {
        if (Whatevr.Settings.rememberWindowGeometry && Whatevr.Settings.hasSavedWindowGeometry()) {
            root.width = Whatevr.Settings.savedWindowWidth()
            root.height = Whatevr.Settings.savedWindowHeight()
            root.x = Whatevr.Settings.savedWindowX()
            root.y = Whatevr.Settings.savedWindowY()
        }
        rebuildPageStack()
    }

    // Shell routing follows the protocol connection lifecycle (D2a): its
    // connection/login views decide splash/login/status/chat.
    Connections {
        target: Whatevr.ProtocolController

        function onStateChanged() {
            // Coalesce bursts of state changes into one rebuild per frame.
            Qt.callLater(root.rebuildPageStack)
            // Logging out returns to the login screen; don't leave settings open.
            if (Whatevr.ProtocolController.loginRequired) {
                settingsView.close()
            }
        }
    }

    Connections {
        target: Whatevr.AppController

        function onActivateWindowRequested() {
            root.activateWindow()
        }

        function onOpenChatRequested(chatId) {
            root.activateWindow()
            // The controller has already selected the chat at this point.
            if (root.currentMode === "chat") {
                root.showConversation(chatId)
            } else {
                root.pendingShowConversation = true
            }
        }
    }
}
