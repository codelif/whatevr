import QtQuick
import QtQuick.Controls
import QtQuick.Effects
import org.kde.kirigami as Kirigami

Item {
    id: root

    property string avatarLocalPath: ""
    property string initials: "?"
    property color backgroundColor: Qt.alpha(Kirigami.Theme.highlightColor, 0.14)
    property color foregroundColor: Kirigami.Theme.highlightColor

    implicitWidth: Kirigami.Units.gridUnit * 2.45
    implicitHeight: implicitWidth

    Kirigami.Theme.inherit: false
    Kirigami.Theme.colorSet: Kirigami.Theme.View

    Rectangle {
        anchors.fill: parent
        radius: width / 2
        color: root.backgroundColor
    }

    Image {
        id: avatarImage

        anchors.fill: parent
        visible: root.avatarLocalPath.length > 0
        source: root.avatarLocalPath.length > 0 ? "file://" + root.avatarLocalPath : ""
        fillMode: Image.PreserveAspectCrop
        asynchronous: true
        cache: true
        sourceSize.width: width
        sourceSize.height: height

        layer.enabled: visible
        layer.effect: MultiEffect {
            maskEnabled: true
            maskSource: avatarMask
        }
    }

    Rectangle {
        id: avatarMask

        width: root.width
        height: root.height
        radius: width / 2
        visible: false
        layer.enabled: true
    }

    Label {
        anchors.centerIn: parent
        visible: root.avatarLocalPath.length === 0
        text: root.initials
        color: root.foregroundColor
        font.weight: Font.DemiBold
    }
}
