import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami

Item {
    id: root

    readonly property bool wideLayout: width >= Kirigami.Units.gridUnit * 36

    RowLayout {
        visible: root.wideLayout
        anchors.fill: parent
        spacing: 0

        ChatListPane {}

        ConversationPane {}
    }

    ChatListPane {
        visible: !root.wideLayout
        anchors.fill: parent
        Layout.fillWidth: false
        Layout.fillHeight: false
    }
}
