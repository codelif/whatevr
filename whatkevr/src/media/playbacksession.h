// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QImage>
#include <QObject>
#include <QPointer>
#include <QQmlEngine>
#include <QQuickItem>
#include <QQuickWindow>
#include <QString>
#include <QTimer>
#include <QUrl>

class MpvCore;
class MpvVideoItem;

/**
 * One live playback: a libmpv core, its audio, and the item that draws it.
 *
 * A session outlives any single view of it. The inline bubble and the
 * full-screen viewer each offer a container item, and the session moves its
 * one video item between them; nothing is reopened, reseeked or restarted, so
 * the handoff costs a reparent. The previous design gave each view its own
 * player, and every handoff was a teardown, a fresh open and a seek, with the
 * audio silent for all of it.
 *
 * Sessions are owned and pooled by VideoPlaybackArbiter; views receive one from
 * acquire() and must never destroy or reconfigure it themselves.
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

    [[nodiscard]] QString messageId() const
    {
        return m_messageId;
    }
    [[nodiscard]] QUrl source() const;
    [[nodiscard]] bool playing() const
    {
        return m_playing;
    }
    [[nodiscard]] bool muted() const
    {
        return m_muted;
    }
    [[nodiscard]] double volume() const
    {
        return m_volume;
    }
    [[nodiscard]] double rate() const
    {
        return m_rate;
    }
    [[nodiscard]] bool loop() const
    {
        return m_loop;
    }
    [[nodiscard]] double position() const;
    [[nodiscard]] double duration() const;
    [[nodiscard]] bool seeking() const
    {
        return m_seekTarget >= 0.0;
    }
    [[nodiscard]] double seekTarget() const
    {
        return m_seekTarget;
    }
    [[nodiscard]] bool busy() const;
    [[nodiscard]] bool failed() const
    {
        return m_failed;
    }
    [[nodiscard]] QString errorText() const
    {
        return m_errorText;
    }
    [[nodiscard]] bool hasVideo() const;
    [[nodiscard]] bool active() const
    {
        return !m_source.isEmpty();
    }
    [[nodiscard]] bool atEnd() const;
    [[nodiscard]] bool audible() const
    {
        return m_playing && !m_muted && m_volume > 0.0;
    }

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
    /// message. The core and its video item are kept: creating them is the part
    /// worth pooling, a resting mpv instance is not.
    void park();

    /**
     * The view showing this session offers a container to draw in.
     *
     * The item moves; it is never rebuilt. Freeing mpv's render context
     * switches the video track of the playing file off, and a fresh context
     * does not turn it back on, so a handoff that rebuilt the item would hand
     * over sound and a black rectangle.
     */
    Q_INVOKABLE void attachView(QQuickItem *container);
    /// Detaches only if this container is still the one showing the session, so
    /// a view being torn down cannot yank the item a newer view just took.
    Q_INVOKABLE void detachView(QQuickItem *container);

    Q_INVOKABLE void seek(double seconds);

    /// A finished clip playing again from the top.
    Q_INVOKABLE void replayFromStart();

    /// Hands the frame on screen to whoever is keeping stills.
    Q_INVOKABLE void captureStill();

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
    /// A still was grabbed for this message. The arbiter stores it; the session
    /// deliberately knows nothing about that store.
    void stillGrabbed(const QString &messageId, const QImage &image);

private:
    void applyStateToCore();
    void loadIfReady();
    void handlePosition();
    void handleProgressState();
    void resetSeekState();
    void maybeAnnounceAudible();
    void syncItemGeometry();
    /// Where the video item waits while no view is showing it: somewhere in the
    /// same scene, so its render context is not rebuilt on the way back.
    [[nodiscard]] QQuickItem *parkingHolder() const;

    /// mpv seeks exactly, so a landing is a landing; the slack is for the frame
    /// or two of playback that can pass before the position is reported.
    static constexpr double seekSettleEpsilon = 0.75;

    MpvCore *m_core = nullptr;
    /// Created with the first view that asks for one, then kept for the life of
    /// the session and moved between views.
    MpvVideoItem *m_item = nullptr;
    QPointer<QQuickItem> m_container;
    /// The window the last view lived in, which is the scene the item is parked
    /// in between views.
    QPointer<QQuickWindow> m_window;

    QString m_messageId;
    QUrl m_source;
    double m_startAt = 0.0;
    /// Whether the current source has been handed to mpv. False while waiting
    /// for a render context, which must exist before a file is opened.
    bool m_loaded = false;

    QTimer m_seekSettleTimer;

    bool m_playing = false;
    bool m_muted = false;
    bool m_loop = false;
    double m_volume = 100.0;
    double m_rate = 1.0;
    double m_seekTarget = -1.0;
    bool m_failed = false;
    QString m_errorText;
    bool m_wasAtEnd = false;
    bool m_wasAudible = false;
    bool m_wasBusy = false;
};
