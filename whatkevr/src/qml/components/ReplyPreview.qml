import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami
import Whatevr as Whatevr

Control {
    id: root

    SystemPalette {
        id: activePalette
        colorGroup: SystemPalette.Active
    }

    property string senderName: ""
    property string body: ""
    property string mediaKind: ""
    property string mediaMimeType: ""
    property string targetMessageId: ""
    property bool outgoing: false
    property bool showCloseButton: false
    // Always-active highlight so the reply accent stays vivid when the window
    // is unfocused (Kirigami.Theme.highlightColor greys out on focus loss).
    property color accentColor: activePalette.highlight
    property color fillColor: Qt.alpha(Kirigami.Theme.textColor, 0.055)
    property color borderColor: Qt.alpha(Kirigami.Theme.textColor, 0.08)

    signal closeRequested()
    signal activated(string messageId)

    function fallbackBody() {
        if (mediaKind === "sticker") {
            return Whatevr.I18n.i18nc("@label quoted message preview", "Sticker")
        }
        if (mediaKind === "image" || mediaMimeType.startsWith("image/")) {
            return Whatevr.I18n.i18nc("@label quoted message preview", "Photo")
        }
        if (mediaKind.length > 0 || mediaMimeType.length > 0) {
            return Whatevr.I18n.i18nc("@label quoted message preview", "Media")
        }
        return Whatevr.I18n.i18nc("@label quoted message preview", "Message unavailable")
    }

    function displayBody() {
        const trimmed = body.trim()
        return trimmed.length > 0 ? trimmed : fallbackBody()
    }

    function displaySender() {
        if (outgoing) {
            return Whatevr.I18n.i18nc("@label quoted own message sender", "You")
        }
        const trimmed = senderName.trim()
        return trimmed.length > 0 ? trimmed : Whatevr.I18n.i18nc("@label quoted unknown sender", "Message")
    }

    // Natural width the preview wants for its (single-line, elided) labels.
    // Reads label implicitWidth, which is the unwrapped text width and is
    // independent of the assigned/anchored width, so the parent can use this to
    // size the bubble without creating a binding loop.
    readonly property real naturalContentWidth: Math.max(senderLabel.implicitWidth,
                                                         previewLabel.implicitWidth)
                                                + leftPadding + rightPadding

    leftPadding: Kirigami.Units.smallSpacing + Kirigami.Units.smallSpacing / 2 + Kirigami.Units.smallSpacing
    rightPadding: showCloseButton ? Kirigami.Units.smallSpacing / 2 : Kirigami.Units.smallSpacing
    topPadding: Kirigami.Units.smallSpacing
    bottomPadding: Kirigami.Units.smallSpacing
    implicitHeight: Math.max(Kirigami.Units.gridUnit * 2.35,
                             content.implicitHeight + topPadding + bottomPadding)

    MouseArea {
        anchors.fill: parent
        acceptedButtons: Qt.LeftButton
        cursorShape: Qt.PointingHandCursor
        enabled: !root.showCloseButton && root.targetMessageId.length > 0
        z: 2
        onClicked: root.activated(root.targetMessageId)
    }

    background: Rectangle {
        radius: Kirigami.Units.cornerRadius * 0.8
        color: root.fillColor
        border.color: root.borderColor
        border.width: 1

        Rectangle {
            anchors.left: parent.left
            anchors.top: parent.top
            anchors.bottom: parent.bottom
            width: Math.max(2, Math.round(Kirigami.Units.smallSpacing / 2))
            radius: width / 2
            color: root.accentColor
        }
    }

    contentItem: Item {
        id: content

        implicitHeight: senderLabel.implicitHeight + Kirigami.Units.smallSpacing / 3 + previewLabel.implicitHeight

        Label {
            id: senderLabel

            anchors.left: parent.left
            anchors.right: closeButton.visible ? closeButton.left : parent.right
            anchors.rightMargin: closeButton.visible ? Kirigami.Units.smallSpacing / 2 : 0
            anchors.top: parent.top
            text: root.displaySender()
            color: root.accentColor
            elide: Text.ElideRight
            maximumLineCount: 1
            font.weight: Font.DemiBold
            font.pointSize: Kirigami.Theme.smallFont.pointSize * 0.92
        }

        Label {
            id: previewLabel

            anchors.left: parent.left
            anchors.right: senderLabel.right
            anchors.top: senderLabel.bottom
            anchors.topMargin: Kirigami.Units.smallSpacing / 3
            text: root.displayBody()
            color: Qt.alpha(Kirigami.Theme.textColor, 0.72)
            elide: Text.ElideRight
            maximumLineCount: 1
            font.pointSize: Kirigami.Theme.smallFont.pointSize * 0.88
        }

        ToolButton {
            id: closeButton

            visible: root.showCloseButton
            anchors.right: parent.right
            anchors.verticalCenter: parent.verticalCenter
            width: Kirigami.Units.gridUnit * 1.25
            height: width
            icon.name: "dialog-close-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Cancel reply")
            display: AbstractButton.IconOnly
            focusPolicy: Qt.NoFocus
            onClicked: root.closeRequested()
        }
    }
}
