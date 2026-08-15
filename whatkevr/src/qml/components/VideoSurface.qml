// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick

import Whatevr as Whatevr

/**
 * The lazy Qt Multimedia video surface and the only place that asks
 * permission to play.
 *
 * Everything above this file (VideoBubble, MediaViewer) treats video as one
 * thing: a source, a playing flag, a position. The decoder is instantiated
 * only after arbitration grants this surface a slot.
 *
 * Permission belongs here rather than inside the player. That keeps decoder
 * limits and exclusive playback consistent for every caller.
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
    /// The decoder said this cannot be played, and what it said. A caller should
    /// report failure on this and nothing else; an absent picture on its own
    /// only ever means "not yet".
    readonly property bool failed: backend ? backend.surfaceFailed : false
    readonly property string errorText: backend ? backend.surfaceErrorText : ""
    /// The engine is opening, seeking or refilling. Show a spinner, never a
    /// failure.
    readonly property bool stalled: backend ? backend.surfaceStalled : false
    /// The seek in flight and where it is headed. The transport displays the
    /// target while one is pending so the clock does not keep running from
    /// the pre-seek point.
    readonly property bool seeking: backend ? backend.surfaceSeeking : false
    readonly property real seekTarget: backend ? backend.surfaceSeekTarget : -1
    /// The clip ran to its end and is parked on its last frame. Playing again
    /// starts over, and the transport should offer that as a replay.
    readonly property bool atEnd: backend ? backend.surfaceAtEnd : false
    // Decoder errors and source teardown can reset position to zero before the
    // owner gets a chance to hand off. Keep the last meaningful value here.
    property real lastUsablePosition: 0

    function handoffPosition() {
        return position > 0 ? position : lastUsablePosition
    }

    onPositionChanged: {
        if (position > 0)
            lastUsablePosition = position
    }
    onMessageIdChanged: lastUsablePosition = 0

    signal endOfFile
    /// Something else took the exclusive lane. The caller must stop wanting to
    /// play; leaving `playing` true would show transport controls over a
    /// surface that is not going to produce a frame.
    signal revoked
    /// The session was paused from outside this surface (a voice note took
    /// the audio focus). The owner must stop wanting playback, or its
    /// transport keeps claiming a clip is running that is not.
    signal externallyPaused

    function seek(seconds) {
        if (backend) {
            backend.surfaceSeek(seconds)
        }
    }

    /// Writes down the picture this clip is showing, from the one place that
    /// has it. Paired with rememberPosition(): between them they are everything
    /// the next surface to open this message needs to carry on without a visible
    /// break.
    function rememberStill() {
        if (messageId.length === 0 || !backend || !hasFrame) {
            return
        }
        backend.captureStill()
    }

    /// Both halves of letting go, in the order that works: the caller may be
    /// about to drop the grant, and neither can be read afterwards.
    function rememberPlaybackState() {
        rememberStill()
        rememberPosition()
    }

    // ---- Arbitration ----

    /// Whether the arbiter has allowed this surface to play. False either
    /// because nothing was asked for, or because the animated lane is full and
    /// this surface is queued for the next slot.
    property bool grantHeld: false
    readonly property bool wantsGrant: engaged && source.toString().length > 0

    /// The engine the arbiter handed this surface. If a session for this
    /// message was already live somewhere (the same clip inline while the
    /// viewer opens), acquire() transfers it running rather than building a
    /// second decoder, which is what makes the handoff gapless.
    property Whatevr.PlaybackSession session: null

    function syncGrant() {
        if (wantsGrant) {
            if (!grantHeld) {
                session = Whatevr.VideoPlayback.acquire(root, messageId, source, startPosition, lane)
                grantHeld = session !== null
            }
            return
        }
        rememberPlaybackState()
        // The reference goes before the release: releasing pauses the session,
        // and a surface still watching it would report that teardown pause as
        // an external one.
        session = null
        // Unconditional, even when nothing is held: it is also how a surface
        // gives up its place in the animated queue, which a bubble scrolled out
        // of view has to do or it is handed a slot it no longer wants.
        Whatevr.VideoPlayback.release(root)
        grantHeld = false
    }

    // The source changing underneath a held grant is a promotion: the stream
    // this session opened on finished downloading and the caller switched to
    // the file. One seamless swap inside the session, not a rebuild.
    onSourceChanged: {
        if (grantHeld && wantsGrant && session) {
            Whatevr.VideoPlayback.updateSource(root, source)
        }
    }

    /// Writes down where this clip got to, from the one place that knows: this
    /// runs while the decoder is still attached and still has a position, which
    /// is not true of anything watching from further up.
    function rememberPosition() {
        if (messageId.length === 0 || !grantHeld)
            return
        const at = handoffPosition()
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
        rememberPlaybackState()
        session = null
        Whatevr.VideoPlayback.release(root)
    }

    // The session pausing while this surface still wants to play can only be
    // someone else's doing (the voice-note player taking the audio focus);
    // every path this surface initiates drops the reference first.
    Connections {
        target: root.session

        function onPlayingChanged() {
            if (root.session && !root.session.playing && root.playing && root.grantHeld) {
                root.externallyPaused()
            }
        }
    }

    Connections {
        target: Whatevr.VideoPlayback

        function onRevoked(claimant) {
            if (claimant !== root)
                return
            // Before grantHeld drops, not after. The owner reacts to revoked()
            // by stopping, which runs syncGrant(), whose rememberPlaybackState()
            // declines to record anything once the grant is gone: a clip
            // revoked by another one silently lost both its position and its
            // picture.
            root.rememberPlaybackState()
            // The session may already belong to whoever revoked this surface.
            // Let go of the reference before the owner's stop cascade runs, so
            // this view's teardown cannot pause a clip mid-handoff.
            root.session = null
            root.grantHeld = false
            root.revoked()
        }

        function onGrantOffered(claimant) {
            if (claimant !== root)
                return
            if (root.wantsGrant && !root.grantHeld) {
                root.session = Whatevr.VideoPlayback.acquire(root, root.messageId, root.source, root.startPosition, root.lane)
                root.grantHeld = root.session !== null
            }
        }
    }

    Loader {
        id: loader

        objectName: "videoSurface.backendLoader"
        anchors.fill: parent
        // An idle bubble is only a poster. Do not build the view layer merely
        // because the row entered ListView's cache band. The decoder itself
        // lives in the arbiter's session; replay after EOF goes through
        // PlaybackSession::replayFromStart, which keeps the old guarantee that
        // an exhausted engine is never resumed into.
        active: root.grantHeld
        sourceComponent: qtComponent

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
        id: qtComponent

        QtVideoSurface {
            messageId: root.messageId
            session: root.session
            playing: root.playing && root.grantHeld
            muted: root.muted
            loop: root.loop
            speed: root.speed
            volume: root.volume
        }
    }
}
