import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Privacy")

    Component.onCompleted: Whatevr.AppController.refreshPrivacySettings()

    // PrivacyAudience enum values from the proto: 1 everyone, 2 contacts,
    // 4 nobody, 5 match-last-seen, 6 known.
    readonly property var everyoneContactsNobody: [
        { value: 1, text: Whatevr.I18n.i18nc("@item privacy audience", "Everyone") },
        { value: 2, text: Whatevr.I18n.i18nc("@item privacy audience", "My contacts") },
        { value: 4, text: Whatevr.I18n.i18nc("@item privacy audience", "Nobody") }
    ]
    readonly property var onlineModel: [
        { value: 1, text: Whatevr.I18n.i18nc("@item privacy audience", "Everyone") },
        { value: 5, text: Whatevr.I18n.i18nc("@item privacy audience", "Same as last seen") }
    ]
    readonly property var callModel: [
        { value: 1, text: Whatevr.I18n.i18nc("@item privacy audience", "Everyone") },
        { value: 6, text: Whatevr.I18n.i18nc("@item privacy audience", "Known contacts") }
    ]

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Who can see my…")
    }

    FormCard.FormCard {
        PrivacyAudienceCombo {
            objectName: "privacy.lastSeen"
            text: Whatevr.I18n.i18nc("@label:listbox", "Last seen")
            categoryKey: "lastSeen"
            audienceModel: page.everyoneContactsNobody
        }

        FormCard.FormDelegateSeparator {}

        PrivacyAudienceCombo {
            objectName: "privacy.online"
            text: Whatevr.I18n.i18nc("@label:listbox", "Online")
            categoryKey: "online"
            audienceModel: page.onlineModel
        }

        FormCard.FormDelegateSeparator {}

        PrivacyAudienceCombo {
            objectName: "privacy.profilePhoto"
            text: Whatevr.I18n.i18nc("@label:listbox", "Profile photo")
            categoryKey: "profilePhoto"
            audienceModel: page.everyoneContactsNobody
        }

        FormCard.FormDelegateSeparator {}

        PrivacyAudienceCombo {
            objectName: "privacy.about"
            text: Whatevr.I18n.i18nc("@label:listbox", "About")
            categoryKey: "about"
            audienceModel: page.everyoneContactsNobody
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Messaging")
    }

    FormCard.FormCard {
        FormCard.FormSwitchDelegate {
            objectName: "privacy.readReceipts"
            text: Whatevr.I18n.i18nc("@option:check", "Read receipts")
            description: Whatevr.I18n.i18nc("@info", "When off, you won't send or receive read receipts. Read receipts are always sent in group chats.")
            checked: Whatevr.AppController.privacySettings.readReceipts ?? true
            onToggled: Whatevr.AppController.setReadReceipts(checked)
        }

        FormCard.FormDelegateSeparator {}

        PrivacyAudienceCombo {
            objectName: "privacy.groupAdd"
            text: Whatevr.I18n.i18nc("@label:listbox", "Who can add me to groups")
            categoryKey: "groupAdd"
            audienceModel: page.everyoneContactsNobody
        }

        FormCard.FormDelegateSeparator {}

        PrivacyAudienceCombo {
            objectName: "privacy.callAdd"
            text: Whatevr.I18n.i18nc("@label:listbox", "Who can call me")
            categoryKey: "callAdd"
            audienceModel: page.callModel
        }
    }

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Blocked")
    }

    FormCard.FormCard {
        FormCard.FormButtonDelegate {
            objectName: "privacy.blocked"
            text: Whatevr.I18n.i18nc("@action:button", "Blocked contacts")
            description: Whatevr.I18n.i18nc("@info", "Manage the contacts you have blocked.")
            icon.name: "im-ban-user-symbolic"
            onClicked: page.hostPageStack.push(blockedPageComponent)
        }
    }

    Component {
        id: blockedPageComponent
        BlockedContactsPage {}
    }
}
