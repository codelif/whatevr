// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QImage>
#include <QObject>
#include <QSize>
#include <QString>
#include <QUrl>

#include <memory>

struct mpv_handle;

/**
 * Shared ownership of one mpv_handle.
 *
 * A render context is built from a handle on the scene graph's render thread
 * and freed there; the core that created the handle lives on the GUI thread and
 * can be destroyed at any time. Without a shared owner the GUI thread wins that
 * race and tears the handle down while a live render context still points at
 * it. MpvQt solves it the same way (MpvHandleManager), and this is where the
 * idea comes from.
 */
struct MpvHandleOwner {
    explicit MpvHandleOwner(mpv_handle *handle);
    ~MpvHandleOwner();

    MpvHandleOwner(const MpvHandleOwner &) = delete;
    MpvHandleOwner &operator=(const MpvHandleOwner &) = delete;

    mpv_handle *handle = nullptr;
};

/**
 * MpvCore is a thin RAII wrapper around one libmpv instance.
 *
 * libmpv delivers events on its own thread through a wakeup callback; this
 * class funnels those onto the GUI thread with a queued signal, so every
 * property below is safe to read and every slot safe to call from QML.
 *
 * It deliberately knows nothing about bubbles or messages. AudioPlayer owns one
 * for voice notes; PlaybackSession owns one per live video.
 */
class MpvCore : public QObject
{
    Q_OBJECT

public:
    /// A video core renders through the scene graph and needs a render context
    /// before it may open a file; an audio-only core never touches the GPU.
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
    /// A reference the render thread can hold, so the handle outlives any
    /// render context built from it however the two are torn down.
    std::shared_ptr<MpvHandleOwner> handleOwner() const
    {
        return m_handle;
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
    /// The decoded picture's size, known only once mpv has a frame. A non-zero
    /// width is the honest answer to "is there something to show".
    QSize videoSize() const
    {
        return m_videoSize;
    }
    /// mpv is landing a seek, or restarting playback after one.
    bool seeking() const
    {
        return m_seeking;
    }
    /// Playback is stopped waiting for the cache to refill: a streamed clip
    /// that has outrun its download.
    bool waitingForCache() const
    {
        return m_waitingForCache;
    }
    /// mpv has nothing to do: opening a file, or run dry. Only meaningful
    /// alongside the pause and end flags.
    bool coreIdle() const
    {
        return m_coreIdle;
    }
    /// Sitting on the last frame with keep-open, rather than having ended.
    bool endReached() const
    {
        return m_endReached;
    }
    QString errorText() const
    {
        return m_errorText;
    }

    /// Loads a file or URL, optionally opening it partway in. Passing an empty
    /// URL stops playback and unloads.
    void load(const QUrl &source, bool autoplay, double startSeconds = 0.0);
    void play();
    void pause();
    void togglePlaying();
    void stop();
    void seek(double seconds);
    void setSpeed(double speed);
    void setMuted(bool muted);
    void setLooping(bool looping);
    void setVolume(double volume);

    /**
     * Re-opens the video track on a file that is already playing.
     *
     * Freeing a render context takes mpv's video output with it, and the file
     * keeps playing with its video track switched off; a new context on the
     * same handle does not bring the picture back on its own. Cycling the track
     * does, and it leaves audio and position alone, which is what makes moving
     * a running clip between two views survivable.
     */
    void reopenVideoTrack();

    /// The frame on screen, straight out of mpv. Null if there is none.
    QImage grabStill() const;

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
    /// Any of seeking, cache-waiting, idling or end-reached moved. They are one
    /// signal because every caller reads them as one question: is this engine
    /// working on something, or is it showing me the clip.
    void progressStateChanged();
    /// Playback reached the end of the file (not emitted while looping).
    void endOfFile();
    void errorOccurred(const QString &message);

private:
    void observeProperties();
    void command(const QStringList &args);
    void setProperty(const QString &name, const QVariant &value);
    /// Applies a flag property change and reports whether it moved.
    static bool updateFlag(bool &target, bool value);

    Mode m_mode = Mode::Audio;
    mpv_handle *m_mpv = nullptr;
    std::shared_ptr<MpvHandleOwner> m_handle;

    QUrl m_source;
    bool m_playing = false;
    double m_position = 0.0;
    double m_duration = 0.0;
    double m_speed = 1.0;
    bool m_muted = false;
    bool m_looping = false;
    QSize m_videoSize;
    bool m_seeking = false;
    bool m_waitingForCache = false;
    bool m_coreIdle = false;
    bool m_endReached = false;
    QString m_errorText;
};
