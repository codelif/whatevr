import QtQuick
import QtQuick.Layouts
import QtQuick.Controls as QQC2
import org.kde.kirigami as Kirigami
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings subpage", "Blocked contacts")

    Component.onCompleted: Whatevr.ProtocolController.openBlockedContacts()
    Component.onDestruction: Whatevr.ProtocolController.closeBlockedContacts()

    readonly property var contacts: Whatevr.ProtocolController.blockedContactsModel

    FormCard.FormCard {
        Layout.topMargin: Kirigami.Units.largeSpacing

        // Empty state.
        FormCard.FormTextDelegate {
            visible: page.contacts.count === 0
            text: Whatevr.I18n.i18nc("@info", "No blocked contacts")
            description: Whatevr.I18n.i18nc("@info", "Contacts you block will appear here.")
        }

        Repeater {
            model: page.contacts

            FormCard.AbstractFormDelegate {
                id: row

                required property var item
                background: null

                contentItem: RowLayout {
                    spacing: Kirigami.Units.largeSpacing

                    Whatevr.AvatarImage {
                        Layout.preferredWidth: Kirigami.Units.iconSizes.medium
                        Layout.preferredHeight: Kirigami.Units.iconSizes.medium
                        avatarLocalPath: row.item.avatar_path ?? ""
                        initials: {
                            const name = (row.item.name ?? "").trim();
                            return name.length > 0 ? name.charAt(0).toUpperCase() : "?";
                        }
                    }

                    ColumnLayout {
                        Layout.fillWidth: true
                        spacing: 0
                        QQC2.Label {
                            Layout.fillWidth: true
                            elide: Text.ElideRight
                            text: {
                                const name = (row.item.name ?? "").trim();
                                return name.length > 0 ? name : (row.item.phone ?? row.item.jid);
                            }
                        }
                        QQC2.Label {
                            Layout.fillWidth: true
                            elide: Text.ElideRight
                            visible: text.length > 0
                            opacity: 0.7
                            font: Kirigami.Theme.smallFont
                            text: row.item.phone ?? ""
                        }
                    }

                    QQC2.Button {
                        text: Whatevr.I18n.i18nc("@action:button", "Unblock")
                        icon.name: "list-remove-user-symbolic"
                        onClicked: Whatevr.ProtocolController.setContactBlocked(row.item.jid, false)
                    }
                }
            }
        }
    }
}
