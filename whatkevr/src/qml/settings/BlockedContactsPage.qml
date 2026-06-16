import QtQuick
import QtQuick.Layouts
import QtQuick.Controls as QQC2
import org.kde.kirigami as Kirigami
import org.kde.kirigamiaddons.formcard as FormCard

import Whatevr as Whatevr

SettingsPage {
    id: page

    title: Whatevr.I18n.i18nc("@title settings subpage", "Blocked contacts")

    Component.onCompleted: Whatevr.AppController.refreshBlocklist()

    readonly property var contacts: Whatevr.AppController.blockedContacts

    FormCard.FormCard {
        Layout.topMargin: Kirigami.Units.largeSpacing

        // Empty state.
        FormCard.FormTextDelegate {
            visible: page.contacts.length === 0
            text: Whatevr.I18n.i18nc("@info", "No blocked contacts")
            description: Whatevr.I18n.i18nc("@info", "Contacts you block will appear here.")
        }

        Repeater {
            model: page.contacts

            FormCard.AbstractFormDelegate {
                id: row

                required property var modelData
                background: null

                contentItem: RowLayout {
                    spacing: Kirigami.Units.largeSpacing

                    Whatevr.AvatarImage {
                        Layout.preferredWidth: Kirigami.Units.iconSizes.medium
                        Layout.preferredHeight: Kirigami.Units.iconSizes.medium
                        avatarLocalPath: row.modelData.avatarLocalPath ?? ""
                        initials: {
                            const name = (row.modelData.displayName ?? "").trim();
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
                                const name = (row.modelData.displayName ?? "").trim();
                                return name.length > 0 ? name : (row.modelData.phoneNumber ?? row.modelData.jid);
                            }
                        }
                        QQC2.Label {
                            Layout.fillWidth: true
                            elide: Text.ElideRight
                            visible: text.length > 0
                            opacity: 0.7
                            font: Kirigami.Theme.smallFont
                            text: row.modelData.phoneNumber ?? ""
                        }
                    }

                    QQC2.Button {
                        text: Whatevr.I18n.i18nc("@action:button", "Unblock")
                        icon.name: "list-remove-user-symbolic"
                        onClicked: Whatevr.AppController.setContactBlocked(row.modelData.jid, false)
                    }
                }
            }
        }
    }
}
