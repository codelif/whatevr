pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import org.kde.kirigami.dialogs as KDialogs
import Whatevr as Whatevr

// Multi-select chat picker for forwarding messages. The daemon accepts at most
// five target chats per forward, mirroring WhatsApp: selected chats show as
// removable chips, search hides behind a button in the title bar.
//
// Scroll architecture mirrors the rest of the app rather than fighting
// Kirigami.Dialog's wrapper ScrollView: the dialog sizes itself to its content
// (no forced preferredHeight) and the chat list is capped at `listMax`, so the
// dialog's outer Flickable always fits and never scrolls — only the inner list
// does. The list uses the same flick wiring as ChatListPane (Qt.NoButton +
// KineticWheelScroller + DiscreetScrollBar) so its kinetic feel is identical.
CenteredDialog {
    id: root

    readonly property int maxTargets: 5
    // Fixed content width used for both the dialog's preferredWidth and the
    // ListView's implicitWidth. Pointing the ListView at root.preferredWidth fed
    // the dialog's own size back into its content's implicit size, destabilising
    // the dialog's centred geometry (binding loop on Kirigami Dialog's `y`).
    readonly property real pickerWidth: Kirigami.Units.gridUnit * 22
    // Tallest the chat list grows before it scrolls internally. Keeping the list
    // (and therefore the whole dialog) bounded means the dialog's outer
    // ScrollView Flickable always fits its content and never scrolls itself.
    readonly property real listMax: Kirigami.Units.gridUnit * 16

    property var messageIds: []
    // chatId -> { name, avatarLocalPath, initials }. Keeping the display fields
    // lets the chip row render selections without re-querying the model.
    property var selectedChatIds: ({})
    property var selectedList: []
    property int selectedChatCount: 0
    property int selectionRevision: 0
    property bool searchExpanded: false
    // Candidate targets, straight from the daemon's `chats` view in its order.
    // The search box narrows the rows the dialog already holds — presentation-
    // side filtering, no sorting or merging (rule 1). The revision tick is what
    // makes this re-evaluate when the view changes.
    readonly property var chatTargets: Whatevr.ProtocolController.forwardTargetsRevision >= 0
                                       ? Whatevr.ProtocolController.forwardChatTargets(searchField.text)
                                       : []

    signal forwardConfirmed(var messageIds, var chatIds)

    title: Whatevr.I18n.i18nc("@title:dialog", "Forward to…")
    standardButtons: Kirigami.Dialog.Cancel
    preferredWidth: root.pickerWidth

    // Kirigami.Dialog wraps content in a QQC2.ScrollView with its own scrollbar;
    // the chat ListView already scrolls, so suppress the dialog's bar. The list
    // is capped so the dialog's Flickable itself never needs to scroll.
    Component.onCompleted: contentItem.ScrollBar.vertical.policy = ScrollBar.AlwaysOff

    onSearchExpandedChanged: {
        if (searchExpanded) {
            searchField.forceActiveFocus()
        } else {
            searchField.text = ""
            chatList.positionViewAtBeginning()
        }
    }

    function openFor(ids) {
        messageIds = ids
        selectedChatIds = ({})
        selectedList = []
        selectedChatCount = 0
        selectionRevision += 1
        searchExpanded = false
        searchField.text = ""
        chatList.positionViewAtBeginning()
        // The picker's own `chats` subscription lives exactly as long as the
        // dialog does; the daemon owns the rows and their order.
        Whatevr.ProtocolController.openForwardTargets()
        open()
    }

    onClosed: Whatevr.ProtocolController.closeForwardTargets()

    // Presentation-only avatar fallback: the daemon chat row carries no
    // precomputed initials (same helper as the chat list).
    function initialsFor(name) {
        const parts = (name || "").trim().split(/\s+/).filter(p => p.length > 0)
        if (parts.length === 0)
            return "?"
        let initials = parts[0].charAt(0)
        if (parts.length > 1)
            initials += parts[parts.length - 1].charAt(0)
        return initials.toUpperCase()
    }

    function isChatSelected(chatId) {
        return selectedChatIds[chatId] !== undefined
    }

    function rebuildSelectedList() {
        const out = []
        for (const id in selectedChatIds) {
            const info = selectedChatIds[id]
            out.push({ chatId: id, name: info.name,
                       avatarLocalPath: info.avatarLocalPath, initials: info.initials })
        }
        selectedList = out
    }

    function chatInfo(chat) {
        return {
            name: String(chat.name || ""),
            avatarLocalPath: String(chat.avatar_path || ""),
            initials: root.initialsFor(String(chat.name || ""))
        }
    }

    function toggleChat(chatId, info) {
        if (chatId.length === 0) {
            return
        }
        const next = Object.assign({}, selectedChatIds)
        if (next[chatId] !== undefined) {
            delete next[chatId]
            selectedChatCount = Math.max(0, selectedChatCount - 1)
        } else {
            if (selectedChatCount >= maxTargets) {
                return
            }
            next[chatId] = info || { name: "", avatarLocalPath: "", initials: "?" }
            selectedChatCount += 1
        }
        selectedChatIds = next
        selectionRevision += 1
        rebuildSelectedList()
    }

    customFooterActions: [
        Kirigami.Action {
            text: root.selectedChatCount > 1
                  ? Whatevr.I18n.i18nc("@action:button", "Forward to %1 Chats", root.selectedChatCount)
                  : Whatevr.I18n.i18nc("@action:button", "Forward")
            icon.name: "mail-forward-symbolic"
            enabled: root.selectedChatCount > 0
            onTriggered: {
                root.forwardConfirmed(root.messageIds, Object.keys(root.selectedChatIds))
                root.close()
            }
        }
    ]

    // Custom header so the search toggle sits in the title bar, right beside the
    // close button (Kirigami.Dialog offers no header-trailing slot otherwise).
    header: KDialogs.DialogHeader {
        dialog: root
        contentItem: RowLayout {
            spacing: Kirigami.Units.smallSpacing

            Kirigami.Heading {
                Layout.fillWidth: true
                Layout.alignment: Qt.AlignVCenter
                text: root.title.length === 0 ? " " : root.title
                textFormat: Text.PlainText
                elide: Text.ElideRight
            }

            Label {
                Layout.alignment: Qt.AlignVCenter
                visible: root.selectedChatCount > 0
                text: Whatevr.I18n.i18nc("@info:status selected forward targets, e.g. 2/5",
                                         "%1/%2", root.selectedChatCount, root.maxTargets)
                color: root.selectedChatCount >= root.maxTargets
                       ? Kirigami.Theme.neutralTextColor
                       : Kirigami.Theme.disabledTextColor
                font: Kirigami.Theme.smallFont
            }

            ToolButton {
                Layout.alignment: Qt.AlignRight | Qt.AlignTop
                icon.name: "search-symbolic"
                display: AbstractButton.IconOnly
                checkable: true
                checked: root.searchExpanded
                text: Whatevr.I18n.i18nc("@action:button toggle chat search", "Search chats")
                ToolTip.visible: hovered
                ToolTip.text: text
                onToggled: root.searchExpanded = checked
            }

            ToolButton {
                Layout.alignment: Qt.AlignRight | Qt.AlignTop
                icon.name: hovered ? "window-close" : "window-close-symbolic"
                display: AbstractButton.IconOnly
                visible: root.showCloseButton
                text: Whatevr.I18n.i18nc("@action:button close dialog", "Close")
                ToolTip.visible: hovered
                ToolTip.text: text
                onClicked: root.reject()
            }
        }
    }

    // Removable selection pill shown in the chip strip.
    component SelectedChip: Control {
        id: chip

        required property string chipChatId
        required property string chipName
        required property string chipAvatar
        required property string chipInitials

        leftPadding: Kirigami.Units.smallSpacing
        rightPadding: Kirigami.Units.smallSpacing
        topPadding: Math.round(Kirigami.Units.smallSpacing / 2)
        bottomPadding: Math.round(Kirigami.Units.smallSpacing / 2)

        background: Rectangle {
            radius: height / 2
            color: Qt.alpha(Kirigami.Theme.highlightColor, 0.18)
            border.width: 1
            border.color: Qt.alpha(Kirigami.Theme.highlightColor, 0.35)
        }

        contentItem: RowLayout {
            spacing: Kirigami.Units.smallSpacing

            AvatarImage {
                Layout.preferredWidth: Kirigami.Units.gridUnit * 1.1
                Layout.preferredHeight: Kirigami.Units.gridUnit * 1.1
                avatarLocalPath: chip.chipAvatar
                initials: chip.chipInitials
            }

            Label {
                text: chip.chipName
                elide: Text.ElideRight
                Layout.maximumWidth: Kirigami.Units.gridUnit * 8
                font: Kirigami.Theme.smallFont
            }

            ToolButton {
                icon.name: "edit-clear-symbolic"
                display: AbstractButton.IconOnly
                icon.width: Kirigami.Units.iconSizes.small
                icon.height: Kirigami.Units.iconSizes.small
                Layout.preferredWidth: Kirigami.Units.iconSizes.small + Kirigami.Units.smallSpacing
                Layout.preferredHeight: Layout.preferredWidth
                text: Whatevr.I18n.i18nc("@action:button remove forward target", "Remove")
                ToolTip.visible: hovered
                ToolTip.text: text
                onClicked: root.toggleChat(chip.chipChatId)
            }
        }
    }

    ColumnLayout {
        // Pin the content width like the other in-app dialogs; Kirigami.Dialog
        // does not give a bare content item a width on its own.
        implicitWidth: root.pickerWidth
        spacing: Kirigami.Units.smallSpacing

        // Selected-chat chips. Height animates so the strip grows/shrinks
        // smoothly instead of snapping the dialog around.
        Flow {
            id: chipFlow

            Layout.fillWidth: true
            Layout.preferredHeight: root.selectedChatCount > 0 ? implicitHeight : 0
            clip: true
            spacing: Kirigami.Units.smallSpacing

            Behavior on Layout.preferredHeight {
                NumberAnimation {
                    duration: Kirigami.Units.shortDuration
                    easing.type: Easing.OutCubic
                }
            }

            Repeater {
                model: root.selectedList

                delegate: SelectedChip {
                    required property var modelData
                    chipChatId: String(modelData.chatId)
                    chipName: String(modelData.name)
                    chipAvatar: String(modelData.avatarLocalPath || "")
                    chipInitials: String(modelData.initials || "?")
                }
            }
        }

        // Retractable search, revealed by the title-bar button.
        Kirigami.SearchField {
            id: searchField

            Layout.fillWidth: true
            Layout.preferredHeight: root.searchExpanded ? implicitHeight : 0
            Layout.bottomMargin: root.searchExpanded ? Kirigami.Units.smallSpacing : 0
            opacity: root.searchExpanded ? 1 : 0
            clip: true
            placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Search chats…")
            Keys.onEscapePressed: root.searchExpanded = false

            Behavior on Layout.preferredHeight {
                NumberAnimation {
                    duration: Kirigami.Units.shortDuration
                    easing.type: Easing.OutCubic
                }
            }
            Behavior on opacity {
                NumberAnimation {
                    duration: Kirigami.Units.shortDuration
                    easing.type: Easing.OutCubic
                }
            }
        }

        // Viewport for the chat list. The list is capped at listMax so the dialog
        // stays bounded and its outer Flickable never scrolls; the inner list owns
        // all scrolling, driven by KineticWheelScroller to match the app's lists.
        Item {
            id: chatListViewport

            Layout.fillWidth: true
            Layout.preferredHeight: chatList.Layout.preferredHeight

            ListView {
                id: chatList

                anchors.fill: parent
                Layout.preferredHeight: Math.min(contentHeight, root.listMax)
                clip: true
                model: root.chatTargets
                currentIndex: -1
                boundsBehavior: Flickable.StopAtBounds
                flickableDirection: Flickable.VerticalFlick
                acceptedButtons: Qt.NoButton
                reuseItems: true
                spacing: 0
                ScrollBar.vertical: DiscreetScrollBar {}

                // Empty state when a search filters everything out.
                Label {
                    anchors.centerIn: parent
                    width: parent.width - Kirigami.Units.largeSpacing * 2
                    visible: root.chatTargets.length === 0
                    horizontalAlignment: Text.AlignHCenter
                    wrapMode: Text.WordWrap
                    color: Kirigami.Theme.disabledTextColor
                    text: searchField.text.length > 0
                          ? Whatevr.I18n.i18nc("@info:placeholder no search results", "No chats found")
                          : Whatevr.I18n.i18nc("@info:placeholder no chats", "No chats available")
                }

                delegate: ItemDelegate {
                    id: chatDelegate

                    required property var modelData

                    readonly property string chatId: String(modelData.id || "")
                    readonly property bool selected: root.selectionRevision >= 0 && root.isChatSelected(chatId)
                    readonly property bool selectable: selected || root.selectedChatCount < root.maxTargets

                    width: ListView.view.width
                    enabled: selectable
                    highlighted: selected
                    onClicked: root.toggleChat(chatId, root.chatInfo(modelData))

                    contentItem: RowLayout {
                        spacing: Kirigami.Units.smallSpacing

                        CheckBox {
                            checked: chatDelegate.selected
                            enabled: chatDelegate.selectable
                            onToggled: root.toggleChat(chatDelegate.chatId, root.chatInfo(chatDelegate.modelData))
                        }

                        AvatarImage {
                            Layout.preferredWidth: Kirigami.Units.gridUnit * 1.65
                            Layout.preferredHeight: Kirigami.Units.gridUnit * 1.65
                            avatarLocalPath: String(chatDelegate.modelData.avatar_path || "")
                            initials: root.initialsFor(String(chatDelegate.modelData.name || ""))
                        }

                        Label {
                            text: String(chatDelegate.modelData.name || "")
                            elide: Text.ElideRight
                            Layout.fillWidth: true
                        }

                        Kirigami.Icon {
                            visible: Boolean(chatDelegate.modelData.is_group)
                            source: "system-users-symbolic"
                            Layout.preferredWidth: Kirigami.Units.iconSizes.small
                            Layout.preferredHeight: Kirigami.Units.iconSizes.small
                            color: Kirigami.Theme.disabledTextColor
                        }
                    }
                }
            }

            KineticWheelScroller {
                anchors.fill: chatList
                target: chatList
                wheelStep: Kirigami.Units.gridUnit * 4
            }
        }
    }
}
