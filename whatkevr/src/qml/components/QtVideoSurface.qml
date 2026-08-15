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
    // hasVideo goes true when the media has been probed and a video track is
    // being decoded. Paused counts: this asks whether there is a picture on
    // screen, and a paused clip is showing one. Requiring PlayingState meant
    // every paused clip reported no picture, fell back to the bubble's
    // buffering phase, and sat under a spinner that then timed out into a
    // failure it had not had.
    surfaceHasFrame: player.hasVideo
                     && (player.playbackState === MediaPlayer.PlayingState
                         || player.playbackState === MediaPlayer.PausedState)
                     && (player.mediaStatus === MediaPlayer.BufferedMedia
                         || player.mediaStatus === MediaPlayer.BufferingMedia
                         || player.mediaStatus === MediaPlayer.EndOfMedia)

    // Everything the engine does before it can draw. A `media.stream` source
    // reads through the daemon's range server, where a seek into bytes that
    // have not arrived blocks until they do; on a large clip that is routinely
    // several seconds of entirely healthy waiting.
    surfaceStalled: player.mediaStatus === MediaPlayer.LoadingMedia
                    || player.mediaStatus === MediaPlayer.StalledMedia
                    || player.mediaStatus === MediaPlayer.BufferingMedia
                    || root.pendingSeek >= 0

    function surfaceSeek(seconds) {
        const target = Math.max(0, seconds)
        if (!player.seekable) {
            // Seeking an unseekable player is silently dropped, which is how a
            // scrub issued the moment a clip opened did nothing at all. Hold it
            // until the engine says it can.
            root.pendingSeek = target
            return
        }
        root.pendingSeek = -1
        player.position = Math.round(target * 1000)
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

    /// Whether startPosition has been honoured for the file now loaded. Qt has
    /// no equivalent of mpv's --start, so this is a seek, and it has to happen
    /// exactly once per load: repeating it on every status change would drag
    /// playback back to the start position for as long as the clip ran.
    property bool startApplied: false
    /// A seek asked for while the player could not take one, in seconds, or -1.
    property real pendingSeek: -1

    onSourceChanged: {
        root.startApplied = false
        root.pendingSeek = -1
        root.surfaceFailed = false
        root.surfaceErrorText = ""
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
        onSeekableChanged: {
            if (player.seekable && root.pendingSeek >= 0) {
                const target = root.pendingSeek
                root.pendingSeek = -1
                player.position = Math.round(target * 1000)
            }
        }
        onMediaStatusChanged: {
            // Seekable only once the media is loaded; asking earlier is
            // silently dropped, which is how a resumed clip ended up back at
            // zero on this backend.
            if (!root.startApplied && root.startPosition > 0
                    && (mediaStatus === MediaPlayer.LoadedMedia
                        || mediaStatus === MediaPlayer.BufferedMedia)) {
                root.startApplied = true
                player.position = Math.round(root.startPosition * 1000)
            }
            if (mediaStatus === MediaPlayer.EndOfMedia && !root.loop) {
                root.endOfFile()
            }
        }
    }
}
