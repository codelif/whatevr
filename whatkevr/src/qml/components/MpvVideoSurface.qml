// SPDX-License-Identifier: BSD-3-Clause
pragma ComponentBehavior: Bound

import QtQuick

import Whatevr as Whatevr

// libmpv playback: hardware decoding, exact seeking, and every codec WhatsApp
// might send. The item only holds a decoder while it is playing; the pool
// decides whether it gets one at all.
VideoSurfaceBackend {
    id: root

    surfacePosition: video.position
    surfaceDuration: video.duration
    surfaceActive: video.active

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
        reserved: root.reserved
        onEndOfFile: root.endOfFile()
    }
}
