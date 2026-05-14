import QtQuick
import org.kde.kirigami as Kirigami

Kirigami.ApplicationWindow {
    id: root

    property string currentMode: ""

    // width: 1180
    // height: 760
    minimumWidth: Kirigami.Units.gridUnit * 20
    minimumHeight: Kirigami.Units.gridUnit * 15
    title: i18nc("@title:window", "Whatevr")
    visible: true

    pageStack.columnView.columnResizeMode: Kirigami.ColumnView.SingleColumn
    pageStack.globalToolBar.style: currentMode === "chat"
                                   ? Kirigami.ApplicationHeaderStyle.None
                                   : Kirigami.ApplicationHeaderStyle.ToolBar
    pageStack.globalToolBar.showNavigationButtons: currentMode === "chat"
                                                  ? Kirigami.ApplicationHeaderStyle.NoNavigationButtons
                                                  : Kirigami.ApplicationHeaderStyle.ShowBackButton | Kirigami.ApplicationHeaderStyle.ShowForwardButton

    Timer {
        id: pageStackRebuildTimer

        interval: 50
        repeat: false
        onTriggered: root.rebuildPageStack()
    }

    function appMode() {
        if (AppController.loginRequired) {
            return "login"
        }
        if (!AppController.shellVisible) {
            return "status"
        }
        return "chat"
    }

    Component {
        id: loginPageComponent

        LoginPage {}
    }

    Component {
        id: statusPageComponent

        StatusPage {}
    }

    Component {
        id: chatShellComponent

        ChatShell {}
    }

    function createPage(component) {
        return component.createObject(pageStack)
    }

    function pushPage(component) {
        const page = createPage(component)
        if (!page) {
            console.warn("Failed to create page")
            return null
        }
        return pageStack.push(page)
    }

    function scheduleRebuildPageStack() {
        pageStackRebuildTimer.restart()
    }

    function resetToPage(mode, pageComponent) {
        currentMode = mode
        pageStack.clear()
        pushPage(pageComponent)
    }

    function rebuildPageStack() {
        const nextMode = appMode()
        if (nextMode === currentMode) {
            return
        }

        switch (nextMode) {
        case "login":
            resetToPage(nextMode, loginPageComponent)
            break
        case "status":
            resetToPage(nextMode, statusPageComponent)
            break
        case "chat":
            resetToPage(nextMode, chatShellComponent)
            break
        }
    }

    Component.onCompleted: scheduleRebuildPageStack()

    Connections {
        target: AppController

        function onStateChanged() {
            root.scheduleRebuildPageStack()
        }
    }
}
