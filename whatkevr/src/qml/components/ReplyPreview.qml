pragma ComponentBehavior: Bound

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
    // When set, the header line shows this fixed title (e.g. "Editing message")
    // instead of the quoted sender, and an optional leading icon. Lets the
    // composer reuse this banner for edit mode with a distinct accent.
    property string title: ""
    property string iconName: ""
    property string closeButtonText: Whatevr.I18n.i18nc("@action:button", "Cancel reply")
    // Always-active highlight so the reply accent stays vivid when the window
    // is unfocused (Kirigami.Theme.highlightColor greys out on focus loss).
    property color accentColor: activePalette.highlight
    property color fillColor: Qt.alpha(Kirigami.Theme.textColor, 0.055)
    property color borderColor: Qt.alpha(Kirigami.Theme.textColor, 0.08)

    signal closeRequested()
    signal activated(string messageId)

    function fallbackBody() {
        switch (mediaKind) {
        case "sticker":
            return Whatevr.I18n.i18nc("@label quoted message preview", "Sticker")
        case "image":
            return Whatevr.I18n.i18nc("@label quoted message preview", "Photo")
        case "video":
            return Whatevr.I18n.i18nc("@label quoted message preview", "Video")
        case "gif":
            return Whatevr.I18n.i18nc("@label quoted message preview", "GIF")
        case "video_note":
            return Whatevr.I18n.i18nc("@label quoted message preview", "Video message")
        case "voice":
            return Whatevr.I18n.i18nc("@label quoted message preview", "Voice message")
        case "audio":
            return Whatevr.I18n.i18nc("@label quoted message preview", "Audio")
        case "document":
            return Whatevr.I18n.i18nc("@label quoted message preview", "Document")
        }
        if (mediaMimeType.startsWith("image/")) {
            return Whatevr.I18n.i18nc("@label quoted message preview", "Photo")
        }
        if (mediaKind.length > 0 || mediaMimeType.length > 0) {
            return Whatevr.I18n.i18nc("@label quoted message preview", "Media")
        }
        return Whatevr.I18n.i18nc("@label quoted message preview", "Message unavailable")
    }

    /// A glyph for the quoted kind, so a reply to a voice note reads as one at
    /// a glance rather than as an anonymous line of text.
    function mediaIconName() {
        switch (mediaKind) {
        case "image":
            return "image-x-generic-symbolic"
        case "sticker":
            return "emoji-symbols-symbolic"
        case "video":
        case "gif":
            return "video-x-generic-symbolic"
        case "video_note":
            return "camera-video-symbolic"
        case "voice":
            return "audio-input-microphone-symbolic"
        case "audio":
            return "audio-x-generic-symbolic"
        case "document":
            return "text-x-generic-symbolic"
        }
        return ""
    }

    function displayBody() {
        const trimmed = body.trim()
        return trimmed.length > 0 ? trimmed : fallbackBody()
    }

    function displaySender() {
        if (title.length > 0) {
            return title
        }
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
                                                         previewLabel.implicitWidth
                                                         + (bodyIcon.visible
                                                            ? bodyIcon.width + Kirigami.Units.smallSpacing / 2
                                                            : 0))
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

        Kirigami.Icon {
            id: headerIcon

            visible: root.iconName.length > 0
            source: root.iconName
            color: root.accentColor
            width: visible ? Kirigami.Theme.smallFont.pointSize * 1.15 : 0
            height: width
            anchors.left: parent.left
            anchors.verticalCenter: senderLabel.verticalCenter
        }

        Label {
            id: senderLabel

            anchors.left: headerIcon.visible ? headerIcon.right : parent.left
            anchors.leftMargin: headerIcon.visible ? Kirigami.Units.smallSpacing / 2 : 0
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

        Kirigami.Icon {
            id: bodyIcon

            // Only for a quoted media message; a quoted text reply keeps the
            // body flush with the sender name above it.
            visible: root.mediaIconName().length > 0
            source: root.mediaIconName()
            color: previewLabel.color
            width: visible ? Math.round(previewLabel.implicitHeight * 0.92) : 0
            height: width
            anchors.left: senderLabel.left
            anchors.verticalCenter: previewLabel.verticalCenter
        }

        Label {
            id: previewLabel

            anchors.left: bodyIcon.visible ? bodyIcon.right : senderLabel.left
            anchors.leftMargin: bodyIcon.visible ? Kirigami.Units.smallSpacing / 2 : 0
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
            text: root.closeButtonText
            display: AbstractButton.IconOnly
            focusPolicy: Qt.NoFocus
            onClicked: root.closeRequested()
        }
    }
}
