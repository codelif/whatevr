pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Multi-select chat picker for forwarding messages. The daemon accepts at
// most five target chats per forward, mirroring WhatsApp.
CenteredDialog {
    id: root

    readonly property int maxTargets: 5
    // Fixed content width used for both the dialog's preferredWidth and the
    // ListView's implicitWidth. Pointing the ListView at root.preferredWidth fed
    // the dialog's own size back into its content's implicit size, destabilising
    // the dialog's centred geometry (binding loop on Kirigami Dialog's `y`).
    readonly property real pickerWidth: Kirigami.Units.gridUnit * 22

    property var messageIds: []
    property var selectedChatIds: ({})
    property int selectedChatCount: 0
    property int selectionRevision: 0

    signal forwardConfirmed(var messageIds, var chatIds)

    title: Whatevr.I18n.i18nc("@title:dialog", "Forward to…")
    standardButtons: Kirigami.Dialog.Cancel
    preferredWidth: root.pickerWidth
    preferredHeight: Kirigami.Units.gridUnit * 26

    // Kirigami.Dialog wraps content in a QQC2.ScrollView with its own vertical
    // scrollbar; our chat ListView already scrolls, so suppress the dialog's
    // duplicate bar (otherwise the whole interface scrolls as one).
    Component.onCompleted: contentItem.ScrollBar.vertical.policy = ScrollBar.AlwaysOff

    function openFor(ids) {
        messageIds = ids
        selectedChatIds = ({})
        selectedChatCount = 0
        selectionRevision += 1
        searchField.text = ""
        open()
    }

    function isChatSelected(chatId) {
        return selectedChatIds[chatId] === true
    }

    function toggleChat(chatId) {
        if (chatId.length === 0) {
            return
        }
        const next = Object.assign({}, selectedChatIds)
        if (next[chatId] === true) {
            delete next[chatId]
            selectedChatCount = Math.max(0, selectedChatCount - 1)
        } else {
            if (selectedChatCount >= maxTargets) {
                return
            }
            next[chatId] = true
            selectedChatCount += 1
        }
        selectedChatIds = next
        selectionRevision += 1
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

    Whatevr.ChatListFilterModel {
        id: chatFilterModel
        sourceModel: Whatevr.AppController.chatListModel
        filterText: searchField.text
    }

    ColumnLayout {
        // Pin the content width like the other in-app dialogs; Kirigami.Dialog
        // does not give a bare content item a width on its own.
        implicitWidth: root.pickerWidth
        spacing: Kirigami.Units.smallSpacing

        Kirigami.SearchField {
            id: searchField
            Layout.fillWidth: true
            placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Search chats…")
        }

        ListView {
            id: chatList

            Layout.fillWidth: true
            // Bounded height so this ListView (not the dialog's outer
            // ScrollView) owns the scrolling; the search field and footer stay
            // fixed and only the chat list scrolls.
            Layout.preferredHeight: Kirigami.Units.gridUnit * 20
            clip: true
            model: chatFilterModel
            reuseItems: true
            ScrollBar.vertical: DiscreetScrollBar {}

            delegate: ItemDelegate {
                id: chatDelegate

                required property var model

                readonly property string chatId: String(model.chatId || "")
                readonly property bool selected: root.selectionRevision >= 0 && root.isChatSelected(chatId)
                readonly property bool selectable: selected || root.selectedChatCount < root.maxTargets

                width: ListView.view.width
                enabled: selectable
                onClicked: root.toggleChat(chatId)

                contentItem: RowLayout {
                    spacing: Kirigami.Units.smallSpacing

                    CheckBox {
                        checked: chatDelegate.selected
                        enabled: chatDelegate.selectable
                        onToggled: root.toggleChat(chatDelegate.chatId)
                    }

                    AvatarImage {
                        Layout.preferredWidth: Kirigami.Units.gridUnit * 1.65
                        Layout.preferredHeight: Kirigami.Units.gridUnit * 1.65
                        avatarLocalPath: String(chatDelegate.model.avatarLocalPath || "")
                        initials: String(chatDelegate.model.initials || "?")
                    }

                    Label {
                        text: String(chatDelegate.model.name || "")
                        elide: Text.ElideRight
                        Layout.fillWidth: true
                    }

                    Kirigami.Icon {
                        visible: Boolean(chatDelegate.model.isGroup)
                        source: "system-users-symbolic"
                        Layout.preferredWidth: Kirigami.Units.iconSizes.small
                        Layout.preferredHeight: Kirigami.Units.iconSizes.small
                        color: Kirigami.Theme.disabledTextColor
                    }
                }
            }
        }
    }
}
