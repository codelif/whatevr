// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick

import Whatevr as Whatevr

// libmpv playback: hardware decoding, exact seeking, and every codec WhatsApp
// might send. The item only holds a decoder while it is playing, and only ever
// gets here having already been allowed to play by VideoPlaybackArbiter, one
// layer up in VideoSurface.
VideoSurfaceBackend {
    id: root

    surfacePosition: video.position
    surfaceDuration: video.duration
    surfaceActive: video.active
    // mpv reports dwidth/dheight once it has actually decoded a frame, so a
    // non-zero width is the first moment there is something to draw.
    surfaceHasFrame: video.active && video.videoWidth > 0 && video.videoHeight > 0

    function surfaceSeek(seconds) {
        video.seek(seconds)
    }

    Whatevr.MpvVideo {
        id: video

        anchors.fill: parent
        messageId: root.messageId
        source: root.source
        playing: root.playing
        muted: root.muted
        loop: root.loop
        speed: root.speed
        volume: root.volume
        startPosition: root.startPosition
        onEndOfFile: root.endOfFile()
    }
}
