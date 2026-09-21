import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

QQC2.Dialog {
    id: root

    title: Whatevr.I18n.i18nc("@title:window", "Create Poll")
    modal: true
    anchors.centerIn: QQC2.Overlay.overlay
    width: Math.min(Kirigami.Units.gridUnit * 24, QQC2.Overlay.overlay ? QQC2.Overlay.overlay.width - Kirigami.Units.gridUnit * 4 : 400)
    standardButtons: QQC2.Dialog.Ok | QQC2.Dialog.Cancel
    parent: QQC2.Overlay.overlay

    property var options: ["", ""]
    property string replyToMessageId: ""

    onAccepted: {
        const q = questionField.text.trim()
        const opts = []
        for (let i = 0; i < root.options.length; ++i) {
            const t = root.options[i].trim()
            if (t.length > 0)
                opts.push(t)
        }
        if (q.length === 0 || opts.length < 2) {
            return
        }
        Whatevr.ProtocolController.sendPoll(q, opts, multiCheck.checked, root.replyToMessageId)
        root.options = ["", ""]
        questionField.text = ""
        multiCheck.checked = false
    }

    onRejected: {
        root.options = ["", ""]
        questionField.text = ""
        multiCheck.checked = false
    }

    contentItem: ColumnLayout {
        spacing: Kirigami.Units.largeSpacing

        QQC2.Label {
            text: Whatevr.I18n.i18nc("@label", "Question")
            font.weight: Font.DemiBold
        }

        QQC2.TextField {
            id: questionField
            Layout.fillWidth: true
            placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Ask a question...")
        }

        QQC2.Label {
            text: Whatevr.I18n.i18nc("@label", "Options")
            font.weight: Font.DemiBold
        }

        ColumnLayout {
            id: optionsColumn
            Layout.fillWidth: true
            spacing: Kirigami.Units.smallSpacing

            Repeater {
                model: root.options.length

                RowLayout {
                    required property int index

                    Layout.fillWidth: true
                    spacing: Kirigami.Units.smallSpacing

                    QQC2.TextField {
                        Layout.fillWidth: true
                        text: root.options[index] || ""
                        placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Option %1", index + 1)
                        onTextEdited: {
                            let copy = root.options.slice()
                            copy[index] = text
                            root.options = copy
                        }
                    }

                    QQC2.ToolButton {
                        icon.name: "list-remove-symbolic"
                        visible: root.options.length > 2
                        onClicked: {
                            let copy = root.options.slice()
                            copy.splice(index, 1)
                            root.options = copy
                        }
                    }
                }
            }
        }

        QQC2.Button {
            text: Whatevr.I18n.i18nc("@action:button", "Add option")
            icon.name: "list-add-symbolic"
            enabled: root.options.length < 12
            onClicked: {
                let copy = root.options.slice()
                copy.push("")
                root.options = copy
            }
        }

        QQC2.CheckBox {
            id: multiCheck
            text: Whatevr.I18n.i18nc("@option:check", "Allow multiple answers")
        }
    }
}
