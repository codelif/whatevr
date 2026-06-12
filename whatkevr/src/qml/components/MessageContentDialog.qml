pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

// Full text of a long ("Read more") message, shown verbatim with the same markup
// the bubble renders. The body scrolls inside our own kinetic Flickable (custom
// scroller + DiscreetScrollBar), fully selectable/copyable, with a dedicated Copy
// button.
CenteredDialog {
    id: root

    property string plainText: ""
    property string richText: ""
    property bool useRichText: false

    title: Whatevr.I18n.i18nc("@title:dialog full text of a long message", "Message")
    padding: Kirigami.Units.largeSpacing

    // Scale with the window so the dialog fills the available space gracefully on
    // both a small (quarter) window and a fullscreen one, while staying within
    // readable/sane bounds. preferredWidth drives the body width below.
    readonly property real windowWidth: parent ? parent.width : Kirigami.Units.gridUnit * 40
    readonly property real windowHeight: parent ? parent.height : Kirigami.Units.gridUnit * 40
    // Reserve for the dialog chrome (title bar + footer + paddings) so the body
    // cap below leaves room for it within maximumHeight.
    readonly property real chromeReserve: Kirigami.Units.gridUnit * 8
    preferredWidth: Math.round(Math.max(Kirigami.Units.gridUnit * 22,
                                        Math.min(windowWidth * 0.85,
                                                 Kirigami.Units.gridUnit * 52)))
    maximumHeight: Math.round(Math.max(Kirigami.Units.gridUnit * 18,
                                       Math.min(windowHeight * 0.82,
                                                Kirigami.Units.gridUnit * 60)))
    // Cap our scrollable body so its Flickable (not Kirigami.Dialog's outer
    // ScrollView) owns the scrolling. Keeping body + chrome <= maximumHeight stops
    // the dialog from adding a second, hidden scroll region that would let the
    // content scroll past where our DiscreetScrollBar ends.
    readonly property real bodyMaxHeight: Math.round(Math.max(Kirigami.Units.gridUnit * 10,
                                                              Math.min(windowHeight * 0.7,
                                                                       Kirigami.Units.gridUnit * 50,
                                                                       maximumHeight - chromeReserve)))
    standardButtons: Kirigami.Dialog.Close

    // Kirigami.Dialog wraps its content in a QQC2.ScrollView with its own vertical
    // scrollbar; our body Flickable already scrolls via the custom kinetic
    // scroller + DiscreetScrollBar, so suppress the dialog's duplicate bar.
    Component.onCompleted: contentItem.ScrollBar.vertical.policy = ScrollBar.AlwaysOff

    function openFor(snapshot) {
        if (!snapshot) {
            return
        }
        root.plainText = String(snapshot.text || "")
        root.richText = String(snapshot.richText || "")
        root.useRichText = Boolean(snapshot.hasRichText) && root.richText.length > 0
        open()
    }

    customFooterActions: [
        Kirigami.Action {
            text: Whatevr.I18n.i18nc("@action:button copy the full message text", "Copy")
            icon.name: "edit-copy-symbolic"
            enabled: root.plainText.length > 0
            onTriggered: {
                Whatevr.AppController.copyToClipboard(root.plainText)
                applicationWindow()?.showPassiveNotification(
                    Whatevr.I18n.i18nc("@info:status", "Message copied"), "short")
            }
        }
    ]

    ColumnLayout {
        // Kirigami.Dialog does not give a bare content item a width; pin the
        // layout to the dialog's preferred content width like the other in-app
        // dialogs do, then let the Flickable fill it.
        implicitWidth: root.preferredWidth - 2 * root.padding
        spacing: 0

        Flickable {
            id: bodyFlick

            Layout.fillWidth: true
            Layout.preferredHeight: Math.min(contentText.implicitHeight, root.bodyMaxHeight)
            contentWidth: width
            contentHeight: contentText.implicitHeight
            clip: true
            boundsBehavior: Flickable.StopAtBounds
            ScrollBar.vertical: DiscreetScrollBar {}

            TextEdit {
                id: contentText

                width: bodyFlick.width
                readOnly: true
                selectByMouse: true
                selectByKeyboard: true
                persistentSelection: true
                wrapMode: TextEdit.Wrap
                textFormat: root.useRichText ? TextEdit.RichText : TextEdit.PlainText
                text: root.useRichText ? root.richText : root.plainText
                color: Kirigami.Theme.textColor
                selectionColor: Kirigami.Theme.highlightColor
                selectedTextColor: Kirigami.Theme.highlightedTextColor
                font.family: Kirigami.Theme.defaultFont.family
                font.pointSize: Kirigami.Theme.defaultFont.pointSize > 0
                                ? Kirigami.Theme.defaultFont.pointSize
                                : 10
                onLinkActivated: link => Qt.openUrlExternally(link)

                HoverHandler {
                    cursorShape: Qt.IBeamCursor
                }
            }

            KineticWheelScroller {
                anchors.fill: parent
                target: bodyFlick
                wheelStep: Kirigami.Units.gridUnit * 3
            }
        }
    }
}
