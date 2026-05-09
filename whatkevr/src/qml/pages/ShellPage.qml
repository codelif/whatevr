import QtQuick
import QtQuick.Controls
import org.kde.kirigami as Kirigami

Kirigami.Page {
    id: page

    title: ""
    padding: 0

    Loader {
        anchors.fill: parent
        sourceComponent: AppController.loginRequired
                         ? loginPage
                         : (AppController.shellVisible ? homePage : statusPage)
    }

    Component {
        id: loginPage

        LoginPage {}
    }

    Component {
        id: statusPage

        StatusPage {}
    }

    Component {
        id: homePage

        ChatsPage {}
    }
}
