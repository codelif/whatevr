import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami

ScrollBar {
    id: root

    policy: ScrollBar.AlwaysOn
    hoverEnabled: true
    interactive: true
    implicitWidth: hovered || pressed || active ? Kirigami.Units.smallSpacing * 1.5 : 2
    width: implicitWidth

    background: Item {
        implicitWidth: root.implicitWidth
    }

    contentItem: Rectangle {
        implicitWidth: root.implicitWidth
        radius: width / 2
        color: Qt.alpha(Kirigami.Theme.textColor,
                        root.hovered || root.pressed || root.active ? 0.42 : 0.22)
    }

    Behavior on implicitWidth {
        NumberAnimation {
            duration: Kirigami.Units.shortDuration
            easing.type: Easing.OutCubic
        }
    }
}
