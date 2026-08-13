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
    surfaceActive: player.playbackState === MediaPlayer.PlayingState

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

    VideoOutput {
        id: output

        anchors.fill: parent
        fillMode: VideoOutput.PreserveAspectFit
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
            if (mediaStatus === MediaPlayer.EndOfMedia && !root.loop) {
                root.endOfFile()
            }
        }
    }
}
