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
// The card is shown immediately from whatever the daemon returns locally, then
// the network-fetched bits (a contact's status text; a group's live
// subject/description/roles) stream in via contactInfoUpdated/groupInfoUpdated
// and are merged in place, so opening never blocks on a round-trip.
CenteredDialog {
    id: root

    // Exactly one of these identifies the current subject.
    property string targetJid: ""
    property string targetChatId: ""
    property bool isGroup: false

    property string subjectKey: isGroup ? targetChatId : targetJid
    property bool loading: true
    property string errorText: ""

    // Contact fields.
    property string phoneNumber: ""
    property string savedName: ""
    property string pushName: ""
    property string businessName: ""
    property string avatarLocalPath: ""
    property bool isBusiness: false
    property string statusText: ""

    // Group fields.
    property string subject: ""
    property string description: ""
    property int memberCount: 0
    property var allMembers: []

    // Back-navigation stack of prior subjects (group → member drill-in). Each
    // entry is a full snapshot of the card state so going back is instant and
    // needs no re-fetch.
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
                                       && targetJid !== Whatevr.AppController.selectedChatId

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

    // (Re)load the card for a subject, resetting all fields and kicking off the
    // daemon fetch. Shared by openFor and the in-place member drill-in.
    function loadSubject(params) {
        root.isGroup = Boolean(params.isGroup)
        root.targetJid = String(params.targetJid || "")
        root.targetChatId = String(params.targetChatId || "")
        root.loading = true
        root.errorText = ""
        root.phoneNumber = ""
        root.savedName = ""
        root.pushName = ""
        root.businessName = ""
        root.avatarLocalPath = ""
        root.isBusiness = false
        root.statusText = ""
        root.subject = ""
        root.description = ""
        root.memberCount = 0
        root.allMembers = []
        memberModel.clear()
        memberSearch.text = ""

        if (root.isGroup) {
            Whatevr.AppController.openGroupInfo(root.targetChatId)
        } else {
            Whatevr.AppController.openContactInfo(root.targetJid)
        }
    }

    function snapshot() {
        return {
            isGroup: root.isGroup,
            targetJid: root.targetJid,
            targetChatId: root.targetChatId,
            loading: root.loading,
            errorText: root.errorText,
            phoneNumber: root.phoneNumber,
            savedName: root.savedName,
            pushName: root.pushName,
            businessName: root.businessName,
            avatarLocalPath: root.avatarLocalPath,
            isBusiness: root.isBusiness,
            statusText: root.statusText,
            subject: root.subject,
            description: root.description,
            memberCount: root.memberCount,
            allMembers: root.allMembers,
        }
    }

    function restore(s) {
        root.isGroup = s.isGroup
        root.targetJid = s.targetJid
        root.targetChatId = s.targetChatId
        root.loading = s.loading
        root.errorText = s.errorText
        root.phoneNumber = s.phoneNumber
        root.savedName = s.savedName
        root.pushName = s.pushName
        root.businessName = s.businessName
        root.avatarLocalPath = s.avatarLocalPath
        root.isBusiness = s.isBusiness
        root.statusText = s.statusText
        root.subject = s.subject
        root.description = s.description
        root.memberCount = s.memberCount
        root.allMembers = s.allMembers
        memberSearch.text = ""
        root.applyMemberFilter("")
    }

    function openMember(jid) {
        if (jid.length === 0) {
            return
        }
        const stack = root.history.slice()
        stack.push(root.snapshot())
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
        restore(prev)
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

    function applyMemberFilter(text) {
        const needle = text.trim().toLowerCase()
        // Collect the matching members first, then swap the model in one
        // clear+refill. (A ListView absorbs a model reset cleanly; the old
        // Repeater-in-a-Column did not, which is what produced the
        // "DelegateModel::cancel: index out range" warnings.)
        const wanted = []
        for (const member of root.allMembers) {
            if (needle.length === 0
                    || String(member.displayName).toLowerCase().includes(needle)
                    || String(member.phoneNumber).toLowerCase().includes(needle)) {
                wanted.push(member)
            }
        }
        memberModel.clear()
        for (const member of wanted) {
            memberModel.append(member)
        }
    }

    ListModel {
        id: memberModel
    }

    Connections {
        target: Whatevr.AppController

        function onContactInfoReceived(info) {
            // The daemon may answer with the canonical phone JID rather than the
            // JID we requested (an @lid mention resolves to @s.whatsapp.net), so
            // match on either and then adopt the canonical JID — later avatar /
            // status updates are published against it.
            if (root.isGroup
                    || (info.jid !== root.targetJid && info.requestedJid !== root.targetJid)) {
                return
            }
            root.targetJid = info.jid
            root.phoneNumber = info.phoneNumber
            root.savedName = info.savedName
            root.pushName = info.pushName
            root.businessName = info.businessName
            root.avatarLocalPath = info.avatarLocalPath
            root.isBusiness = info.isBusiness
            root.statusText = info.statusText
            root.loading = false
        }

        function onContactInfoFailed(jid, errorText) {
            if (root.isGroup || jid !== root.targetJid) {
                return
            }
            root.errorText = errorText
            root.loading = false
        }

        function onGroupInfoReceived(info) {
            if (!root.isGroup || info.chatId !== root.targetChatId) {
                return
            }
            root.subject = info.subject
            root.description = info.description
            root.avatarLocalPath = info.avatarLocalPath
            root.allMembers = info.members
            root.memberCount = info.members.length
            root.applyMemberFilter(memberSearch.text)
            root.loading = false
        }

        function onGroupInfoFailed(chatId, errorText) {
            if (!root.isGroup || chatId !== root.targetChatId) {
                return
            }
            root.errorText = errorText
            root.loading = false
        }

        // Late-arriving "about" text for the contact currently shown.
        function onContactInfoUpdated(jid, statusText) {
            if (!root.isGroup && jid === root.targetJid) {
                root.statusText = statusText
            }
        }

        // Live group fields land after the stored card; merge subject /
        // description / roles / creation time without disturbing the avatar.
        function onGroupInfoUpdated(info) {
            if (!root.isGroup || info.chatId !== root.targetChatId) {
                return
            }
            if (info.subject.length > 0) {
                root.subject = info.subject
            }
            root.description = info.description
            if (info.members.length > 0) {
                root.allMembers = info.members
                root.memberCount = info.members.length
                root.applyMemberFilter(memberSearch.text)
            }
        }

        // Avatars stream in asynchronously; patch the header and member rows in
        // place as they land.
        function onSenderAvatarUpdated(senderId, avatarLocalPath) {
            if (!root.isGroup && senderId === root.targetJid) {
                root.avatarLocalPath = avatarLocalPath
            }
            for (let i = 0; i < memberModel.count; ++i) {
                if (memberModel.get(i).jid === senderId) {
                    memberModel.setProperty(i, "avatarLocalPath", avatarLocalPath)
                }
            }
            const updated = root.allMembers.slice()
            for (let j = 0; j < updated.length; ++j) {
                if (updated[j].jid === senderId) {
                    updated[j].avatarLocalPath = avatarLocalPath
                }
            }
            root.allMembers = updated
        }

        function onProfilePictureReady(jid, localPath) {
            if (jid === root.subjectKey || jid === root.targetJid) {
                pictureViewer.showImage(localPath)
            }
        }
    }

    ProfilePictureViewer {
        id: pictureViewer
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

                TapHandler {
                    enabled: !root.loading
                    onTapped: Whatevr.AppController.viewProfilePicture(root.subjectKey)
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

        // ---- Message action (1:1 only) ----
        QQC2.Button {
            Layout.alignment: Qt.AlignHCenter
            visible: root.canMessage
            icon.name: "mail-message-new-symbolic"
            text: Whatevr.I18n.i18nc("@action:button start a chat with this contact", "Message")
            onClicked: {
                Whatevr.AppController.startDirectChat(root.targetJid)
                root.close()
                applicationWindow().showConversation()
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
            onTextChanged: root.applyMemberFilter(text)
        }

        ListView {
            id: memberList
            Layout.fillWidth: true
            Layout.preferredHeight: Math.min(contentHeight, root.memberListMax)
            visible: root.isGroup && memberModel.count > 0
            clip: true
            model: memberModel
            reuseItems: true
            ScrollBar.vertical: DiscreetScrollBar {}

            delegate: QQC2.ItemDelegate {
                id: memberDelegate

                required property string jid
                required property string displayName
                required property string phoneNumber
                required property string avatarLocalPath
                required property bool isAdmin

                width: ListView.view.width
                onClicked: root.openMember(jid)

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
                        visible: memberDelegate.isAdmin
                        text: Whatevr.I18n.i18nc("@info group admin badge", "Admin")
                        color: Kirigami.Theme.positiveTextColor
                        font: Kirigami.Theme.smallFont
                    }
                }
            }
        }
    }
}
