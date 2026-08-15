// SPDX-License-Identifier: BSD-3-Clause
#include "playbacksession.h"

#include <algorithm>
#include <cmath>

#include <QVideoSink>

PlaybackSession::PlaybackSession(QObject *parent)
    : QObject(parent)
{
    m_player.setAudioOutput(&m_audio);
    m_audio.setVolume(1.0);

    // A seek that neither lands nor fails within this window is abandoned so
    // busy clears and the failure backstops above re-arm; a held target used
    // to pin the spinner forever and disable exactly the timers meant to
    // catch a wedged seek.
    m_seekSettleTimer.setSingleShot(true);
    m_seekSettleTimer.setInterval(8000);
    connect(&m_seekSettleTimer, &QTimer::timeout, this, [this]() {
        if (m_seekTarget >= 0.0) {
            m_seekTarget = -1.0;
            Q_EMIT seekingChanged();
            Q_EMIT busyChanged();
        }
    });

    m_replayWatchdog.setSingleShot(true);
    m_replayWatchdog.setInterval(500);
    connect(&m_replayWatchdog, &QTimer::timeout, this, [this]() {
        if (m_playing && m_player.playbackState() == QMediaPlayer::StoppedState) {
            // The engine refused to restart in place. Re-open the source: a
            // fresh engine is the one thing guaranteed not to be exhausted.
            const QUrl source = m_player.source();
            m_player.setSource(QUrl());
            m_player.setSource(source);
            m_player.play();
        }
    });

    connect(&m_player, &QMediaPlayer::positionChanged, this, [this](qint64 positionMs) {
        handlePosition(positionMs);
        Q_EMIT positionChanged();
    });
    connect(&m_player, &QMediaPlayer::durationChanged, this, &PlaybackSession::durationChanged);
    connect(&m_player, &QMediaPlayer::hasVideoChanged, this, &PlaybackSession::hasVideoChanged);
    connect(&m_player, &QMediaPlayer::playbackStateChanged, this, &PlaybackSession::activeChanged);
    connect(&m_player, &QMediaPlayer::seekableChanged, this, [this](bool) {
        flushSeek();
    });
    connect(&m_player, &QMediaPlayer::mediaStatusChanged, this, [this](QMediaPlayer::MediaStatus status) {
        handleMediaStatus(status);
    });
    connect(&m_player, &QMediaPlayer::errorOccurred, this, [this](QMediaPlayer::Error error, const QString &errorString) {
        if (error == QMediaPlayer::NoError) {
            return;
        }
        m_failed = true;
        m_errorText = errorString;
        Q_EMIT failedChanged();
    });
    connect(&m_audio, &QAudioOutput::mutedChanged, this, &PlaybackSession::mutedChanged);
}

bool PlaybackSession::busy() const
{
    const QMediaPlayer::MediaStatus status = m_player.mediaStatus();
    return status == QMediaPlayer::LoadingMedia
        || status == QMediaPlayer::StalledMedia
        || status == QMediaPlayer::BufferingMedia
        || seeking();
}

void PlaybackSession::setPlaying(bool playing)
{
    if (m_playing == playing) {
        return;
    }
    m_playing = playing;
    if (playing) {
        m_player.play();
    } else {
        m_player.pause();
    }
    Q_EMIT playingChanged();
    maybeAnnounceAudible();
}

void PlaybackSession::setMuted(bool muted)
{
    if (this->muted() == muted) {
        return;
    }
    m_audio.setMuted(muted);
    maybeAnnounceAudible();
}

void PlaybackSession::setVolume(double volume)
{
    const double clamped = std::clamp(volume, 0.0, 100.0);
    if (qFuzzyCompare(m_volume, clamped)) {
        return;
    }
    m_volume = clamped;
    m_audio.setVolume(clamped / 100.0);
    Q_EMIT volumeChanged();
    maybeAnnounceAudible();
}

void PlaybackSession::setRate(double rate)
{
    if (qFuzzyCompare(m_player.playbackRate(), rate)) {
        return;
    }
    m_player.setPlaybackRate(rate);
    Q_EMIT rateChanged();
}

void PlaybackSession::setLoop(bool loop)
{
    if (m_loop == loop) {
        return;
    }
    m_loop = loop;
    m_player.setLoops(loop ? QMediaPlayer::Infinite : 1);
    Q_EMIT loopChanged();
}

void PlaybackSession::configure(const QString &messageId, const QUrl &source, double startAt)
{
    resetSeekState();
    m_failed = false;
    m_errorText.clear();
    Q_EMIT failedChanged();

    if (m_messageId != messageId) {
        m_messageId = messageId;
        Q_EMIT messageIdChanged();
    }
    m_player.setSource(source);
    Q_EMIT sourceChanged();
    if (startAt > 0.0) {
        // Held by the seek machine until the engine can take it, so a resume
        // into a source that is not seekable yet is retried, not lost.
        seek(startAt);
    }
    if (m_playing) {
        m_player.play();
    }
}

void PlaybackSession::promoteSource(const QUrl &source)
{
    if (m_player.source() == source) {
        return;
    }
    // Keep whichever destination is more current: a seek the user has in
    // flight beats the position the old source had reached.
    const double resumeAt = m_seekTarget >= 0.0 ? m_seekTarget : position();
    const bool wasPlaying = m_playing;
    resetSeekState();
    m_player.setSource(source);
    Q_EMIT sourceChanged();
    if (resumeAt > 0.0) {
        seek(resumeAt);
    }
    if (wasPlaying) {
        m_player.play();
    }
}

