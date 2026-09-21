pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Calls tab: what is ringing right now, with Reject per call and the honest
// caveat that answering happens on the phone. Missed calls land in their
// chats as tombstone messages (badge + preview), so this page stays empty
// unless something is actively ringing.
Kirigami.ScrollablePage {
    id: root

    title: Whatevr.I18n.i18nc("@title", "Calls")
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    Component.onCompleted: Whatevr.ProtocolController.openCalls()
    // Guarded: at engine teardown the singleton may already be null.
    Component.onDestruction: { const c = Whatevr.ProtocolController; if (c) c.closeCalls() }

    function callerLabel(item) {
        const caller = item.caller || {}
        if (caller.name && caller.name.length > 0) {
            return caller.name
        }
        return caller.id || item.chat_id
    }

    function callKindLabel(item) {
        if (item.video) {
            return Whatevr.I18n.i18nc("@info call kind", "Incoming video call")
        }
        return Whatevr.I18n.i18nc("@info call kind", "Incoming voice call")
    }

    ListView {
        id: callsList

        model: Whatevr.ProtocolController.callsModel
        currentIndex: -1
        reuseItems: true
        Component.onDestruction: callsList.model = null

        Kirigami.PlaceholderMessage {
            anchors.centerIn: parent
            width: parent.width - Kirigami.Units.gridUnit * 4
            visible: callsList.count === 0
            icon.name: "call-start-symbolic"
            text: Whatevr.I18n.i18nc("@info placeholder for the calls list", "No active calls")
            explanation: Whatevr.I18n.i18nc("@info:placeholder", "Incoming calls ring here with a Reject button. Answer on your phone — the desktop cannot pick up. Missed calls are kept in their chats.")
        }

        delegate: QQC2.ItemDelegate {
            id: callDelegate

            required property var item

            width: ListView.view.width

            contentItem: RowLayout {
                spacing: Kirigami.Units.largeSpacing

                AvatarImage {
                    Layout.preferredWidth: Kirigami.Units.gridUnit * 2
                    Layout.preferredHeight: Kirigami.Units.gridUnit * 2
                    initials: {
                        const name = callDelegate.item.caller ? (callDelegate.item.caller.name || "") : ""
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
                    backgroundColor: Qt.alpha(Kirigami.Theme.highlightColor, 0.18)
                }

                ColumnLayout {
                    Layout.fillWidth: true
                    spacing: Kirigami.Units.smallSpacing / 2

                    QQC2.Label {
                        Layout.fillWidth: true
                        text: root.callerLabel(callDelegate.item)
                        font.weight: Font.DemiBold
                        elide: Text.ElideRight
                    }

                    QQC2.Label {
                        Layout.fillWidth: true
                        text: root.callKindLabel(callDelegate.item)
                        color: Kirigami.Theme.disabledTextColor
                        elide: Text.ElideRight
                    }
                }

                QQC2.Button {
                    text: Whatevr.I18n.i18nc("@action:button reject the call", "Reject")
                    icon.name: "call-stop-symbolic"
                    onClicked: Whatevr.ProtocolController.rejectCall(callDelegate.item.chat_id)
                }
            }

            QQC2.Menu {
                id: callContextMenu

                QQC2.MenuItem {
                    text: Whatevr.I18n.i18nc("@action:menu reject call", "Reject call")
                    icon.name: "call-stop-symbolic"
                    onTriggered: Whatevr.ProtocolController.rejectCall(callDelegate.item.chat_id)
                }
            }

            TapHandler {
                acceptedButtons: Qt.RightButton
                onTapped: callContextMenu.popup()
            }
        }
    }
}
