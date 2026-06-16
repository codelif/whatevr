import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard

import "SettingsSearch.js" as Search

// Base for every settings category page. Adds the search "jump to a row"
// affordance: when opened from the global settings search, pendingRowId names a
// delegate (by objectName) to scroll into focus and briefly flash so the user
// sees exactly which option their query matched.
FormCard.FormCardPage {
    id: page

    // Set via ConfigurationModule.initialProperties when navigating from search.
    property string pendingRowId: ""

    // The settings window's own pageStack, injected by SettingsWindow when the
    // page is created. Pages must push drill-in sub-pages (e.g. blocked
    // contacts) onto THIS stack; applicationWindow() resolves to the main app
    // window, which would open the sub-page behind the settings pane.
    property var hostPageStack: null

    function flashRow(rowId) {
        if (!rowId)
            return
        const item = Search.findRow(page, rowId)
        if (!item)
            return
        flashAnimation.item = item
        flashAnimation.restart()
    }

    SequentialAnimation {
        id: flashAnimation

        property Item item: null

        loops: 3
        NumberAnimation {
            target: flashAnimation.item
            property: "opacity"
            to: 0.35
            duration: 180
            easing.type: Easing.InOutQuad
        }
        NumberAnimation {
            target: flashAnimation.item
            property: "opacity"
            to: 1.0
            duration: 220
            easing.type: Easing.InOutQuad
        }
    }

    Component.onCompleted: if (pendingRowId)
        Qt.callLater(() => flashRow(pendingRowId))
}
