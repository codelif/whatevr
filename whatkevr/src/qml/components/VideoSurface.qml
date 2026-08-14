// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick

import Whatevr as Whatevr

/**
 * The seam between the two playback engines, and the only place that asks
 * permission to play.
 *
 * Everything above this file (VideoBubble, MediaViewer) treats video as one
 * thing: a source, a playing flag, a position. Underneath it is either libmpv
 * or Qt Multimedia, chosen once at startup by MediaBackend, and both present
 * the same VideoSurfaceBackend interface.
 *
 * Permission belongs here rather than in either engine. Expressed inside
 * MpvPool it did not exist at all on the Qt Multimedia path, where each bubble
 * builds its own MediaPlayer and every clip in the conversation played at once.
 * Asking once, above the Loader, makes the rule true of both by construction.
 */
Item {
    id: root

    property url source
    property string messageId
    /// Whether this surface should hold a decoder at all. Separate from
    /// `playing` because pausing means two different things in the two places
    /// this is used: a bubble drops the decoder and goes back to its thumbnail,
    /// while the full-screen viewer has to keep the frame it is showing.
    property bool engaged: false
    /// Whether the decoder should be running. Whether it happens is the
    /// arbiter's call, so this is a request rather than a command.
    property bool playing: false
    property bool muted: false
    property bool loop: false
    property real speed: 1.0
    property real volume: 100
    /// Where to open the file. Applied at load, so set it before playing.
    property real startPosition: 0
    /// Which arbitration lane this surface plays in: Exclusive for video and
    /// video notes (one at a time, app-wide), Animated for GIFs (several, up to
    /// the user's limit).
    property int lane: Whatevr.VideoPlayback.Exclusive

    readonly property VideoSurfaceBackend backend: loader.item as VideoSurfaceBackend
    readonly property real position: backend ? backend.surfacePosition : 0
    readonly property real duration: backend ? backend.surfaceDuration : 0
    /// True while a decoder is actually attached. A bubble that could not get
    /// one keeps showing its thumbnail.
    readonly property bool active: backend ? backend.surfaceActive : false
    /// True once there is a decoded frame to show. This, not `active`, is what
    /// callers should swap their thumbnail out on.
    readonly property bool hasFrame: backend ? backend.surfaceHasFrame : false

    signal endOfFile
    /// Something else took the exclusive lane. The caller must stop wanting to
    /// play; leaving `playing` true would show transport controls over a
    /// surface that is not going to produce a frame.
    signal revoked

    function seek(seconds) {
        if (backend) {
            backend.surfaceSeek(seconds)
        }
    }

    // ---- Arbitration ----

    /// Whether the arbiter has allowed this surface to play. False either
    /// because nothing was asked for, or because the animated lane is full and
    /// this surface is queued for the next slot.
    property bool grantHeld: false
    readonly property bool wantsGrant: engaged && source.toString().length > 0

    function syncGrant() {
        if (wantsGrant) {
            if (!grantHeld) {
                grantHeld = Whatevr.VideoPlayback.request(root, lane)
            }
            return
        }
        rememberPosition()
        // Unconditional, even when nothing is held: it is also how a surface
        // gives up its place in the animated queue, which a bubble scrolled out
        // of view has to do or it is handed a slot it no longer wants.
        Whatevr.VideoPlayback.release(root)
        grantHeld = false
    }

    /// Writes down where this clip got to, from the one place that knows: this
    /// runs while the decoder is still attached and still has a position, which
    /// is not true of anything watching from further up.
    function rememberPosition() {
        if (messageId.length === 0 || !grantHeld)
            return
        const at = position
        const total = duration
        // Not worth resuming at either end: a clip that barely started should
        // start, and one that ran to its end should not open on its last frame.
        if (at > 0.5 && (total <= 0 || at < total - 0.5))
            Whatevr.VideoPlayback.setResumePosition(messageId, at)
        else
            Whatevr.VideoPlayback.clearResumePosition(messageId)
    }

    onWantsGrantChanged: syncGrant()
    // A surface built already wanting to play (an autoplaying GIF on screen
    // from the first frame) never sees that change signal.
    Component.onCompleted: syncGrant()
    Component.onDestruction: {
        rememberPosition()
        Whatevr.VideoPlayback.release(root)
    }

    Connections {
        target: Whatevr.VideoPlayback

        function onRevoked(claimant) {
            if (claimant !== root)
                return
            root.grantHeld = false
            root.revoked()
        }

        function onGrantOffered(claimant) {
            if (claimant !== root)
                return
            if (root.wantsGrant && !root.grantHeld)
                root.grantHeld = Whatevr.VideoPlayback.request(root, root.lane)
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
            // Gated on the grant, so a decoder only ever exists for a surface
            // that was allowed one. That is what keeps the number of live
            // decoders inside MpvPool's ceiling without the pool having to
            // refuse anyone, which is where the never-ending spinner came from.
            source: root.grantHeld ? root.source : ""
            startPosition: root.startPosition
            // The grant, not the request: this is the single point where "may
            // play" turns into "is playing", for either engine.
            playing: root.playing && root.grantHeld
            muted: root.muted
            loop: root.loop
            speed: root.speed
            volume: root.volume
        }
    }

    Component {
        id: qtComponent

        QtVideoSurface {
            messageId: root.messageId
            source: root.grantHeld ? root.source : ""
            startPosition: root.startPosition
            playing: root.playing && root.grantHeld
            muted: root.muted
            loop: root.loop
            speed: root.speed
            volume: root.volume
        }
    }
}
