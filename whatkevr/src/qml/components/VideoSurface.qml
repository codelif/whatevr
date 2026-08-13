// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick

import Whatevr as Whatevr

/**
 * The seam between the two playback engines.
 *
 * Everything above this file (VideoBubble, MediaViewer) treats video as one
 * thing: a source, a playing flag, a position. Underneath it is either libmpv
 * or Qt Multimedia, chosen once at startup by MediaBackend, and both present
 * the same VideoSurfaceBackend interface.
 */
Item {
    id: root

    property url source
    property string messageId
    property bool playing: false
    property bool muted: false
    property bool loop: false
    property real speed: 1.0
    property real volume: 100
    /// Set by the full-screen viewer so it plays on the pool's reserved core.
    property bool reserved: false

    readonly property VideoSurfaceBackend backend: loader.item as VideoSurfaceBackend
    readonly property real position: backend ? backend.surfacePosition : 0
    readonly property real duration: backend ? backend.surfaceDuration : 0
    /// True while a decoder is actually attached. A bubble that could not get
    /// one keeps showing its thumbnail.
    readonly property bool active: backend ? backend.surfaceActive : false

    signal endOfFile

    function seek(seconds) {
        if (backend) {
            backend.surfaceSeek(seconds)
        }
    }

    Loader {
        id: loader

        anchors.fill: parent
        // Reading usingMpv is what resolves the engine, so this binding is
        // already correct the first time it runs: there is no startup default
        // to build a decoder on and tear down a tick later, and no flag to be
        // stuck false and leave the surface without a backend at all.
        sourceComponent: Whatevr.MediaBackend.usingMpv ? mpvComponent : qtComponent

        // Both the connect and the disconnect live here: a Loader can replace
        // or drop its item at any time, and a stale relay would deliver
        // endOfFile once per past incarnation.
        onItemChanged: {
            if (root.relayedBackend) {
                root.relayedBackend.endOfFile.disconnect(root.endOfFile)
            }
            const surface = item as VideoSurfaceBackend
            if (surface) {
                surface.endOfFile.connect(root.endOfFile)
            }
            root.relayedBackend = surface
        }
    }

    /// The surface the endOfFile relay is currently attached to.
    property VideoSurfaceBackend relayedBackend: null

    Component {
        id: mpvComponent

        MpvVideoSurface {
            messageId: root.messageId
            source: root.source
            playing: root.playing
            muted: root.muted
            loop: root.loop
            speed: root.speed
            volume: root.volume
            reserved: root.reserved
        }
    }

    Component {
        id: qtComponent

        QtVideoSurface {
            messageId: root.messageId
            source: root.source
            playing: root.playing
            muted: root.muted
            loop: root.loop
            speed: root.speed
            volume: root.volume
            reserved: root.reserved
        }
    }
}