void PlaybackSession::park()
{
    m_replayWatchdog.stop();
    resetSeekState();
    m_playing = false;
    m_player.stop();
    m_player.setSource(QUrl());
    if (m_sink) {
        disconnect(m_sink, &QObject::destroyed, this, nullptr);
        m_sink = nullptr;
    }
    m_player.setVideoSink(nullptr);
    m_player.setPlaybackRate(1.0);
    m_player.setLoops(1);
    m_loop = false;
    m_audio.setMuted(false);
    m_volume = 100.0;
    m_audio.setVolume(1.0);
    m_failed = false;
    m_errorText.clear();
    m_wasAtEnd = false;
    m_wasAudible = false;
    if (!m_messageId.isEmpty()) {
        m_messageId.clear();
        Q_EMIT messageIdChanged();
    }
    Q_EMIT sourceChanged();
    Q_EMIT playingChanged();
    Q_EMIT failedChanged();
}

void PlaybackSession::attachSink(QVideoSink *sink)
{
    if (!sink || m_sink == sink) {
        return;
    }
    if (m_sink) {
        disconnect(m_sink, &QObject::destroyed, this, nullptr);
    }
    m_sink = sink;
    connect(sink, &QObject::destroyed, this, [this]() {
        m_sink = nullptr;
        m_player.setVideoSink(nullptr);
    });
    m_player.setVideoSink(sink);
}

void PlaybackSession::detachSink(QVideoSink *sink)
{
    if (!sink || m_sink != sink) {
        return;
    }
    disconnect(m_sink, &QObject::destroyed, this, nullptr);
    m_sink = nullptr;
    m_player.setVideoSink(nullptr);
}

void PlaybackSession::seek(double seconds)
{
    double target = std::max(0.0, seconds);
    // Sender-declared durations are frequently rounded or simply wrong; once
    // the decoder knows the real length, never aim past it. A seek beyond EOF
    // settles in StoppedState and looks like a hang.
    if (m_player.duration() > 0) {
        target = std::min(target, std::max(0.0, m_player.duration() / 1000.0 - 0.1));
    }
    const bool wasSeeking = seeking();
    m_seekTarget = target;
    m_seekIssued = false;
    m_seekSettleTimer.start();
    if (!wasSeeking) {
        Q_EMIT seekingChanged();
        Q_EMIT busyChanged();
    } else {
        Q_EMIT seekingChanged();
    }
    flushSeek();
}

void PlaybackSession::flushSeek()
{
    if (m_seekTarget < 0.0 || m_seekIssued || !m_player.isSeekable()) {
        return;
    }
    m_seekIssued = true;
    m_player.setPosition(qRound64(m_seekTarget * 1000.0));
    // The position write alone can leave the FFmpeg backend's pipeline
    // wedged: audio drains its buffer from the old position and video waits
    // on the demuxer, until a play() resets both renderers. This is why a
    // stalled seek used to recover the moment the user paused and resumed by
    // hand.
    if (m_playing) {
        m_player.play();
    }
}

void PlaybackSession::replayFromStart()
{
    resetSeekState();
    m_playing = true;
    // play() on an EndOfMedia player restarts from zero on its own; asking it
    // to seek first is what used to park a pending seek against a stopped
    // engine.
    m_player.play();
    Q_EMIT playingChanged();
    m_replayWatchdog.start();
    maybeAnnounceAudible();
}

void PlaybackSession::handleMediaStatus(QMediaPlayer::MediaStatus status)
{
    if (status == QMediaPlayer::LoadedMedia) {
        // A load boundary: a position handed to the player before it belongs
        // to the previous load and must be handed over again.
        m_seekIssued = false;
    }
    if (status == QMediaPlayer::LoadedMedia || status == QMediaPlayer::BufferedMedia) {
        flushSeek();
    }

    const bool nowAtEnd = status == QMediaPlayer::EndOfMedia;
    if (nowAtEnd != m_wasAtEnd) {
        m_wasAtEnd = nowAtEnd;
        Q_EMIT atEndChanged();
        if (nowAtEnd && !m_loop) {
            Q_EMIT endOfMedia();
        }
    }
    Q_EMIT busyChanged();
}

void PlaybackSession::handlePosition(qint64 positionMs)
{
    if (m_seekTarget < 0.0 || !m_seekIssued) {
        return;
    }
    // The engine landed near the target (FFmpeg snaps to a keyframe, hence
    // the generous epsilon), or playback demonstrably moved on from wherever
    // the seek put it. Either way the seek is over.
    const double at = positionMs / 1000.0;
    if (std::abs(at - m_seekTarget) < seekSettleEpsilon
        || (m_player.mediaStatus() == QMediaPlayer::BufferedMedia
            && m_player.playbackState() == QMediaPlayer::PlayingState)) {
        m_seekTarget = -1.0;
        m_seekSettleTimer.stop();
        Q_EMIT seekingChanged();
        Q_EMIT busyChanged();
    }
}

void PlaybackSession::resetSeekState()
{
    m_seekSettleTimer.stop();
    const bool wasSeeking = seeking();
    m_seekTarget = -1.0;
    m_seekIssued = false;
    if (wasSeeking) {
        Q_EMIT seekingChanged();
        Q_EMIT busyChanged();
    }
}

void PlaybackSession::maybeAnnounceAudible()
{
    const bool nowAudible = audible();
    if (nowAudible && !m_wasAudible) {
        Q_EMIT audiblePlaybackStarted();
    }
    m_wasAudible = nowAudible;
}
