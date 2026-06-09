import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami
import org.kde.kirigamiaddons.components as KirigamiAddons

Item {
    id: root

    property string avatarLocalPath: ""
    property string initials: "?"
    property color backgroundColor: Qt.alpha(activePalette.highlight, 0.14)
    property color foregroundColor: activePalette.highlight
    readonly property string avatarSource: avatarLocalPath.length > 0 ? "file://" + avatarLocalPath : ""
    readonly property bool hasUsableAvatar: avatarLocalPath.length > 0
    readonly property bool initialsAreAlphabetic: /^[A-Za-z]+$/.test(initials)
    readonly property bool showInitials: !hasUsableAvatar && initialsAreAlphabetic

    implicitWidth: Kirigami.Units.gridUnit * 2.45
    implicitHeight: implicitWidth

    Kirigami.Theme.inherit: false
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    SystemPalette {
        id: activePalette
        colorGroup: SystemPalette.Active
    }

    Rectangle {
        anchors.fill: parent
        radius: width / 2
        antialiasing: true
        color: root.backgroundColor
    }

    KirigamiAddons.Avatar {
        id: avatarImage

        // Decode size snapped to 32px buckets (capped at 256) so layout jitter
        // and animated resizes never force a re-decode, and identical avatars
        // shown at slightly different sizes share one pixmap-cache entry.
        readonly property int decodeSize: Math.min(256, Math.ceil(Math.max(1, width * Screen.devicePixelRatio) / 32) * 32)

        anchors.fill: parent
        visible: root.hasUsableAvatar
        source: root.avatarSource
        imageMode: KirigamiAddons.Avatar.ImageMode.AlwaysShowImage
        asynchronous: true
        cache: true
        sourceSize.width: decodeSize
        sourceSize.height: decodeSize
    }

    Label {
        anchors.centerIn: parent
        visible: root.showInitials
        text: root.initials
        color: root.foregroundColor
        font.weight: Font.DemiBold
    }

    Kirigami.Icon {
        anchors.centerIn: parent
        visible: !root.hasUsableAvatar && !root.initialsAreAlphabetic
        source: "user-identity-symbolic"
        implicitWidth: Math.round(Math.min(root.width, root.height) * 0.58)
        implicitHeight: implicitWidth
        color: root.foregroundColor
    }
}
