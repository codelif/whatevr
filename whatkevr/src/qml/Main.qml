import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import QtQuick.Window
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
    property var workspacePageItem: null
    property var transientPageItem: null
    // Secondary tabs share the same two-column shell. Pages are cached by URL
    // so switching tabs changes the right column without rebuilding the chat
    // list or reloading an already-open tab.
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
    property bool quitting: false
    function quitApplication() {
        quitting = true
        // Ask the daemon to exit too (tray icon is daemon-owned), then quit
        // the frontend even if the daemon is already gone.
        Whatevr.ProtocolController.shutdownDaemon()
        Qt.quit()
    }
    // Sidebar Home/DMs/Groups/Unread/Favorites entry point: reset the left
    // column to chats and show the conversation column.
    function openConversation() {
        if (currentMode !== "chat") {
            return
        }
        ensureChatPages()
        if (chatListPageItem)
            chatListPageItem.workspaceMode = "chats"
        navTargetChatId = Whatevr.ProtocolController.selectedChatId
        workspacePageItem.openConversation()
        navProgrammaticIndexChange = true
        pageStack.currentIndex = Whatevr.ProtocolController.hasSelectedChat ? 1 : 0
        navProgrammaticIndexChange = false
    }
    onClosing: closeEvent => {
        if (Whatevr.Settings.closeToTray && !quitting) {
            closeEvent.accepted = false
            root.hide()
        }
    }

    SettingsView {
        id: settingsView

        window: root
    }

    Connections {
        target: Whatevr.Settings
        function onAppLockChanged() {
            if (Whatevr.Settings.appLocked)
                settingsView.close()
        }
    }

    Rectangle {
        id: appLockOverlay
        anchors.fill: parent
        z: 10000
        visible: Whatevr.Settings.appLocked
        focus: visible
        activeFocusOnTab: visible
        color: Kirigami.Theme.backgroundColor

        ColumnLayout {
            anchors.centerIn: parent
            width: Math.min(parent.width - Kirigami.Units.largeSpacing * 4,
                            Kirigami.Units.gridUnit * 20)
            spacing: Kirigami.Units.largeSpacing

            Kirigami.Icon {
                Layout.alignment: Qt.AlignHCenter
                source: "object-locked-symbolic"
                implicitWidth: Kirigami.Units.iconSizes.large
                implicitHeight: implicitWidth
            }
            Kirigami.Heading {
                Layout.alignment: Qt.AlignHCenter
                text: Whatevr.I18n.i18nc("@title app lock", "Whatevr is locked")
            }
            QQC2.TextField {
                id: unlockPin
                Layout.fillWidth: true
                echoMode: TextInput.Password
                placeholderText: Whatevr.I18n.i18nc("@info:placeholder app unlock PIN", "PIN")
                onAccepted: unlockButton.clicked()
                Component.onCompleted: if (appLockOverlay.visible) forceActiveFocus()
            }
            QQC2.Button {
                id: unlockButton
                Layout.alignment: Qt.AlignHCenter
                text: Whatevr.I18n.i18nc("@action:button unlock app", "Unlock")
                enabled: unlockPin.text.length > 0
                onClicked: {
                    if (Whatevr.Settings.unlockApp(unlockPin.text)) {
                        unlockPin.clear()
                    } else {
                        unlockPin.selectAll()
                    }
                }
            }
        }
    }

    // Tray right-click menu (daemon `show_tray_menu` event). A top-level
    // Popup-flag window, not an in-window Menu: only a separate window can
    // render over the system panel where the click happened. It opens upward
    // from the click point like a native tray menu and dismisses on outside
    // click. Coordinates are screen space (0,0 when the platform supplies
    // none → bottom-right of the screen, where trays usually live).
    Window {
        id: trayMenuWindow

        // A Qt.Popup keeps a native pointer grab. If it survives the main
        // window's close-to-tray hide/show cycle, scrolling still works but
        // every chat-row and button click is swallowed. Keep it non-modal.
        flags: Qt.Tool | Qt.FramelessWindowHint | Qt.WindowStaysOnTopHint
        color: "transparent"
        visible: false

        width: Math.max(1, trayMenuCard.implicitWidth)
        height: Math.max(1, trayMenuCard.implicitHeight)

        Rectangle {
            id: trayMenuCard

            anchors.fill: parent
            radius: Kirigami.Units.cornerRadius
            color: Kirigami.Theme.backgroundColor
            border.width: 1
            border.color: Qt.alpha(Kirigami.Theme.textColor, 0.2)

            ColumnLayout {
                anchors.fill: parent
                anchors.margins: Kirigami.Units.smallSpacing
                spacing: 0

                QQC2.Button {
                    flat: true
                    Layout.fillWidth: true
                    text: Whatevr.I18n.i18nc("@action:inmenu open the main window", "Open Whatevr")
                    onClicked: {
                        trayMenuWindow.visible = false
                        root.activateWindow()
                    }
                }

                QQC2.CheckBox {
                    id: notificationsItem

                    Layout.fillWidth: true
                    text: Whatevr.I18n.i18nc("@action:inmenu toggle desktop notifications", "Notifications")
                    onToggled: Whatevr.ProtocolController.setAppPreference("notifications_enabled", checked)
                }

                QQC2.Button {
                    flat: true
                    Layout.fillWidth: true
                    text: Whatevr.I18n.i18nc("@action:inmenu mark all chats read from tray", "Mark all as read")
                    onClicked: {
                        trayMenuWindow.visible = false
                        Whatevr.ProtocolController.markAllChatsRead()
                    }
                }

                QQC2.CheckBox {
                    id: muteNotificationsItem
                    Layout.fillWidth: true
                    text: Whatevr.I18n.i18nc("@action:inmenu mute desktop notifications", "Mute notifications")
                    checked: !(Whatevr.ProtocolController.appPreferences.notifications_enabled ?? true)
                    onToggled: Whatevr.ProtocolController.setAppPreference("notifications_enabled", !checked)
                }

                Kirigami.Separator {
                    Layout.fillWidth: true
                }

                QQC2.Button {
                    flat: true
                    Layout.fillWidth: true
                    text: Whatevr.I18n.i18nc("@action:inmenu quit the application", "Quit")
                    onClicked: root.quitApplication()
                }
            }
        }

        function showAt(sx, sy) {
            notificationsItem.checked = Whatevr.ProtocolController.appPreferences.notifications_enabled ?? true
            muteNotificationsItem.checked = !notificationsItem.checked
            const screenW = Screen.desktopAvailableWidth > 0 ? Screen.desktopAvailableWidth : Screen.width
            const screenH = Screen.desktopAvailableHeight > 0 ? Screen.desktopAvailableHeight : Screen.height
            const w = trayMenuWindow.width
            const h = trayMenuWindow.height
            trayMenuWindow.x = sx > 0 ? Math.max(0, Math.min(sx - w / 2, screenW - w)) : screenW - w
            // Open upward from the click: panel trays sit at a screen edge.
            trayMenuWindow.y = sy > 0 ? Math.max(0, sy - h) : Math.max(0, screenH - h)
            trayMenuWindow.visible = true
        }
    }

    // Ctrl+, — the KDE-standard accelerator for opening preferences. Lives at
    // window scope so it fires regardless of which column has focus.
    Shortcut {
        sequences: [StandardKey.Preferences]
        enabled: !Whatevr.Settings.appLocked
        onActivated: settingsView.open()
    }

    function openSettings(moduleId) {
        if (Whatevr.Settings.appLocked)
            return
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
        id: workspacePaneComponent

        WorkspacePane {}
    }

    function destroyChatPages() {
        pageStack.clear()
        if (workspacePageItem) {
            workspacePageItem.destroy()
            workspacePageItem = null
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
                listPage.statusSelected.connect(showStatusViewer)
                listPage.channelSelected.connect(showChannelMessages)
            }
        }

        if (!workspacePageItem) {
            const workspacePage = workspacePaneComponent.createObject(pageStack)
            if (workspacePage) {
                workspacePageItem = workspacePage
                conversationPageItem = workspacePage.conversationPane
                pageStack.push(workspacePage)
                if (workspacePage.closeChatRequested) {
                    workspacePage.closeChatRequested.connect(closeConversation)
                }
            }
        }

        // Pushing the conversation page leaves currentIndex at 1. Anchor it to
        // the actual selection so the very first wide -> single-column switch
        // shows the right column instead of an empty conversation pane.
        navTargetChatId = Whatevr.ProtocolController.selectedChatId
        workspacePageItem.openConversation()
        pageStack.currentIndex = Whatevr.ProtocolController.hasSelectedChat ? 1 : 0
        navProgrammaticIndexChange = false
    }

    function openWorkspace(tab) {
        if (currentMode !== "chat") {
            return
        }
        ensureChatPages()
        // Status/Channels lists live in the left column; their content opens
        // in the right column only once an item is picked. Other tabs render
        // directly in the right column.
        if (tab === "status" || tab === "channels") {
            chatListPageItem.workspaceMode = tab
            navProgrammaticIndexChange = true
            workspacePageItem.openConversation()
            pageStack.currentIndex = 1
            navProgrammaticIndexChange = false
            return
        }
        chatListPageItem.workspaceMode = "chats"
        navProgrammaticIndexChange = true
        workspacePageItem.openTab(String(tab))
        pageStack.currentIndex = 1
        navProgrammaticIndexChange = false
    }

    function showStatusViewer(senderId, senderName) {
        workspacePageItem.openStatusViewer(senderId, senderName)
        pageStack.currentIndex = 1
    }

    function showChannelMessages(channelId, channelName) {
        workspacePageItem.openChannelMessages(channelId, channelName)
        pageStack.currentIndex = 1
    }

    function showConversation(chatId) {
        if (currentMode !== "chat" || !Whatevr.ProtocolController.hasSelectedChat) {
            return
        }
        navTargetChatId = chatId || Whatevr.ProtocolController.selectedChatId
        if (chatListPageItem)
            chatListPageItem.workspaceMode = "chats"
        workspacePageItem.openConversation()
        navProgrammaticIndexChange = true
        pageStack.currentIndex = 1
        navProgrammaticIndexChange = false
    }

    function closeConversation() {
        navTargetChatId = ""
        if (chatWideLayout && Whatevr.ProtocolController.hasSelectedChat) {
            // No slide in the wide layout; clear immediately.
            Whatevr.ProtocolController.selectChat("")
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
        onTriggered: root.applyNavTarget()
    }

    // Single owner of settled navigation state; only ever runs from the quiet
    // timer. Applies navTargetChatId: clears the selection once the chat list
    // is the settled column, or ensures the conversation column is current.
    function applyNavTarget() {
        if (currentMode !== "chat" || pageStack.columnView.moving) {
            return
        }
        if (navTargetChatId === "") {
            if (chatSingleColumnLayout
                    && pageStack.currentIndex === 0
                    && Whatevr.ProtocolController.hasSelectedChat) {
                Whatevr.ProtocolController.selectChat("")
            }
            return
        }
        if (pageStack.currentIndex !== 1) {
            navProgrammaticIndexChange = true
            pageStack.currentIndex = 1
            navProgrammaticIndexChange = false
        }
    }

    function rebuildPageStack() {
        const nextMode = appMode()
        Whatevr.ProtocolController.setConversationVisible(nextMode === "chat")
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
            if (pendingShowConversation && Whatevr.ProtocolController.hasSelectedChat) {
                pendingShowConversation = false
                showConversation()
            }
            break
        }
    }

    function activateWindow() {
        trayMenuWindow.close()
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
        navTargetChatId = Whatevr.ProtocolController.selectedChatId
        navProgrammaticIndexChange = true
        pageStack.currentIndex = Whatevr.ProtocolController.hasSelectedChat ? 1 : 0
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
            if (Whatevr.ProtocolController.perfLogging) {
                console.log("[perf] slide", root.pageStack.columnView.moving ? "start" : "settle")
            }
            if (root.pageStack.columnView.moving) {
                // A bounce within the quiet period simply re-arms the timer at
                // its settle edge.
                navQuietTimer.stop()
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

        readonly property bool sliding: Whatevr.ProtocolController.perfLogging
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

    // Shell routing follows the protocol connection lifecycle: its
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

        function onActivateWindowRequested() {
            root.activateWindow()
        }

        // Tray right-click: show the tray menu window at the click point (see
        // above). show() alone unhides a hidden main window without stealing
        // focus; the menu positions itself.
        function onShowTrayMenuRequested(x, y) {
            root.show()
            trayMenuWindow.showAt(x, y)
        }

        // The daemon's `open_chat` (notification click, whatevr:// URL) and the
        // local deep-link route both land here with the chat already selected.
        function onOpenChatRequested(chatId) {
            root.activateWindow()
            Whatevr.ProtocolController.selectChat(chatId)
            if (root.currentMode === "chat") {
                root.showConversation(chatId)
            } else {
                root.pendingShowConversation = true
            }
        }
    }
}
