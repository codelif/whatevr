// SPDX-FileCopyrightText: 2020 Tobias Fella <fella@posteo.de>
// SPDX-FileCopyrightText: 2021 Carl Schwan <carl@carlschwan.eu>
// SPDX-FileCopyrightText: 2021 Felipe Kinoshita <kinofhek@gmail.com>
// SPDX-FileCopyrightText: 2021 Marco Martin <mart@kde.org>
// SPDX-License-Identifier: LGPL-2.0-or-later

pragma ComponentBehavior: Bound

import QtQml
import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import org.kde.kirigamiaddons.delegates as Delegates
import org.kde.kirigamiaddons.settings as KirigamiSettings

import Whatevr as Whatevr
import "SettingsSearch.js" as Search

// Project fork of KirigamiAddons' private ConfigWindow. The upstream sidebar
// search only filters category *names* (module.text); this version searches
// every individual option (label + keywords + description) via searchIndex and,
// when a result is chosen, jumps to its category and flashes the exact row.
Kirigami.ApplicationWindow {
    id: root

    required property string defaultModule
    required property list<KirigamiSettings.ConfigurationModule> modules
    // Flat option index: { moduleId, category, rowId, label, keywords, description }.
    property var searchIndex: []
    // When opened from a search result, the row to flash once its page is shown.
    property string pendingModuleId: ""
    property string pendingRowId: ""

    // Do not use Map, it crashes very frequently
    property var pageCache: Object.create(null)

    // Guards the one-shot search-index augmentation (see ensureSearchCoverage).
    property bool searchCoverageDone: false

    modality: Qt.WindowModal

    // The first category page is pushed exactly once. Kept at window scope (not
    // on the sidebar ListView) so a ListView re-creation can't reset it and push
    // a duplicate page, which replays the slide-in transition.
    property bool initDone: false

    pageStack {
        columnView.columnWidth: Kirigami.Units.gridUnit * 13
        // Settings is intentionally animation-free. This is scoped locally (not
        // via the app-wide Kirigami AnimationDurationFactor, which must stay 1 so
        // the chat-list/conversation slide and the starred-messages drill-in
        // still animate) so only the settings page transitions are instant.
        columnView.scrollDuration: 0

        popHiddenPages: true

        globalToolBar {
            style: Kirigami.ApplicationHeaderStyle.ToolBar
            showNavigationButtons: root.pageStack.depth > 1 ? Kirigami.ApplicationHeaderStyle.ShowBackButton : Kirigami.ApplicationHeaderStyle.NoNavigationButtons
        }
    }

    contentItem.Keys.onEscapePressed: root.close()

    globalDrawer: Kirigami.OverlayDrawer {
        id: drawer
        edge: Qt.application.layoutDirection === Qt.RightToLeft ? Qt.RightEdge : Qt.LeftEdge

        modal: false
        Kirigami.OverlayZStacking.layer: Kirigami.OverlayZStacking.Drawer
        z: Kirigami.OverlayZStacking.z
        drawerOpen: true
        width: Kirigami.Units.gridUnit * 13
        Kirigami.Theme.colorSet: Kirigami.Theme.View
        Kirigami.Theme.inherit: false

        leftPadding: 0
        rightPadding: 0
        topPadding: 0
        bottomPadding: 0

        contentItem: ColumnLayout {
            spacing: 0

            QQC2.ToolBar {
                Layout.fillWidth: true
                Layout.preferredHeight: pageStack.globalToolBar.preferredHeight

                visible: !Kirigami.Settings.isMobile

                Kirigami.SearchField {
                    id: searchField
                    placeholderText: Whatevr.I18n.i18nc("@info:placeholder", "Search settings…")
                    // Only emit accepted on an explicit Enter. With the default
                    // autoAccept the field fires accepted after every typing
                    // pause, which jumped to and flashed the top result while the
                    // user was still typing.
                    autoAccept: false
                    onTextChanged: {
                        listview.filterText = text
                        if (text.trim().length !== 0)
                            root.ensureSearchCoverage()
                    }
                    anchors {
                        verticalCenter: parent.verticalCenter
                        left: parent.left
                        right: parent.right
                    }
                    Keys.onDownPressed: listview.forceActiveFocus()
                    onAccepted: if (listview.searching && listview.count > 0)
                        listview.itemAtIndex(0)?.activate()
                }
            }

            ListView {
                id: listview

                // ListView is its own Flickable; wrapping it in a ScrollView
                // double-managed the scrollbar and made the desktop style read
                // 'horizontal' off a null control on creation. Fill + attach the
                // vertical bar directly instead.
                Layout.fillWidth: true
                Layout.fillHeight: true
                clip: true

                QQC2.ScrollBar.vertical: QQC2.ScrollBar {}

                property string filterText: ""
                readonly property bool searching: filterText.trim().length !== 0

                currentIndex: -1

                    onWidthChanged: if (!root.initDone) {
                        let module = getModuleByName(root.defaultModule);
                        if (module) {
                            root.pageStack.push(pageForModule(module));
                            listview.currentIndex = root.modules.findIndex(module => module.moduleId == root.defaultModule);
                        } else {
                            root.pageStack.push(pageForModule(root.modules[0]));
                            listview.currentIndex = 0;
                            if (root.pageStack.currentItem.title.length === 0) {
                                root.pageStack.currentItem.title = root.modules[0].text;
                            }
                        }
                        root.initDone = true;
                        if (root.pendingRowId)
                            root.navigateToRow(root.pendingModuleId, root.pendingRowId);
                    }
                    model: {
                        if (searching)
                            return Search.rank(filterText, root.searchIndex);

                        let filteredModules = [];
                        for (const module of root.modules) {
                            if (module.visible) {
                                filteredModules.push(module);
                            }
                        }
                        return filteredModules;
                    }
                    delegate: Delegates.RoundedItemDelegate {
                        id: settingDelegate

                        required property int index
                        required property var modelData

                        // Search results carry rowId; category modules do not.
                        readonly property bool isResult: !!settingDelegate.modelData.rowId

                        text: isResult ? settingDelegate.modelData.label : settingDelegate.modelData?.text
                        icon.name: isResult ? "" : (settingDelegate.modelData?.icon.name ?? "")
                        icon.source: isResult ? "" : (settingDelegate.modelData?.icon.source ?? "")
                        checked: !listview.searching && ListView.view.currentIndex === settingDelegate.index

                        // Category rows keep the plain look; result rows add the
                        // category as a subtitle. RoundedItemDelegate has no
                        // subtitle of its own, so the content item provides it.
                        contentItem: Loader {
                            sourceComponent: settingDelegate.isResult ? resultContent : moduleContent
                        }

                        Component {
                            id: moduleContent
                            Delegates.DefaultContentItem {
                                itemDelegate: settingDelegate
                            }
                        }
                        Component {
                            id: resultContent
                            Delegates.SubtitleContentItem {
                                itemDelegate: settingDelegate
                                subtitle: settingDelegate.modelData.category ?? ""
                            }
                        }

                        function activate() {
                            if (settingDelegate.isResult) {
                                root.navigateToRow(settingDelegate.modelData.moduleId, settingDelegate.modelData.rowId);
                                return;
                            }

                            const page = pageForModule(settingDelegate.modelData);
                            if (ListView.view.currentIndex === settingDelegate.index) {
                                return;
                            }

                            while (root.pageStack.length > 1) {
                                root.pageStack.pop(null);
                            }
                            root.pageStack.replace(page);

                            ListView.view.currentIndex = settingDelegate.index;
                            if (root.pageStack.currentItem.title.length === 0) {
                                root.pageStack.currentItem.title = text;
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
                        visible: listview.searching && listview.count === 0
                        opacity: 0.7
                        text: Whatevr.I18n.i18nc("@info:placeholder", "No matching settings")
                    }
                }
        }
    }

    // Open the category that owns rowId, then flash that row. Used both by the
    // search results here and by SettingsView.openAt() for external jumps.
    function navigateToRow(moduleId: string, rowId: string): void {
        const module = getModuleByName(moduleId);
        if (!module) {
            return;
        }
        const page = pageForModule(module);
        const idx = visibleModuleIndex(moduleId);

        while (pageStack.length > 1) {
            pageStack.pop(null);
        }
        clearSearch();
        if (pageStack.currentItem !== page) {
            pageStack.replace(page);
        }
        if (idx >= 0) {
            listview.currentIndex = idx;
        }
        if (page.title.length === 0) {
            page.title = module.text;
        }
        if (rowId && typeof page.flashRow === "function") {
            Qt.callLater(() => page.flashRow(rowId));
        }
    }

    // Guarantee the search index covers every option, even ones never added to
    // the curated SettingsView.searchIndex. Runs once, lazily on first search:
    // instantiates each category page (cached via pageForModule) and appends a
    // minimal entry for any objectName+text delegate the curated list is
    // missing. Curated entries win on rowId collision, keeping their richer
    // keywords/descriptions.
    function ensureSearchCoverage(): void {
        if (searchCoverageDone)
            return;
        searchCoverageDone = true;

        const known = Object.create(null);
        for (const rec of root.searchIndex)
            known[rec.rowId] = true;

        const additions = [];
        for (const module of root.modules) {
            const page = pageForModule(module);
            if (!page)
                continue;
            const rows = Search.collectRows(page, []);
            for (const row of rows) {
                if (known[row.rowId])
                    continue;
                known[row.rowId] = true;
                additions.push({
                    moduleId: module.moduleId,
                    category: module.text,
                    rowId: row.rowId,
                    label: row.label,
                    description: row.description,
                    keywords: []
                });
            }
        }

        if (additions.length > 0)
            root.searchIndex = root.searchIndex.concat(additions);
    }

    function clearSearch(): void {
        if (searchField.text.length > 0) {
            searchField.text = "";
        }
        listview.filterText = "";
    }

    function visibleModuleIndex(moduleId: string): int {
        let idx = 0;
        for (const module of root.modules) {
            if (!module.visible) {
                continue;
            }
            if (module.moduleId == moduleId) {
                return idx;
            }
            ++idx;
        }
        return -1;
    }

    function pageForModule(module: KirigamiSettings.ConfigurationModule): var {
        if (pageCache[module.moduleId]) {
            return pageCache[module.moduleId];
        } else {
            const component = module.page();
            if (!component) {
                console.error("Unable to create component for moduleId", module.moduleId);
                return null;
            }

            if (component.status === Component.Error) {
                console.error(`Unable to create component for moduleId: "${module.moduleId}". Error: ${component.errorString()}`);
                return null;
            }

            const page = component.createObject(root, module.initialProperties());
            if (page.hasOwnProperty("hostPageStack")) {
                page.hostPageStack = root.pageStack;
            }
            // Every category page is a FormCardPage. On window refocus its
            // ScrollablePage scrolls the focused control into view and
            // miscomputes a full page-width horizontal offset, so the flickable
            // is shoved sideways and rebounds to 0 — replaying the page slide-in
            // on every refocus. These pages never scroll horizontally, so pin
            // contentX to 0. Done here (not just in SettingsPage) so the library
            // AboutPage, which does not derive from SettingsPage, is covered too.
            if (page.flickable) {
                page.flickable.contentXChanged.connect(() => {
                    if (page.flickable.contentX !== 0)
                        page.flickable.contentX = 0;
                });
            }
            if (page.title.length === 0) {
                page.title = module.text;
            }
            pageCache[module.moduleId] = page;
            return page;
        }
    }

    function getModuleByName(moduleId: string): KirigamiSettings.ConfigurationModule {
        return modules.find(module => module.moduleId == moduleId) ?? null;
    }
}
