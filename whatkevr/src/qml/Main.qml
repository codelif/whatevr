import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami

Kirigami.ApplicationWindow {
    id: root

    width: 1180
    height: 760
    minimumWidth: 420
    minimumHeight: 680
    title: i18nc("@title:window", "Whatevr")
    visible: true

    pageStack.globalToolBar.style: Kirigami.ApplicationHeaderStyle.None
    pageStack.initialPage: ShellPage {}
}
