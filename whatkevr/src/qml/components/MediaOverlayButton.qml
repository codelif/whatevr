// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls as Controls

import org.kde.kirigami as Kirigami

/**
 * A round button that sits on top of a video frame.
 *
 * AbstractButton with an explicit Kirigami.Icon rather than a ToolButton: a
 * ToolButton whose background is replaced draws no glyph at all here, which is
 * what the "stray dark blob on the rim" in DN17 turned out to be. Every
 * over-video control in the app is one of these, so there is one answer to how
 * big it is, how dark its plate is and what colour its icon takes, instead of
 * three copies that had drifted apart.
 *
 * The icon is white rather than the theme's text colour, which is chosen for
 * contrast against the window and not against whatever frame happens to be
 * underneath.
 */
Controls.AbstractButton {
    id: root

    /// Symbolic icon name.
    property alias iconName: icon.source
    /// Diameter, in whole pixels so the plate stays a circle.
    property real diameter: Kirigami.Units.gridUnit * 1.8

    implicitWidth: Math.round(diameter)
    implicitHeight: Math.round(diameter)
    hoverEnabled: true

    Controls.ToolTip.text: text
    Controls.ToolTip.visible: hovered && text.length > 0
    Controls.ToolTip.delay: Kirigami.Units.toolTipDelay
    Accessible.name: text

    background: Rectangle {
        radius: width / 2
        color: Qt.alpha("black", root.hovered || root.down ? 0.66 : 0.45)

        Behavior on color {
            ColorAnimation { duration: Kirigami.Units.shortDuration }
        }
    }

    contentItem: Kirigami.Icon {
        id: icon

        color: "white"
        isMask: true
        implicitWidth: Math.round(root.diameter * 0.5)
        implicitHeight: implicitWidth
    }
}
