import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings category", "Privacy")

    Component.onCompleted: Whatevr.ProtocolController.openPrivacySettings()
    Component.onDestruction: Whatevr.ProtocolController.closePrivacySettings()

    readonly property var everyoneContactsNobody: [
        { value: "all", text: Whatevr.I18n.i18nc("@item privacy audience", "Everyone") },
        { value: "contacts", text: Whatevr.I18n.i18nc("@item privacy audience", "My contacts") },
        { value: "contact_blacklist", text: Whatevr.I18n.i18nc("@item privacy audience", "My contacts except…") },
        { value: "none", text: Whatevr.I18n.i18nc("@item privacy audience", "Nobody") }
    ]
    readonly property var onlineModel: [
        { value: "all", text: Whatevr.I18n.i18nc("@item privacy audience", "Everyone") },
        { value: "match_last_seen", text: Whatevr.I18n.i18nc("@item privacy audience", "Same as last seen") }
    ]
    readonly property var callModel: [
        { value: "all", text: Whatevr.I18n.i18nc("@item privacy audience", "Everyone") },
        { value: "known", text: Whatevr.I18n.i18nc("@item privacy audience", "Known contacts") }
    ]

    FormCard.FormHeader {
        title: Whatevr.I18n.i18nc("@title:group", "Who can see my…")
    }

    FormCard.FormCard {
        PrivacyAudienceCombo {
            objectName: "privacy.lastSeen"
            text: Whatevr.I18n.i18nc("@label:listbox", "Last seen")
            categoryKey: "last_seen"
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
            categoryKey: "profile_photo"
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
            id: readReceiptsSwitch
            objectName: "privacy.readReceipts"
            text: Whatevr.I18n.i18nc("@option:check", "Read receipts")
            description: Whatevr.I18n.i18nc("@info", "When off, you won't send or receive read receipts. Read receipts are always sent in group chats.")
            checked: Whatevr.ProtocolController.privacySettings.read_receipts ?? true
            onToggled: Whatevr.ProtocolController.setReadReceipts(checked)

            // Toggling a switch breaks its `checked` binding, so an external
            // change (e.g. from the phone) would stop reflecting. Re-establish
            // the binding whenever the privacy settings change.
            Connections {
                target: Whatevr.ProtocolController
                function onPrivacySettingsChanged() {
                    readReceiptsSwitch.checked = Qt.binding(() => Whatevr.ProtocolController.privacySettings.read_receipts ?? true)
                }
                function onSettingsActionFailed() {
                    readReceiptsSwitch.checked = Qt.binding(() => Whatevr.ProtocolController.privacySettings.read_receipts ?? true)
                }
            }
        }

        FormCard.FormDelegateSeparator {}

        PrivacyAudienceCombo {
            objectName: "privacy.groupAdd"
            text: Whatevr.I18n.i18nc("@label:listbox", "Who can add me to groups")
            categoryKey: "group_add"
            audienceModel: page.everyoneContactsNobody
        }

        FormCard.FormDelegateSeparator {}

        PrivacyAudienceCombo {
            objectName: "privacy.callAdd"
            text: Whatevr.I18n.i18nc("@label:listbox", "Who can call me")
            categoryKey: "call_add"
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
