// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick
import QtMultimedia

import Whatevr as Whatevr

// The view half of Qt Multimedia playback: a VideoOutput bound to a shared
// PlaybackSession, exposed through the small interface shared by the inline
// bubble and full-screen viewer.
//
// The MediaPlayer no longer lives here. It lives in the session, which the
// arbiter owns and which outlives this item; moving a clip between the bubble
// and the viewer reassigns the session's sink to whichever of them is showing
// it, on a decoder that never stops. This item only draws frames and pushes
// the owner's wishes (playing, muted, volume) into the session it holds.
VideoSurfaceBackend {
    id: root

    /// The engine, handed over by VideoSurface when the arbiter grants one,
    /// and taken away (set null) before a revocation reaches the owner so a
    /// stopping view can never pause a session that was just transferred.
    property Whatevr.PlaybackSession session: null

    surfacePosition: session ? session.position : 0
    surfaceDuration: session ? session.duration : 0
    surfaceActive: session ? session.active : false
    // Frame truth comes from the sink, not from playback state. Deriving it
    // from playbackState and mediaStatus meant every excursion the FFmpeg
    // backend takes through LoadedMedia around a seek, and the StoppedState it
    // parks in at EndOfMedia, dropped the picture that was still on screen:
    // the poster popped back over a frame the sink was perfectly happy with.
    surfaceHasFrame: root.sinkFrameSeen && (session ? session.hasVideo : false)
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
        const sink = output.videoSink
        if (!sink) {
            return
        }
        // A sink that has never been handed a frame reports undefined rather
        // than an invalid QVideoFrame, which is not a value the C++ signature
        // can take at all.
        const frame = sink.videoFrame
        if (frame === undefined || frame === null) {
            return
        }
        Whatevr.VideoPlayback.captureFrame(root.messageId, frame)
    }

    /// Latched true on the first decoded frame the sink receives. The one
    /// honest answer to "is there a picture on screen".
    property bool sinkFrameSeen: false

    // The owner's wishes, pushed into the session rather than bound: two
    // views exist for a moment during a handoff, and only the one holding the
    // session may steer it.
    onPlayingChanged: if (session) session.playing = playing
    onMutedChanged: if (session) session.muted = muted
    onLoopChanged: if (session) session.loop = loop
    onSpeedChanged: if (session) session.rate = speed
    onVolumeChanged: if (session) session.volume = volume

    onSessionChanged: {
        sinkFrameSeen = false
        if (!session) {
            return
        }
        session.attachSink(output.videoSink)
        session.playing = playing
        session.muted = muted
        session.loop = loop
        session.rate = speed
        session.volume = volume
        // A session picked up mid-flight (a handoff) may already be drawing;
        // the sink only signals on the next frame, which a paused clip never
        // delivers. Ask directly.
        if (output.videoSink && output.videoSink.videoFrame !== undefined
                && output.videoSink.videoFrame !== null) {
            sinkFrameSeen = true
        }
    }

    Connections {
        target: root.session

        function onEndOfMedia() {
            root.endOfFile()
        }
    }

    // Watching the sink instead of playbackState; disabled once latched so a
    // running clip is not paying a JS call per decoded frame.
    Connections {
        target: output.videoSink
        enabled: !root.sinkFrameSeen

        function onVideoFrameChanged() {
            root.sinkFrameSeen = true
        }
    }

    VideoOutput {
        id: output

        anchors.fill: parent
        fillMode: VideoOutput.PreserveAspectFit
        // Hidden until it has something to show. Qt Multimedia decodes into the
        // sink whether or not this is painted, so hiding it costs nothing.
        // Painted while empty it is a black rectangle, which on a video note
        // showed as black corners around the circle.
        visible: root.surfaceHasFrame
    }
}
