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

        anchors.fill: parent
        visible: root.avatarLocalPath.length > 0
        source: root.avatarLocalPath.length > 0 ? "file://" + root.avatarLocalPath : ""
        imageMode: KirigamiAddons.Avatar.ImageMode.AlwaysShowImage
        asynchronous: true
        cache: true
        sourceSize.width: Math.ceil(width * Screen.devicePixelRatio)
        sourceSize.height: Math.ceil(height * Screen.devicePixelRatio)
    }

    Label {
        anchors.centerIn: parent
        visible: root.avatarLocalPath.length === 0
        text: root.initials
        color: root.foregroundColor
        font.weight: Font.DemiBold
    }
}
