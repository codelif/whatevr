pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Kirigami.Page {
    id: root

    signal chatSelected(string chatId)

    // The search bar is revealed by the toolbar search button, not shown by
    // default. Hiding it clears any active query.
    property bool searchBarVisible: false

    function hideSearch() {
        searchBarVisible = false
        Whatevr.AppController.clearSearch()
    }

    Layout.fillHeight: true
    Layout.minimumWidth: Kirigami.Units.gridUnit * 17
    // Track the window: roughly a third of the width, clamped so the list
    // neither starves the conversation on small windows nor sprawls on wide
    // ones. (In the single-column layout the column spans the window anyway.)
    Layout.preferredWidth: Math.max(Kirigami.Units.gridUnit * 17,
                                    Math.min(Kirigami.Units.gridUnit * 24,
                                             Math.round((applicationWindow()?.width ?? 0) * 0.34)))
    Layout.maximumWidth: Kirigami.Units.gridUnit * 24
    title: Whatevr.I18n.i18nc("@title", "Chats")
    padding: 0
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    actions: [
        Kirigami.Action {
            icon.name: "search-symbolic"
            text: Whatevr.I18n.i18nc("@action:button toggle the chat search bar", "Search")
            displayHint: Kirigami.DisplayHint.IconOnly
            checkable: true
            checked: root.searchBarVisible
            onTriggered: {
                if (root.searchBarVisible) {
                    root.hideSearch()
                } else {
                    root.searchBarVisible = true
                }
            }
        },
        Kirigami.Action {
            icon.name: "view-refresh-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Refresh")
            visible: Whatevr.AppController.bannerText.length > 0
            enabled: Whatevr.AppController.primaryActionEnabled
            onTriggered: Whatevr.AppController.triggerPrimaryAction()
        },
        Kirigami.Action {
            icon.name: "starred-symbolic"
            text: Whatevr.I18n.i18nc("@action:button open the starred-messages view", "Starred messages")
            displayHint: Kirigami.DisplayHint.AlwaysHide
            onTriggered: {
                Whatevr.AppController.loadStarredMessages("")
                applicationWindow().pageStack.layers.push(Qt.resolvedUrl("StarredMessagesPage.qml"), {
                    chatId: "",
                    headerTitle: Whatevr.I18n.i18nc("@title", "Starred messages")
                })
            }
        },
        Kirigami.Action {
            icon.name: "system-log-out-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Log out")
            displayHint: Kirigami.DisplayHint.AlwaysHide
            onTriggered: Whatevr.AppController.logout()
        }
    ]

    ColumnLayout {
        anchors.fill: parent
        spacing: 0

        HistorySyncStrip {
            Layout.margins: Kirigami.Units.largeSpacing
            Layout.bottomMargin: Whatevr.AppController.historySyncVisible ? Kirigami.Units.smallSpacing : 0
        }

        Kirigami.InlineMessage {
            Layout.fillWidth: true
            Layout.leftMargin: Kirigami.Units.largeSpacing
            Layout.rightMargin: Kirigami.Units.largeSpacing
            visible: Whatevr.AppController.bannerText.length > 0
            type: Kirigami.MessageType.Warning
            showCloseButton: false
            text: Whatevr.AppController.bannerText
        }

        Kirigami.SearchField {
            id: searchField

            visible: root.searchBarVisible
            Layout.fillWidth: true
            Layout.margins: Kirigami.Units.largeSpacing
            Layout.bottomMargin: Kirigami.Units.smallSpacing
            placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Search chats and messages")
            // Debounced daemon-side search; the model fills as results arrive.
            onTextChanged: Whatevr.AppController.setSearchQuery(text)

            onVisibleChanged: {
                if (visible) {
                    forceActiveFocus()
                } else {
                    text = ""
                }
            }

            // First Escape clears a non-empty query; a second Escape (empty
            // field) dismisses the search bar entirely.
            Keys.onEscapePressed: event => {
                if (searchField.text.length > 0) {
                    searchField.text = ""
                } else {
                    root.hideSearch()
                }
                event.accepted = true
            }

            Connections {
                target: Whatevr.AppController
                // Keep the field in sync when the search is cleared elsewhere
                // (e.g. after opening a result).
                function onSearchChanged() {
                    if (Whatevr.AppController.searchQuery !== searchField.text) {
                        searchField.text = Whatevr.AppController.searchQuery
                    }
                }
            }
        }

        Item {
            id: chatListViewport

            Layout.fillWidth: true
            Layout.fillHeight: true

            ListView {
                id: chatList

                visible: !Whatevr.AppController.searchActive

                property string contextChatId: ""
                property bool contextChatPinned: false
                property bool contextChatArchived: false
                property bool contextChatMuted: false
                // Whether the collapsible "Archived" section is expanded.
                property bool archivedExpanded: false

                anchors.fill: parent
                clip: true
                model: Whatevr.AppController.chatListModel
                currentIndex: -1
                boundsBehavior: Flickable.StopAtBounds
                flickableDirection: Flickable.VerticalFlick
                acceptedButtons: Qt.NoButton
                reuseItems: true
                spacing: 0
                cacheBuffer: Math.max(0, Math.round(height * 0.3))
                ScrollBar.vertical: DiscreetScrollBar {}

                // Archived chats sort last (model) and carry chatSection ===
                // "archived". Only the archived section gets a visible header —
                // the active section's header collapses to nothing.
                section.property: "chatSection"
                section.criteria: ViewSection.FullString
                section.delegate: Item {
                    id: sectionHeader

                    // ListView assigns "section" to the section delegate as a
                    // required property (the chatSection role: "archived"/"active").
                    required property string section

                    readonly property bool isArchivedSection: section === "archived"
                    width: chatList.width
                    height: isArchivedSection ? archivedHeader.implicitHeight : 0
                    visible: isArchivedSection

                    ItemDelegate {
                        id: archivedHeader

                        anchors.fill: parent
                        visible: sectionHeader.isArchivedSection
                        implicitHeight: Kirigami.Units.gridUnit * 2.0
                        padding: 0
                        onClicked: chatList.archivedExpanded = !chatList.archivedExpanded

                        contentItem: RowLayout {
                            spacing: Kirigami.Units.largeSpacing

                            Kirigami.Icon {
                                Layout.leftMargin: Kirigami.Units.largeSpacing
                                implicitWidth: Kirigami.Units.iconSizes.small
                                implicitHeight: Kirigami.Units.iconSizes.small
                                source: chatList.archivedExpanded ? "go-down-symbolic" : "go-next-symbolic"
                                color: Kirigami.Theme.textColor
                                isMask: true
                            }

                            Kirigami.Icon {
                                implicitWidth: Kirigami.Units.iconSizes.small
                                implicitHeight: Kirigami.Units.iconSizes.small
                                source: "package-x-generic-symbolic"
                                color: Kirigami.Theme.neutralTextColor
                                isMask: true
                            }

                            Label {
                                Layout.fillWidth: true
                                text: Whatevr.I18n.i18nc("@title:group chat list section",
                                                         "Archived (%1)",
                                                         Whatevr.AppController.chatListModel.archivedCount)
                                elide: Text.ElideRight
                                font.bold: true
                            }
                        }
                    }
                }

                delegate: ChatListDelegate {
                    id: chatDelegate

                    required property var model

                    chatId: String(model.chatId || "")
                    name: String(model.name || "")
                    lastMessage: String(model.lastMessage || "")
                    lastMessageDirection: Number(model.lastMessageDirection || 0)
                    lastMessageStatus: Number(model.lastMessageStatus || 0)
                    avatarLocalPath: String(model.avatarLocalPath || "")
                    initials: String(model.initials || "?")
                    unreadCount: Number(model.unreadCount || 0)
                    isPinned: Boolean(model.isPinned || false)
                    isArchived: Boolean(model.isArchived || false)
                    isMuted: Boolean(model.isMuted || false)
                    archivedExpanded: chatList.archivedExpanded
                    isTyping: Boolean(model.isTyping || false)
                    hasDraft: Boolean(model.hasDraft || false)
                    draftText: String(model.draftText || "")
                    current: Whatevr.AppController.selectedChatId === chatId
                    onSelected: id => {
                        Whatevr.AppController.selectChat(id)
                        root.chatSelected(id)
                    }
                    onPinToggled: (id, pinned) => Whatevr.AppController.setChatPinned(id, pinned)
                    onContextMenuRequested: (id, pinned, archived, muted, x, y) => {
                        chatList.contextChatId = id
                        chatList.contextChatPinned = pinned
                        chatList.contextChatArchived = archived
                        chatList.contextChatMuted = muted
                        // Collapse the hidden mute/unmute row before open() so the
                        // menu is sized correctly on the first frame (doing it in
                        // onAboutToShow lands a frame late, leaving a stray scrollbar).
                        chatContextMenu.refreshMuteItems()
                        const pos = chatDelegate.mapToItem(chatContextMenu.parent, x, y)
                        chatContextMenu.x = pos.x
                        chatContextMenu.y = pos.y
                        chatContextMenu.open()
                    }
                }

                Menu {
                    id: chatContextMenu

                    parent: chatList
                    readonly property real framePadding: Kirigami.Units.smallSpacing
                    topPadding: framePadding
                    bottomPadding: framePadding
                    leftPadding: framePadding
                    rightPadding: framePadding

                    // The MenuItem QQC2 auto-generates for the Mute submenu does
                    // not honour the submenu's own `visible` binding, so capture
                    // it and toggle on open (mirrors MessageView's pin submenu).
                    // Hidden items must also collapse their implicitHeight — the
                    // menu sums implicitHeight (not height), so a merely
                    // invisible row still inflates the menu and adds a scrollbar.
                    property Item muteSubMenuItem: null
                    property real menuRowHeight: 0

                    function refreshMuteItems() {
                        const muted = chatList.contextChatMuted
                        unmuteItem.visible = muted
                        unmuteItem.implicitHeight = muted ? menuRowHeight : 0
                        if (muteSubMenuItem) {
                            muteSubMenuItem.visible = !muted
                            muteSubMenuItem.implicitHeight = muted ? 0 : menuRowHeight
                        }
                    }

                    Component.onCompleted: {
                        menuRowHeight = unmuteItem.implicitHeight
                        for (let i = 0; i < count; ++i) {
                            const item = itemAt(i)
                            if (item && item.subMenu === muteDurationSubMenu) {
                                muteSubMenuItem = item
                                break
                            }
                        }
                        refreshMuteItems()
                    }

                    onAboutToShow: {
                        refreshMuteItems()
                        // heightRatio is stale after the imperative implicitHeight
                        // toggle above; relayout the body so no scrollbar flashes.
                        if (contentItem)
                            contentItem.forceLayout()
                    }

                    // No exit animation: right-clicking another row dismisses the
                    // open menu and reopens it at the new position in the same
                    // press, and a fading copy left at the old spot reads as a
                    // second menu flashing. Closing instantly removes the ghost.
                    exit: Transition {}

                    MenuItem {
                        text: chatList.contextChatPinned
                              ? Whatevr.I18n.i18nc("@action:menu", "Unpin chat")
                              : Whatevr.I18n.i18nc("@action:menu", "Pin chat")
                        icon.name: chatList.contextChatPinned ? "window-unpin" : "window-pin"
                        onTriggered: Whatevr.AppController.setChatPinned(chatList.contextChatId, !chatList.contextChatPinned)
                    }

                    MenuItem {
                        text: chatList.contextChatArchived
                              ? Whatevr.I18n.i18nc("@action:menu", "Unarchive chat")
                              : Whatevr.I18n.i18nc("@action:menu", "Archive chat")
                        icon.name: chatList.contextChatArchived ? "package-up-symbolic" : "package-down-symbolic"
                        onTriggered: Whatevr.AppController.setChatArchived(chatList.contextChatId, !chatList.contextChatArchived)
                    }

                    MenuItem {
                        id: unmuteItem

                        text: Whatevr.I18n.i18nc("@action:menu", "Unmute chat")
                        icon.name: "notifications-symbolic"
                        // visible / implicitHeight are driven imperatively by
                        // chatContextMenu.refreshMuteItems() (see above).
                        onTriggered: Whatevr.AppController.setChatMuted(chatList.contextChatId, false, 0)
                    }

                    Menu {
                        id: muteDurationSubMenu

                        title: Whatevr.I18n.i18nc("@action:inmenu mute a chat", "Mute")
                        icon.name: "notifications-disabled-symbolic"
                        // Row visibility is driven imperatively via the captured
                        // generated MenuItem (refreshMuteItems). A `visible` binding
                        // here would not hide the row and, since a Menu is a Popup,
                        // would auto-open this submenu on first show.

                        readonly property real framePadding: Kirigami.Units.smallSpacing
                        topPadding: framePadding
                        bottomPadding: framePadding
                        leftPadding: framePadding
                        rightPadding: framePadding

                        MenuItem {
                            text: Whatevr.I18n.i18nc("@action:inmenu mute duration", "For 8 hours")
                            onTriggered: Whatevr.AppController.setChatMuted(chatList.contextChatId, true, 8 * 60 * 60)
                        }
                        MenuItem {
                            text: Whatevr.I18n.i18nc("@action:inmenu mute duration", "For 1 week")
                            onTriggered: Whatevr.AppController.setChatMuted(chatList.contextChatId, true, 7 * 24 * 60 * 60)
                        }
                        MenuItem {
                            text: Whatevr.I18n.i18nc("@action:inmenu mute duration", "Always")
                            onTriggered: Whatevr.AppController.setChatMuted(chatList.contextChatId, true, 0)
                        }
                        MenuItem {
                            text: Whatevr.I18n.i18nc("@action:inmenu mute for a custom duration", "Custom…")
                            onTriggered: customMuteDialog.openFor(chatList.contextChatId)
                        }
                    }
                }

                BusyIndicator {
                    anchors.centerIn: parent
                    running: Whatevr.AppController.chatsLoading && Whatevr.AppController.chatsEmpty
                    visible: running
                }

                Kirigami.PlaceholderMessage {
                    anchors.centerIn: parent
                    width: Math.min(parent.width - Kirigami.Units.largeSpacing * 4,
                                    Kirigami.Units.gridUnit * 16)
                    visible: !Whatevr.AppController.chatsLoading && Whatevr.AppController.chatsEmpty
                    text: Whatevr.I18n.i18nc("@info", "No chats yet")
                    explanation: Whatevr.I18n.i18nc("@info", "Chats will appear here as history sync stores them locally.")
                }
            }

            KineticWheelScroller {
                anchors.fill: chatList
                target: chatList
                // Only the visible list's scroller may be live; a disabled Item
                // lets wheel events fall through to the sibling beneath it, so
                // the hidden one never intercepts scrolling.
                enabled: chatList.visible
                wheelStep: Kirigami.Units.gridUnit * 4
            }

            // Search results replace the chat list while a query is active.
            // Chat-name matches and message-text matches are split into two
            // sections via the model's "kind" role.
            ListView {
                id: searchList

                anchors.fill: parent
                clip: true
                visible: Whatevr.AppController.searchActive
                model: Whatevr.AppController.searchResultsModel
                currentIndex: -1
                boundsBehavior: Flickable.StopAtBounds
                reuseItems: true
                spacing: 0
                ScrollBar.vertical: DiscreetScrollBar {}

                section.property: "kind"
                section.criteria: ViewSection.FullString
                section.delegate: Kirigami.ListSectionHeader {
                    required property string section

                    width: ListView.view ? ListView.view.width : 0
                    text: {
                        if (section === "message") {
                            return Whatevr.I18n.i18nc("@title:group search results", "Messages")
                        }
                        if (section === "number") {
                            return Whatevr.I18n.i18nc("@title:group search results", "Phone number")
                        }
                        return Whatevr.I18n.i18nc("@title:group search results", "Chats")
                    }
                }

                delegate: SearchResultDelegate {
                    required property var model

                    kind: String(model.kind || "chat")
                    avatarLocalPath: String(model.avatarLocalPath || "")
                    initials: String(model.initials || "?")
                    title: String(model.title || "")
                    subtitle: String(model.subtitle || "")
                    chatId: String(model.chatId || "")
                    messageId: String(model.messageId || "")
                    senderName: String(model.senderName || "")
                    timeText: String(model.timeText || "")
                    isOutgoing: Boolean(model.isOutgoing || false)
                    jid: String(model.jid || "")
                    registered: Boolean(model.registered || false)

                    onChatActivated: id => {
                        Whatevr.AppController.selectChat(id)
                        root.hideSearch()
                        root.chatSelected(id)
                    }
                    onMessageActivated: (cId, mId) => {
                        Whatevr.AppController.showMessageInChat(cId, mId)
                        root.hideSearch()
                        root.chatSelected(cId)
                    }
                    onNumberActivated: jidArg => {
                        Whatevr.AppController.startDirectChat(jidArg)
                        root.hideSearch()
                    }
                }

                BusyIndicator {
                    anchors.centerIn: parent
                    running: Whatevr.AppController.searchBusy && searchList.count === 0
                    visible: running
                }

                Kirigami.PlaceholderMessage {
                    anchors.centerIn: parent
                    width: Math.min(parent.width - Kirigami.Units.largeSpacing * 4,
                                    Kirigami.Units.gridUnit * 16)
                    visible: !Whatevr.AppController.searchBusy && searchList.count === 0
                    icon.name: "search-symbolic"
                    text: Whatevr.I18n.i18nc("@info", "No results")
                    explanation: Whatevr.I18n.i18nc("@info", "No chats or messages match your search.")
                }
            }

            KineticWheelScroller {
                anchors.fill: searchList
                target: searchList
                enabled: searchList.visible
                wheelStep: Kirigami.Units.gridUnit * 4
            }
        }
    }

    Kirigami.PromptDialog {
        id: customMuteDialog

        // Centre on the stable implicitHeight (see deleteConfirmDialog note).
        y: parent ? Math.round((parent.height - implicitHeight) / 2) : 0

        property string chatId: ""
        // Seconds per unit: hours, days, weeks.
        readonly property var unitSeconds: [60 * 60, 24 * 60 * 60, 7 * 24 * 60 * 60]

        function openFor(id) {
            chatId = id
            amountSpin.value = 1
            unitCombo.currentIndex = 0
            open()
        }

        title: Whatevr.I18n.i18nc("@title:dialog", "Mute chat")
        standardButtons: Kirigami.Dialog.Cancel
        showCloseButton: false

        contentItem: RowLayout {
            spacing: Kirigami.Units.largeSpacing

            Label {
                text: Whatevr.I18n.i18nc("@label:spinbox mute duration", "Mute for")
            }

            SpinBox {
                id: amountSpin
                from: 1
                to: 999
                value: 1
                editable: true
                Layout.preferredWidth: Kirigami.Units.gridUnit * 5
            }

            ComboBox {
                id: unitCombo
                Layout.fillWidth: true
                model: [
                    Whatevr.I18n.i18nc("@item:inlistbox mute duration unit", "hours"),
                    Whatevr.I18n.i18nc("@item:inlistbox mute duration unit", "days"),
                    Whatevr.I18n.i18nc("@item:inlistbox mute duration unit", "weeks"),
                ]
            }
        }

        customFooterActions: [
            Kirigami.Action {
                text: Whatevr.I18n.i18nc("@action:button", "Mute")
                icon.name: "notifications-disabled-symbolic"
                onTriggered: {
                    const secs = amountSpin.value * customMuteDialog.unitSeconds[unitCombo.currentIndex]
                    Whatevr.AppController.setChatMuted(customMuteDialog.chatId, true, secs)
                    customMuteDialog.close()
                }
            }
        ]
    }
}
