// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick

/**
 * Drags a downloaded media file out into a file manager or another app.
 *
 * It lives in the per-kind media components rather than in ChatBubble so a
 * plain text row instantiates nothing for it: the delegate object budget is
 * counted per row, and most rows are text.
 *
 * Only the horizontal axis starts a drag, so flicking the conversation
 * vertically still scrolls it.
 */
Item {
    id: root

    /// Absolute path of the file to drag. Empty disables dragging.
    required property string localPath
    /// Selection mode owns every gesture in the row.
    property bool blocked: false

    Drag.dragType: Drag.Automatic
    Drag.supportedActions: Qt.CopyAction
    Drag.mimeData: ({
        "text/uri-list": "file://" + root.localPath
    })

    DragHandler {
        id: dragHandler

        enabled: root.localPath.length > 0 && !root.blocked
        target: null
        xAxis.enabled: true
        yAxis.enabled: false

        // Assigned, not bound: the drag system writes Drag.active itself when a
        // Drag.Automatic drag ends, and a binding fighting those writes is a
        // binding loop (one per media row, every run).
        onActiveChanged: root.Drag.active = dragHandler.active
    }
}
