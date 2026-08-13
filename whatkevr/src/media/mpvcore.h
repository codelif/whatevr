// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QObject>
#include <QSize>
#include <QString>
#include <QUrl>

struct mpv_handle;

/**
 * MpvCore is a thin RAII wrapper around one libmpv instance.
 *
 * libmpv delivers events on its own thread through a wakeup callback; this
 * class funnels those onto the GUI thread with a queued signal, so every
 * property below is safe to read and every slot safe to call from QML.
 *
 * It deliberately knows nothing about bubbles or messages: AudioPlayer owns one
 * of these for voice notes, and MpvPool hands them to video items.
 */
class MpvCore : public QObject
{
    Q_OBJECT

public:
    /// Video cores create a render context; audio-only cores never touch the
    /// scene graph and are correspondingly cheap.
    enum class Mode {
        Audio,
        Video,
    };

    explicit MpvCore(Mode mode, QObject *parent = nullptr);

    /**
     * Pins LC_NUMERIC to "C", which libmpv requires and refuses to run without.
     *
     * QApplication's constructor calls setlocale(LC_ALL, "") and would otherwise
     * leave every mpv_create() returning null, with playback silently doing
     * nothing. main() calls this once at startup; the constructor calls it too,
     * so no later locale change can take playback out again.
     */
    static void ensureNumericLocale();
    ~MpvCore() override;

    bool isValid() const
    {
        return m_mpv != nullptr;
    }
    mpv_handle *handle() const
    {
        return m_mpv;
    }

    QUrl source() const
    {
        return m_source;
    }
    bool playing() const
    {
        return m_playing;
    }
    double position() const
    {
        return m_position;
    }
    double duration() const
    {
        return m_duration;
    }
    double speed() const
    {
        return m_speed;
    }
    bool muted() const
    {
        return m_muted;
    }
    bool looping() const
    {
        return m_looping;
    }
    /// Video size, or a null size until the first frame is decoded.
    QSize videoSize() const
    {
        return m_videoSize;
    }

    /// Loads a file or URL. Passing an empty URL stops playback and unloads.
    void load(const QUrl &source, bool autoplay);
    void play();
    void pause();
    void togglePlaying();
    void stop();
    void seek(double seconds);
    void setSpeed(double speed);
    void setMuted(bool muted);
    void setLooping(bool looping);
    void setVolume(double volume);

    /// Applies the user's hardware-decoding preference. Video only.
    void setHardwareDecoding(bool enabled);

public Q_SLOTS:
    /// Drains mpv's event queue. Called on the GUI thread in response to
    /// libmpv's wakeup callback, never directly.
    void handleEventsFromMpv();

Q_SIGNALS:
    void playingChanged();
    void positionChanged();
    void durationChanged();
    void speedChanged();
    void mutedChanged();
    void videoSizeChanged();
    /// A new frame is ready; MpvVideoItem turns this into update().
    void frameReady();
    /// Playback reached the end of the file (not emitted while looping).
    void endOfFile();
    void errorOccurred(const QString &message);

private:
    void observeProperties();
    void command(const QStringList &args);
    void setProperty(const QString &name, const QVariant &value);

    mpv_handle *m_mpv = nullptr;
    Mode m_mode = Mode::Audio;

    QUrl m_source;
    bool m_playing = false;
    double m_position = 0.0;
    double m_duration = 0.0;
    double m_speed = 1.0;
    bool m_muted = false;
    bool m_looping = false;
    QSize m_videoSize;
};

