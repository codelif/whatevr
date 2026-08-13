// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QHash>
#include <QObject>
#include <QQmlEngine>
#include <QUrl>

class MpvCore;

/**
 * AudioPlayer is the app's single audio stream: one voice note or audio file
 * plays at a time, which is both what users expect and what keeps a long
 * conversation from spawning a decoder per bubble.
 *
 * Bubbles do not own a player. They bind to this singleton and compare
 * messageId to decide whether the position and playing state on screen are
 * theirs, so scrolling a playing voice note out of view and back costs nothing.
 */
class AudioPlayer : public QObject
{
    Q_OBJECT
    QML_ELEMENT
    QML_SINGLETON

    /// The message currently loaded, or empty when nothing is.
    Q_PROPERTY(QString messageId READ messageId NOTIFY messageIdChanged)
    Q_PROPERTY(bool playing READ playing NOTIFY playingChanged)
    /// Playback position and total length in seconds.
    Q_PROPERTY(double position READ position NOTIFY positionChanged)
    Q_PROPERTY(double duration READ duration NOTIFY durationChanged)
    Q_PROPERTY(double speed READ speed WRITE setSpeed NOTIFY speedChanged)
    /// False when libmpv could not be created at all. Bubbles show why instead
    /// of silently doing nothing when a play button is pressed.
    Q_PROPERTY(bool available READ available CONSTANT)
    /// The last playback error, cleared when a new message loads.
    Q_PROPERTY(QString error READ error NOTIFY errorChanged)

public:
    explicit AudioPlayer(QObject *parent = nullptr);
    ~AudioPlayer() override;

    QString messageId() const
    {
        return m_messageId;
    }
    bool playing() const;
    double position() const;
    double duration() const;
    double speed() const;
    void setSpeed(double speed);
    bool available() const;
    QString error() const
    {
        return m_error;
    }

    /**
     * Plays a message, remembering where the last one left off.
     *
     * durationHint comes from the message row so the bubble can render a real
     * scrub bar before mpv has opened the file.
     */
    Q_INVOKABLE void play(const QString &messageId, const QUrl &source, double durationHint = 0.0);
    /// Plays messageId if it is not playing, pauses it if it is, and switches
    /// to it if something else is playing.
    Q_INVOKABLE void toggle(const QString &messageId, const QUrl &source, double durationHint = 0.0);
    Q_INVOKABLE void pause();
    Q_INVOKABLE void stop();
    Q_INVOKABLE void seek(double seconds);
    /// Cycles 1x, 1.5x, 2x, which is the set WhatsApp offers.
    Q_INVOKABLE double cycleSpeed();

    /// Position for a message that is not the loaded one: its remembered
    /// resume point, so a half-listened note shows where it stopped.
    Q_INVOKABLE double resumePosition(const QString &messageId) const;

Q_SIGNALS:
    void messageIdChanged();
    void playingChanged();
    void positionChanged();
    void durationChanged();
    void speedChanged();
    void errorChanged();
    /// Emitted when a message finishes on its own, so the conversation can
    /// advance to the next voice note the way WhatsApp does.
    void finished(const QString &messageId);
    /// Emitted the first time a message actually starts playing, which is when
    /// a played receipt is owed.
    void started(const QString &messageId);

private:
    void rememberPosition();
    void setError(const QString &error);

    MpvCore *m_core = nullptr;
    QString m_messageId;
    QString m_error;
    double m_durationHint = 0.0;
    bool m_startedReported = false;

    /// Resume points by message id. Deliberately in memory only: where you got
    /// to in a voice note is not worth persisting across restarts.
    QHash<QString, double> m_resumePositions;
};

