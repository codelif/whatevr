pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Kirigami.Page {
    id: root

    signal chatSelected(string chatId)

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

        Item {
            id: chatListViewport

            Layout.fillWidth: true
            Layout.fillHeight: true

            ListView {
                id: chatList

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
                    // it and toggle visibility on open (mirrors MessageView's
                    // pin submenu). Otherwise Mute and Unmute both show.
                    property Item muteSubMenuItem: null

                    Component.onCompleted: {
                        for (let i = 0; i < count; ++i) {
                            const item = itemAt(i)
                            if (item && item.subMenu === muteDurationSubMenu) {
                                muteSubMenuItem = item
                                item.visible = false
                                break
                            }
                        }
                    }

                    onAboutToShow: {
                        if (muteSubMenuItem) {
                            muteSubMenuItem.visible = !chatList.contextChatMuted
                        }
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
                        text: Whatevr.I18n.i18nc("@action:menu", "Unmute chat")
                        icon.name: "notifications-symbolic"
                        visible: chatList.contextChatMuted
                        // A hidden MenuItem still reserves its height, overflowing
                        // the menu and adding a stray scrollbar; collapse it.
                        height: visible ? implicitHeight : 0
                        onTriggered: Whatevr.AppController.setChatMuted(chatList.contextChatId, false, 0)
                    }

                    Menu {
                        id: muteDurationSubMenu

                        title: Whatevr.I18n.i18nc("@action:inmenu mute a chat", "Mute")
                        icon.name: "notifications-disabled-symbolic"
                        // Hidden once the chat is muted; the Unmute item shows instead.
                        visible: !chatList.contextChatMuted

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
