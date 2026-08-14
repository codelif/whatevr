// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick
import QtMultimedia

// The fallback engine, used when the scene graph is not on OpenGL and mpv
// therefore cannot draw into our items. Everything here is expressed in the
// same terms as the mpv path, so nothing above VideoSurface can tell them apart.
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
    // being decoded, which is Qt's equivalent of mpv's dwidth landing. Paused
    // counts: this asks whether there is a picture on screen, and a paused clip
    // is showing one. Requiring PlayingState meant every paused clip reported
    // no picture, fell back to the bubble's buffering phase, and sat under a
    // spinner that then timed out into a failure it had not had.
    surfaceHasFrame: player.hasVideo
                     && (player.playbackState === MediaPlayer.PlayingState
                         || player.playbackState === MediaPlayer.PausedState)
                     && (player.mediaStatus === MediaPlayer.BufferedMedia
                         || player.mediaStatus === MediaPlayer.BufferingMedia
                         || player.mediaStatus === MediaPlayer.EndOfMedia)

    function surfaceSeek(seconds) {
        player.position = Math.round(seconds * 1000)
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

    onSourceChanged: root.startApplied = false

    VideoOutput {
        id: output

        anchors.fill: parent
        fillMode: VideoOutput.PreserveAspectFit
        // Hidden until it has something to show. Unlike mpv, whose decoding is
        // driven by the scene graph rendering its item, Qt Multimedia decodes
        // into the sink whether or not this is painted, so hiding it costs
        // nothing. Painted while empty it is a black rectangle, which on a
        // video note showed as black corners around the circle.
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
