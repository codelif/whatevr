// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick

/**
 * The interface both playback engines present to the rest of the app.
 *
 * Declaring it as a real type (rather than duck-typing whatever a Loader
 * produced) is what lets VideoSurface hand out typed, compiled property
 * accesses instead of dynamic lookups on QObject.
 */
Item {
    id: root

    property url source
    property string messageId
    property bool playing: false
    property bool muted: false
    property bool loop: false
    /// Playback speed (1.0 is normal) and volume (0-100). Only the full-screen
    /// viewer sets these; a bubble plays at 1x.
    property real speed: 1.0
    property real volume: 100
    /// Claims the decoder pool's reserved core instead of an inline one.
    property bool reserved: false

    /// Playback position and length in seconds, and whether a decoder is
    /// actually attached. Subclasses bind these to their engine.
    property real surfacePosition: 0
    property real surfaceDuration: 0
    property bool surfaceActive: false

    signal endOfFile

    function surfaceSeek(seconds) {
    }
}
