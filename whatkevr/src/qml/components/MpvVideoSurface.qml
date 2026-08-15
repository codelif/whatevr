// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick

import Whatevr as Whatevr

// The view half of libmpv playback: a container the session draws into, bound
// to the shared PlaybackSession, exposed through the small interface the inline
// bubble and the full-screen viewer share.
//
// The engine does not live here. It lives in the session, which the arbiter
// owns and which outlives this item; moving a clip between the bubble and the
// viewer moves the session's video item into whichever container is showing it,
// on a decoder that never stops. This item only offers that container and
// pushes the owner's wishes (playing, muted, volume) into the session it holds.
VideoSurfaceBackend {
    id: root

    /// The engine, handed over by VideoSurface when the arbiter grants one, and
    /// taken away (set null) before a revocation reaches the owner so a
    /// stopping view can never pause a session that was just transferred.
    property Whatevr.PlaybackSession session: null

    surfacePosition: session ? session.position : 0
    surfaceDuration: session ? session.duration : 0
    surfaceActive: session ? session.active : false
    // mpv reports a displayed size once it has actually decoded a frame, which
    // is the first moment there is anything to show. Deriving this from play
    // state instead put the poster back over a picture on every seek.
    surfaceHasFrame: session ? session.hasVideo : false
    surfaceStalled: session ? session.busy : false
    surfaceSeekTarget: session ? session.seekTarget : -1
    surfaceAtEnd: session ? session.atEnd : false
    surfaceFailed: session ? session.failed : false
    surfaceErrorText: session ? session.errorText : ""

    function surfaceSeek(seconds) {
        if (session) {
            session.seek(seconds)
        }
    }

    function captureStill() {
        if (session) {
            session.captureStill()
        }
    }

    // The owner's wishes, pushed into the session rather than bound: two views
    // exist for a moment during a handoff, and only the one holding the session
    // may steer it.
    onPlayingChanged: if (session) session.playing = playing
    onMutedChanged: if (session) session.muted = muted
    onLoopChanged: if (session) session.loop = loop
    onSpeedChanged: if (session) session.rate = speed
    onVolumeChanged: if (session) session.volume = volume

    /// The session this view is currently showing, which is not always the one
    /// in `session`: that property is cleared the moment a handoff starts, and
    /// the container it was drawing into still has to be given back.
    property Whatevr.PlaybackSession attachedSession: null

    onSessionChanged: {
        if (attachedSession && attachedSession !== session) {
            attachedSession.detachView(videoArea)
        }
        attachedSession = session
        if (!session) {
            return
        }
        // Wishes first, container second: attaching is what lets the session
        // open its file, and a GIF that was opened before its mute arrived got
        // to be audible for those few milliseconds.
        session.playing = playing
        session.muted = muted
        session.loop = loop
        session.rate = speed
        session.volume = volume
        session.attachView(videoArea)
    }

    Component.onCompleted: {
        if (session) {
            attachedSession = session
            session.playing = playing
            session.muted = muted
            session.loop = loop
            session.rate = speed
            session.volume = volume
            session.attachView(videoArea)
        }
    }
    // Explicit, and before the container goes: the session keeps its video item
    // and must not be left holding a visual parent that is being destroyed.
    Component.onDestruction: if (attachedSession) attachedSession.detachView(videoArea)

    Connections {
        target: root.session

        function onEndOfMedia() {
            root.endOfFile()
        }
    }

    Item {
        id: videoArea

        anchors.fill: parent
        // The session parents its video item here. Nothing else may: the item
        // is shared, and this container exists only to give it a place and a
        // size in this view.
    }
}
