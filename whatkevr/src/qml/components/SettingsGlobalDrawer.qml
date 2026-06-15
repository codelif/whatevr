import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami

import Whatevr as Whatevr
import "../settings/SettingsSearch.js" as Search

// Home for app-level navigation: a prominent "search every setting" field plus
// Settings / About / Log out. Searching fans out across all categories and,
// when a result is chosen, jumps straight to that exact option.
Kirigami.GlobalDrawer {
    id: drawer

    // Flat option index supplied by SettingsView.searchIndex.
    property var searchIndex: []

    signal settingsRequested()
    signal aboutRequested()
    signal logoutRequested()
    // A search result was activated: open settings at this option.
    signal optionRequested(string moduleId, string rowId)

    modal: true
    title: Whatevr.I18n.i18nc("@title", "Whatevr")
    titleIcon: "in.codelif.Whatevr"

    function resetSearch() {
        searchField.text = ""
    }

    onClosed: resetSearch()

    topContent: [
        Kirigami.SearchField {
            id: searchField

            Layout.fillWidth: true
            Layout.margins: Kirigami.Units.smallSpacing
            placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Search settings…")

            onAccepted: {
                if (resultsView.count > 0)
                    resultsView.itemAtIndex(0)?.activate()
            }
            Keys.onDownPressed: resultsView.forceActiveFocus()
        },
        QQC2.ScrollView {
            id: resultsScroll

            Layout.fillWidth: true
            visible: searchField.text.length > 0
            QQC2.ScrollBar.horizontal.policy: QQC2.ScrollBar.AlwaysOff
            // Grow with the result count, capped so the drawer stays usable.
            implicitHeight: Math.min(resultsView.contentHeight,
                                     Kirigami.Units.gridUnit * 16)

            ListView {
                id: resultsView

                clip: true
                model: searchField.text.length > 0
                    ? Search.rank(searchField.text, drawer.searchIndex)
                    : []

                delegate: QQC2.ItemDelegate {
                    id: resultDelegate

                    required property var modelData

                    width: ListView.view.width
                    text: modelData.label

                    function activate() {
                        drawer.optionRequested(modelData.moduleId, modelData.rowId)
                        drawer.close()
                    }

                    contentItem: ColumnLayout {
                        spacing: 0
                        QQC2.Label {
                            Layout.fillWidth: true
                            text: resultDelegate.modelData.label
                            elide: Text.ElideRight
                        }
                        QQC2.Label {
                            Layout.fillWidth: true
                            text: resultDelegate.modelData.category
                            elide: Text.ElideRight
                            font: Kirigami.Theme.smallFont
                            opacity: 0.7
                        }
                    }

                    onClicked: activate()
                    Keys.onReturnPressed: activate()
                    Keys.onEnterPressed: activate()
                }

                QQC2.Label {
                    anchors.centerIn: parent
                    width: parent.width - Kirigami.Units.largeSpacing * 2
                    horizontalAlignment: Text.AlignHCenter
                    wrapMode: Text.WordWrap
                    visible: resultsView.count === 0 && searchField.text.length > 0
                    opacity: 0.7
                    text: Whatevr.I18n.i18nc("@info:placeholder", "No matching settings")
                }
            }
        }
    ]

    actions: [
        Kirigami.Action {
            icon.name: "settings-configure-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Settings")
            onTriggered: {
                drawer.close()
                drawer.settingsRequested()
            }
        },
        Kirigami.Action {
            icon.name: "help-about-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "About Whatevr")
            onTriggered: {
                drawer.close()
                drawer.aboutRequested()
            }
        },
        Kirigami.Action {
            icon.name: "system-log-out-symbolic"
            text: Whatevr.I18n.i18nc("@action:button", "Log out")
            enabled: Whatevr.AppController.shellVisible
            onTriggered: {
                drawer.close()
                drawer.logoutRequested()
            }
        }
    ]
}
