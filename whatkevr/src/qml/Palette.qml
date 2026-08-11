pragma Singleton

import QtQuick

// One app-wide SystemPalette pinned to the Active colour group.
//
// Kirigami.Theme.highlightColor follows the window's active/inactive palette
// group and greys out when the window loses focus, so accent surfaces (bubble
// fill, read ticks) read from here instead to stay vivid regardless of focus.
//
// This used to be a `SystemPalette` declared inside ChatBubble, i.e. one more
// QObject on every message row for a value that is identical across the whole
// app (DN9).
SystemPalette {
    colorGroup: SystemPalette.Active
}
