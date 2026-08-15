// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QAudioOutput>
#include <QMediaPlayer>
#include <QObject>
#include <QPointer>
#include <QQmlEngine>
#include <QString>
#include <QTimer>
#include <QUrl>
#include <QVideoSink>

/**
 * One live playback: a decoder, its audio, and the seek machine, with no view
 * attached.
 *
 * A session outlives any single view of it. The inline bubble and the
 * full-screen viewer each bring their own VideoOutput and attach its sink;
 * moving a clip between them reassigns the sink on a player that never stops,
 * which is what makes the handoff gapless. The previous design gave each view
 * its own MediaPlayer, so every handoff was a teardown, a fresh open, and a
 * seek, with the audio silent for all of it.
 *
 * Sessions are owned and pooled by VideoPlaybackArbiter; views receive one
 * from acquire() and must never destroy or reconfigure it themselves.
 */
class PlaybackSession : public QObject
{
    Q_OBJECT
    QML_ELEMENT
    QML_UNCREATABLE("Sessions are acquired from VideoPlayback")

    Q_PROPERTY(QString messageId READ messageId NOTIFY messageIdChanged)
    Q_PROPERTY(QUrl source READ source NOTIFY sourceChanged)
    Q_PROPERTY(bool playing READ playing WRITE setPlaying NOTIFY playingChanged)
    Q_PROPERTY(bool muted READ muted WRITE setMuted NOTIFY mutedChanged)
    Q_PROPERTY(double volume READ volume WRITE setVolume NOTIFY volumeChanged)
    Q_PROPERTY(double rate READ rate WRITE setRate NOTIFY rateChanged)
    Q_PROPERTY(bool loop READ loop WRITE setLoop NOTIFY loopChanged)
    Q_PROPERTY(double position READ position NOTIFY positionChanged)
    Q_PROPERTY(double duration READ duration NOTIFY durationChanged)
    Q_PROPERTY(bool seeking READ seeking NOTIFY seekingChanged)
    Q_PROPERTY(double seekTarget READ seekTarget NOTIFY seekingChanged)
    Q_PROPERTY(bool busy READ busy NOTIFY busyChanged)
    Q_PROPERTY(bool failed READ failed NOTIFY failedChanged)
    Q_PROPERTY(QString errorText READ errorText NOTIFY failedChanged)
    Q_PROPERTY(bool hasVideo READ hasVideo NOTIFY hasVideoChanged)
    Q_PROPERTY(bool active READ active NOTIFY activeChanged)
    Q_PROPERTY(bool atEnd READ atEnd NOTIFY atEndChanged)

public:
    explicit PlaybackSession(QObject *parent = nullptr);

    [[nodiscard]] QString messageId() const { return m_messageId; }
    [[nodiscard]] QUrl source() const { return m_player.source(); }
    [[nodiscard]] bool playing() const { return m_playing; }
    [[nodiscard]] bool muted() const { return m_audio.isMuted(); }
    [[nodiscard]] double volume() const { return m_volume; }
    [[nodiscard]] double rate() const { return m_player.playbackRate(); }
    [[nodiscard]] bool loop() const { return m_loop; }
    [[nodiscard]] double position() const { return m_player.position() / 1000.0; }
    [[nodiscard]] double duration() const { return m_player.duration() / 1000.0; }
    [[nodiscard]] bool seeking() const { return m_seekTarget >= 0.0; }
    [[nodiscard]] double seekTarget() const { return m_seekTarget; }
    [[nodiscard]] bool busy() const;
    [[nodiscard]] bool failed() const { return m_failed; }
    [[nodiscard]] QString errorText() const { return m_errorText; }
    [[nodiscard]] bool hasVideo() const { return m_player.hasVideo(); }
    [[nodiscard]] bool active() const { return m_player.playbackState() != QMediaPlayer::StoppedState; }
    [[nodiscard]] bool atEnd() const { return m_player.mediaStatus() == QMediaPlayer::EndOfMedia; }
    [[nodiscard]] bool audible() const { return m_playing && !muted() && m_volume > 0.0; }

    void setPlaying(bool playing);
    void setMuted(bool muted);
    void setVolume(double volume);
    void setRate(double rate);
    void setLoop(bool loop);

    /// Points this session at a message and a source, from rest. Only the
    /// arbiter calls this, and only on a session that is parked.
    void configure(const QString &messageId, const QUrl &source, double startAt);

    /// Swaps the source underneath a live session, keeping position and play
    /// state: what a stream promoted to a completed local file wants.
    void promoteSource(const QUrl &source);

    /// Stops and forgets everything so the session can be handed to another
    /// message. The player object itself is reused; opening a decoder is the
    /// expensive part, a resting QMediaPlayer is not.
    void park();

    /// The view showing this session hands its sink in; the previous sink, if
    /// any, simply stops receiving frames. Audio is untouched, which is the
    /// whole trick of the bubble to full screen handoff.
    Q_INVOKABLE void attachSink(QVideoSink *sink);
    /// Detaches only if this sink is still the attached one, so a view being
    /// torn down cannot yank the sink a newer view just attached.
    Q_INVOKABLE void detachSink(QVideoSink *sink);

    Q_INVOKABLE void seek(double seconds);

    /// A finished clip playing again from the top. play() on a player parked
    /// at EndOfMedia restarts from zero; if the engine refuses to leave
    /// StoppedState the watchdog re-opens the source, preserving the old
    /// guarantee that replay never resumes an exhausted engine.
    Q_INVOKABLE void replayFromStart();

Q_SIGNALS:
    void messageIdChanged();
    void sourceChanged();
    void playingChanged();
    void mutedChanged();
    void volumeChanged();
    void rateChanged();
    void loopChanged();
    void positionChanged();
    void durationChanged();
    void seekingChanged();
    void busyChanged();
    void failedChanged();
    void hasVideoChanged();
    void activeChanged();
    void atEndChanged();
    void endOfMedia();
    /// This session started producing sound, or stopped being silent (unmuted
    /// mid-play). What the audio player pauses voice notes on.
    void audiblePlaybackStarted();

private:
    void flushSeek();
    void handleMediaStatus(QMediaPlayer::MediaStatus status);
    void handlePosition(qint64 positionMs);
    void resetSeekState();
    void maybeAnnounceAudible();

    /// FFmpeg lands seeks on a keyframe, so "arrived" is a wide net.
    static constexpr double seekSettleEpsilon = 1.5;

    QMediaPlayer m_player;
    QAudioOutput m_audio;
    /// The attached sink, guarded: a view (and its sink) can be destroyed at
    /// any moment while the session lives on, and the player must never be
    /// left holding a dangling sink pointer.
    QPointer<QVideoSink> m_sink;
    QString m_messageId;
    QTimer m_seekSettleTimer;
    QTimer m_replayWatchdog;

    bool m_playing = false;
    bool m_loop = false;
    double m_volume = 100.0;
    double m_seekTarget = -1.0;
    bool m_seekIssued = false;
    bool m_failed = false;
    QString m_errorText;
    bool m_wasAtEnd = false;
    bool m_wasAudible = false;
};
