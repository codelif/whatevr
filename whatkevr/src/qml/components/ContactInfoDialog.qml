pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Centered contact/group info dialog. For a 1:1 chat it shows the
// saved/push/business name, phone, avatar and "about"; for a group it shows the
// subject, description and a searchable, scrollable member list. Tapping a
// member drills into their card in place (with a back button); tapping the big
// avatar opens a full-screen viewer.
//
// The dialog holds no card state: it is a window onto the daemon's `contact` /
// `group` object views plus the `group_members` roster, subscribed for exactly
// as long as it is open. The card renders from whatever the daemon has locally
// and the network-fetched bits (a contact's about text, a group's live
// subject/description/roles) arrive later as ordinary upserts, so opening never
// blocks on a round-trip and nothing here has to merge two phases together.
CenteredDialog {
    id: root

    readonly property string cardKind: Whatevr.ProtocolController.infoCardKind
    readonly property bool isGroup: cardKind === "group"
    // The daemon's item for the open card, verbatim (empty until it lands).
    readonly property var card: Whatevr.ProtocolController.infoCard
    // The subject of the open card: a contact jid, or a group chat id.
    readonly property string subjectKey: Whatevr.ProtocolController.infoCardSubject
    readonly property string targetJid: isGroup ? "" : subjectKey
    readonly property bool loading: Whatevr.ProtocolController.infoCardLoading
    readonly property string errorText: Whatevr.ProtocolController.infoCardError

    // Contact fields.
    readonly property string phoneNumber: root.isGroup ? "" : String(root.card.phone ?? "")
    readonly property string savedName: root.isGroup ? "" : String(root.card.saved_name ?? "")
    readonly property string pushName: root.isGroup ? "" : String(root.card.push_name ?? "")
    readonly property string businessName: root.isGroup ? "" : String(root.card.business_name ?? "")
    readonly property bool isBusiness: root.isGroup ? false : Boolean(root.card.is_business ?? false)
    readonly property string statusText: root.isGroup ? "" : String(root.card.about ?? "")
    readonly property string avatarLocalPath: String(root.card.avatar_path ?? "")

    // Group fields.
    readonly property string subject: root.isGroup ? String(root.card.subject ?? "") : ""
    readonly property string description: root.isGroup ? String(root.card.description ?? "") : ""
    // The roster is authoritative once it has filled; until then the card's own
    // count (which the daemon computes) keeps the header honest.
    readonly property int memberCount: Whatevr.ProtocolController.groupMemberCount > 0
                                       ? Whatevr.ProtocolController.groupMemberCount
                                       : Number(root.card.member_count ?? 0)

    // Back-navigation stack of prior subjects (group → member drill-in). Each
    // entry only names a subject — going back re-subscribes, and the daemon
    // still holds the card, so there is nothing to snapshot.
    property var history: []

    readonly property real windowHeight: parent ? parent.height : Kirigami.Units.gridUnit * 40
    // Cap the member list so it (not the dialog's outer ScrollView) owns the
    // scrolling; keep everything else fixed.
    readonly property real memberListMax: Math.round(Math.max(
        Kirigami.Units.gridUnit * 8,
        Math.min(windowHeight * 0.45, Kirigami.Units.gridUnit * 18)))

    readonly property string primaryName: {
        if (isGroup) {
            return subject.length > 0 ? subject : Whatevr.I18n.i18nc("@info group fallback name", "Group")
        }
        if (savedName.length > 0) {
            return savedName
        }
        if (businessName.length > 0) {
            return businessName
        }
        if (pushName.length > 0) {
            return pushName
        }
        return phoneNumber.length > 0 ? phoneNumber : Whatevr.I18n.i18nc("@info contact fallback name", "Unknown")
    }
    // Secondary "~push name~" line, shown only when it adds information beyond
    // the saved name we already display as the title.
    readonly property string secondaryName: {
        if (isGroup || pushName.length === 0) {
            return ""
        }
        if (savedName.length === 0 || savedName === pushName) {
            return ""
        }
        return pushName
    }
    readonly property bool canMessage: !isGroup && targetJid.length > 0
                                       && targetJid !== Whatevr.ProtocolController.selectedChatId
    // Blocked state is membership in the daemon's `blocklist` view, composed in
    // by the controller — no local snapshot to refresh.
    readonly property bool contactBlocked: Whatevr.ProtocolController.infoCardBlocked
    readonly property bool canBlock: !isGroup && targetJid.length > 0
    // The chat whose media gallery this dialog can open. A group's subject key
    // is its chat id; a contact card only has one when that contact is the chat
    // currently open.
    readonly property string galleryChatId: {
        if (isGroup) {
            return subjectKey
        }
        return targetJid.length > 0 && targetJid === Whatevr.ProtocolController.selectedChatId
            ? targetJid
            : ""
    }

    title: isGroup
           ? Whatevr.I18n.i18nc("@title group info dialog", "Group info")
           : Whatevr.I18n.i18nc("@title contact info dialog", "Contact info")
    preferredWidth: Kirigami.Units.gridUnit * 24
    padding: Kirigami.Units.largeSpacing
    standardButtons: Kirigami.Dialog.Close

    // Kirigami.Dialog wraps content in a QQC2.ScrollView with its own scrollbar;
    // the member ListView already scrolls, so suppress the dialog's bar.
    Component.onCompleted: contentItem.ScrollBar.vertical.policy = ScrollBar.AlwaysOff

    // Entry point: open the dialog for a chat. params: { isGroup, targetChatId }
    // or { isGroup:false, targetJid }.
    function openFor(params) {
        root.history = []
        loadSubject(params)
        open()
    }

    // Point the dialog's subscriptions at a subject. Shared by openFor and the
    // in-place member drill-in; each call replaces the previous card.
    function loadSubject(params) {
        memberSearch.text = ""
        if (Boolean(params.isGroup)) {
            Whatevr.ProtocolController.openGroupCard(String(params.targetChatId || ""))
        } else {
            Whatevr.ProtocolController.openContactCard(String(params.targetJid || ""))
        }
    }

    // The card's subscriptions live exactly as long as the dialog is open.
    onClosed: {
        root.history = []
        Whatevr.ProtocolController.closeInfoCard()
    }

    function openMember(jid) {
        if (jid.length === 0) {
            return
        }
        const stack = root.history.slice()
        stack.push({ isGroup: root.isGroup, targetChatId: root.isGroup ? root.subjectKey : "",
                     targetJid: root.isGroup ? "" : root.subjectKey })
        root.history = stack
        loadSubject({ isGroup: false, targetJid: jid })
    }

    function goBack() {
        if (root.history.length === 0) {
            return
        }
        const stack = root.history.slice()
        const prev = stack.pop()
        root.history = stack
        loadSubject(prev)
    }

    function initialsForName(name) {
        const parts = name.trim().split(/\s+/)
        let initials = ""
        for (const part of parts) {
            if (part.length > 0) {
                initials += part[0].toUpperCase()
            }
            if (initials.length >= 2) {
                break
            }
        }
        return initials.length > 0 ? initials : "?"
    }

    // Member rows matching the search box, in the daemon's roster order.
    // PROTOCOL.md calls member search presentation-side filtering over rows the
    // frontend already has; the revision tick makes the read reactive.
    readonly property var members: Whatevr.ProtocolController.groupMembersRevision >= 0
                                   ? Whatevr.ProtocolController.groupMembers(memberSearch.text)
                                   : []

    Connections {
        target: Whatevr.ProtocolController

        function onProfilePictureReady(jid, localPath) {
            if (jid === root.subjectKey) {
                pictureViewer.showImage(localPath)
            }
        }
    }

    ProfilePictureViewer {
        id: pictureViewer
    }

    Kirigami.PromptDialog {
        id: blockConfirmDialog

        // Centre on the stable implicitHeight (see the mute dialog note in
        // ChatListPane).
        y: parent ? Math.round((parent.height - implicitHeight) / 2) : 0
        title: Whatevr.I18n.i18nc("@title:dialog", "Block contact")
        subtitle: Whatevr.I18n.i18nc("@info block confirmation",
                                     "Block %1? Blocked contacts can no longer send you messages or call you.",
                                     root.primaryName)
        standardButtons: Kirigami.Dialog.Cancel
        showCloseButton: false

        customFooterActions: [
            Kirigami.Action {
                text: Whatevr.I18n.i18nc("@action:button confirm blocking", "Block")
                icon.name: "im-ban-kick-user-symbolic"
                onTriggered: {
                    Whatevr.ProtocolController.setContactBlocked(root.targetJid, true)
                    blockConfirmDialog.close()
                }
            }
        ]
    }

    ColumnLayout {
        // Kirigami.Dialog does not give a bare content item a width; pin it to
        // the dialog's content width like the other in-app dialogs do.
        implicitWidth: root.preferredWidth - 2 * root.padding
        spacing: Kirigami.Units.largeSpacing

        // ---- Back row (only while drilled into a member) ----
        QQC2.ToolButton {
            Layout.alignment: Qt.AlignLeft
            visible: root.history.length > 0
            icon.name: "draw-arrow-back"
            text: Whatevr.I18n.i18nc("@action:button return to the previous info card", "Back")
            display: QQC2.AbstractButton.TextBesideIcon
            onClicked: root.goBack()
        }

        // ---- Header: big avatar + names ----
        ColumnLayout {
            Layout.fillWidth: true
            spacing: Kirigami.Units.smallSpacing

            AvatarImage {
                Layout.alignment: Qt.AlignHCenter
                Layout.preferredWidth: Kirigami.Units.gridUnit * 6
                Layout.preferredHeight: Kirigami.Units.gridUnit * 6
                avatarLocalPath: root.avatarLocalPath
                initials: root.initialsForName(root.primaryName)

                // MouseArea (not TapHandler) so the press is consumed and does
                // not bleed through to items behind the avatar.
                MouseArea {
                    anchors.fill: parent
                    enabled: !root.loading
                    cursorShape: Qt.PointingHandCursor
                    onClicked: Whatevr.ProtocolController.viewProfilePicture(root.subjectKey)
                }
            }

            QQC2.Label {
                Layout.alignment: Qt.AlignHCenter
                Layout.fillWidth: true
                horizontalAlignment: Text.AlignHCenter
                text: root.primaryName
                font.pointSize: Kirigami.Theme.defaultFont.pointSize * 1.4
                font.weight: Font.DemiBold
                elide: Text.ElideRight
                wrapMode: Text.NoWrap
            }

            QQC2.Label {
                Layout.alignment: Qt.AlignHCenter
                visible: root.secondaryName.length > 0
                text: Whatevr.I18n.i18nc("@info push name, shown when a saved name also exists", "~%1~", root.secondaryName)
                color: Kirigami.Theme.disabledTextColor
                font.italic: true
            }

            RowLayout {
                Layout.alignment: Qt.AlignHCenter
                visible: root.isBusiness
                spacing: Kirigami.Units.smallSpacing

                Kirigami.Icon {
                    source: "feed-subscribe-symbolic"
                    implicitWidth: Kirigami.Units.iconSizes.small
                    implicitHeight: Kirigami.Units.iconSizes.small
                    color: Kirigami.Theme.positiveTextColor
                }
                QQC2.Label {
                    text: Whatevr.I18n.i18nc("@info business account badge", "Business account")
                    color: Kirigami.Theme.positiveTextColor
                    font: Kirigami.Theme.smallFont
                }
            }
        }

        QQC2.BusyIndicator {
            Layout.alignment: Qt.AlignHCenter
            running: root.loading
            visible: running
        }

        Kirigami.InlineMessage {
            Layout.fillWidth: true
            type: Kirigami.MessageType.Error
            visible: root.errorText.length > 0
            text: root.errorText
        }

        // ---- Details: phone / about / description ----
        Kirigami.FormLayout {
            Layout.fillWidth: true
            visible: !root.loading

            QQC2.Label {
                Kirigami.FormData.label: Whatevr.I18n.i18nc("@label:textbox", "Phone")
                visible: !root.isGroup && root.phoneNumber.length > 0
                text: root.phoneNumber
                textFormat: Text.PlainText
            }

            QQC2.Label {
                Kirigami.FormData.label: Whatevr.I18n.i18nc("@label:textbox contact about/status text", "About")
                visible: !root.isGroup && root.statusText.length > 0
                text: root.statusText
                textFormat: Text.PlainText
                wrapMode: Text.Wrap
                Layout.maximumWidth: root.preferredWidth - Kirigami.Units.gridUnit * 8
            }

            QQC2.Label {
                Kirigami.FormData.label: Whatevr.I18n.i18nc("@label:textbox group description", "Description")
                visible: root.isGroup && root.description.length > 0
                text: root.description
                textFormat: Text.PlainText
                wrapMode: Text.Wrap
                Layout.maximumWidth: root.preferredWidth - Kirigami.Units.gridUnit * 8
            }
        }

        // ---- Message / block actions (1:1 only) ----
        RowLayout {
            Layout.alignment: Qt.AlignHCenter
            spacing: Kirigami.Units.largeSpacing
            visible: root.canMessage || root.canBlock

            QQC2.Button {
                visible: root.canMessage
                icon.name: "mail-message-new-symbolic"
                text: Whatevr.I18n.i18nc("@action:button start a chat with this contact", "Message")
                onClicked: {
                    Whatevr.ProtocolController.startDirectChat(root.targetJid)
                    root.close()
                    applicationWindow().showConversation()
                }
            }

            QQC2.Button {
                visible: root.canBlock
                icon.name: "im-ban-kick-user-symbolic"
                text: root.contactBlocked
                      ? Whatevr.I18n.i18nc("@action:button unblock this contact", "Unblock")
                      : Whatevr.I18n.i18nc("@action:button block this contact", "Block")
                onClicked: {
                    if (root.contactBlocked) {
                        // Unblocking is low-stakes; act directly, like the
                        // blocked-contacts settings page does.
                        Whatevr.ProtocolController.setContactBlocked(root.targetJid, false)
                    } else {
                        blockConfirmDialog.open()
                    }
                }
            }
        }

        // ---- Media, links and documents ----
        // The gallery is per chat, so it only makes sense where the dialog was
        // opened from a chat rather than from a bare contact card.
        QQC2.Button {
            Layout.alignment: Qt.AlignHCenter
            visible: root.galleryChatId.length > 0
            icon.name: "folder-images-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Media, links and documents")
            onClicked: {
                const chatId = root.galleryChatId
                const chatName = root.primaryName
                root.close()
                applicationWindow().pageStack.layers.push(Qt.resolvedUrl("ChatMediaGalleryPage.qml"), {
                    chatId: chatId,
                    chatName: chatName
                })
            }
        }

        // ---- Group members ----
        Kirigami.ListSectionHeader {
            Layout.fillWidth: true
            visible: root.isGroup && !root.loading
            text: Whatevr.I18n.i18ncp("@title:group number of group members",
                                      "%1 member", "%1 members", root.memberCount)
        }

        Kirigami.SearchField {
            id: memberSearch
            Layout.fillWidth: true
            visible: root.isGroup && root.memberCount > 0
            placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Search members…")
        }

        ListView {
            id: memberList
            Layout.fillWidth: true
            Layout.preferredHeight: Math.min(contentHeight, root.memberListMax)
            visible: root.isGroup && count > 0
            clip: true
            model: root.members
            reuseItems: true
            ScrollBar.vertical: DiscreetScrollBar {}

            delegate: QQC2.ItemDelegate {
                id: memberDelegate

                // One daemon `group_members` row, verbatim.
                required property var modelData

                readonly property string jid: String(memberDelegate.modelData.jid ?? "")
                readonly property string displayName: String(memberDelegate.modelData.display_name ?? "")
                readonly property string phoneNumber: String(memberDelegate.modelData.phone ?? "")
                readonly property string avatarLocalPath: String(memberDelegate.modelData.avatar_path ?? "")
                readonly property string role: String(memberDelegate.modelData.role ?? "member")

                width: ListView.view.width
                onClicked: root.openMember(memberDelegate.jid)

                contentItem: RowLayout {
                    spacing: Kirigami.Units.largeSpacing

                    AvatarImage {
                        Layout.preferredWidth: Kirigami.Units.gridUnit * 2.2
                        Layout.preferredHeight: Kirigami.Units.gridUnit * 2.2
                        avatarLocalPath: memberDelegate.avatarLocalPath
                        initials: root.initialsForName(memberDelegate.displayName.length > 0
                                                       ? memberDelegate.displayName
                                                       : memberDelegate.phoneNumber)
                    }

                    ColumnLayout {
                        Layout.fillWidth: true
                        spacing: 0

                        QQC2.Label {
                            Layout.fillWidth: true
                            text: memberDelegate.displayName.length > 0
                                  ? memberDelegate.displayName
                                  : memberDelegate.phoneNumber
                            elide: Text.ElideRight
                            font.weight: Font.DemiBold
                        }

                        QQC2.Label {
                            Layout.fillWidth: true
                            visible: memberDelegate.phoneNumber.length > 0
                                     && memberDelegate.displayName.length > 0
                            text: memberDelegate.phoneNumber
                            elide: Text.ElideRight
                            color: Kirigami.Theme.disabledTextColor
                            font: Kirigami.Theme.smallFont
                        }
                    }

                    QQC2.Label {
                        visible: memberDelegate.role === "admin" || memberDelegate.role === "superadmin"
                        text: memberDelegate.role === "superadmin"
                              ? Whatevr.I18n.i18nc("@info group owner badge", "Owner")
                              : Whatevr.I18n.i18nc("@info group admin badge", "Admin")
                        color: Kirigami.Theme.positiveTextColor
                        font: Kirigami.Theme.smallFont
                    }
                }
            }
        }
    }
}
