// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick
import QtMultimedia

import Whatevr as Whatevr

// Qt Multimedia video playback, exposed through the small interface shared by
// the inline bubble and full-screen viewer.
VideoSurfaceBackend {
    id: root

    // Qt Multimedia counts in milliseconds; the rest of the app counts in
    // seconds.
    surfacePosition: player.position / 1000
    surfaceDuration: player.duration / 1000
    // "A decoder is attached", which a paused player still has. Equating it
    // with PlayingState made pause indistinguishable from stopped.
    surfaceActive: player.playbackState !== MediaPlayer.StoppedState
    // Frame truth comes from the sink, not from playback state. Deriving it
    // from playbackState and mediaStatus meant every excursion the FFmpeg
    // backend takes through LoadedMedia around a seek, and the StoppedState it
    // parks in at EndOfMedia, dropped the picture that was still on screen:
    // the poster popped back over a frame the sink was perfectly happy with.
    surfaceHasFrame: root.sinkFrameSeen && player.hasVideo

    // Everything the engine does before it can draw. A `media.stream` source
    // reads through the daemon's range server, where a seek into bytes that
    // have not arrived blocks until they do; on a large clip that is routinely
    // several seconds of entirely healthy waiting.
    surfaceStalled: player.mediaStatus === MediaPlayer.LoadingMedia
                    || player.mediaStatus === MediaPlayer.StalledMedia
                    || player.mediaStatus === MediaPlayer.BufferingMedia
                    || root.surfaceSeeking

    function surfaceSeek(seconds) {
        let target = Math.max(0, seconds)
        // Sender-declared durations are frequently rounded or simply wrong;
        // once the decoder knows the real length, never aim past it. A seek
        // beyond EOF settles in StoppedState and looks like a hang.
        if (player.duration > 0) {
            target = Math.min(target, Math.max(0, player.duration / 1000 - 0.1))
        }
        root.surfaceSeekTarget = target
        root.seekIssued = false
        seekSettleTimer.restart()
        root.flushSeek()
    }

    /// Hands the held target to the player if it can take one right now.
    /// Called again from every signal that could make it possible, so a seek
    /// asked for at the wrong moment is retried instead of silently dropped.
    function flushSeek() {
        if (root.surfaceSeekTarget < 0 || root.seekIssued || !player.seekable) {
            return
        }
        root.seekIssued = true
        player.position = Math.round(root.surfaceSeekTarget * 1000)
        // The position write alone can leave the FFmpeg backend's pipeline
        // wedged: audio drains its buffer from the old position and video
        // waits on the demuxer, until a play() resets both renderers. This is
        // why a stalled seek used to recover the moment the user paused and
        // resumed by hand.
        if (root.playing) {
            player.play()
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

    onPlayingChanged: {
        if (root.playing) {
            player.play()
        } else {
            player.pause()
        }
    }

    Component.onCompleted: {
        if (root.playing) {
            player.play()
        }
    }

    /// Whether startPosition has been consumed for the file now loaded. Qt has
    /// no equivalent of mpv's --start, so this is a seek, and it has to happen
    /// exactly once per load: repeating it on every status change would drag
    /// playback back to the start position for as long as the clip ran.
    property bool startConsumed: false
    /// Whether the held seek target has been handed to the player yet. The
    /// target outlives the handoff so the UI can keep displaying it until the
    /// engine demonstrably lands there.
    property bool seekIssued: false
    /// Latched true on the first decoded frame the sink receives for this
    /// source. The one honest answer to "is there a picture on screen".
    property bool sinkFrameSeen: false

    onSourceChanged: {
        root.startConsumed = false
        root.surfaceSeekTarget = -1
        root.seekIssued = false
        root.sinkFrameSeen = false
        seekSettleTimer.stop()
        root.surfaceFailed = false
        root.surfaceErrorText = ""
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

    // A seek that neither lands nor fails within this window is abandoned so
    // surfaceStalled clears and the failure backstops above re-arm. A held
    // target used to pin stalled forever, which disabled exactly the timers
    // meant to catch a wedged seek.
    Timer {
        id: seekSettleTimer

        interval: 8000
        onTriggered: root.surfaceSeekTarget = -1
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

    MediaPlayer {
        id: player

        videoOutput: output
        source: root.source
        loops: root.loop ? MediaPlayer.Infinite : 1
        // Qt's FFmpeg backend shifts pitch with rate, which is why audio-only
        // playback never comes here; for video it is the lesser evil.
        playbackRate: root.speed
        audioOutput: AudioOutput {
            muted: root.muted
            volume: root.volume / 100
        }
        // The only authority on whether playback failed. Everything above used
        // to infer it from a stopwatch, which said "could not be played" about
        // clips that were merely slow.
        onErrorOccurred: (error, errorString) => {
            if (error === MediaPlayer.NoError) {
                return
            }
            root.surfaceFailed = true
            root.surfaceErrorText = errorString
        }
        onSeekableChanged: root.flushSeek()
        onPositionChanged: {
            if (root.surfaceSeekTarget < 0 || !root.seekIssued) {
                return
            }
            // The engine landed near the target (FFmpeg snaps to a keyframe,
            // hence the generous epsilon), or playback demonstrably moved on
            // from wherever the seek put it. Either way the seek is over.
            const at = player.position / 1000
            if (Math.abs(at - root.surfaceSeekTarget) < 1.5
                    || (player.mediaStatus === MediaPlayer.BufferedMedia
                        && player.playbackState === MediaPlayer.PlayingState)) {
                root.surfaceSeekTarget = -1
                seekSettleTimer.stop()
            }
        }
        onMediaStatusChanged: {
            // Seekable only once the media is loaded; asking earlier is
            // silently dropped, which is how a resumed clip ended up back at
            // zero on this backend. Routed through the same held-target seek
            // as a user scrub, so a resume that hits a not-yet-seekable
            // network source is retried rather than lost for the session.
            if (mediaStatus === MediaPlayer.LoadedMedia
                    || mediaStatus === MediaPlayer.BufferedMedia) {
                // LoadedMedia is a load boundary: a position handed to the
                // player before it belongs to the previous load and must be
                // handed over again.
                if (mediaStatus === MediaPlayer.LoadedMedia) {
                    root.seekIssued = false
                }
                if (!root.startConsumed && root.startPosition > 0) {
                    root.startConsumed = true
                    root.surfaceSeek(root.startPosition)
                } else {
                    root.flushSeek()
                }
            }
            if (mediaStatus === MediaPlayer.EndOfMedia && !root.loop) {
                root.endOfFile()
            }
        }
    }
}
