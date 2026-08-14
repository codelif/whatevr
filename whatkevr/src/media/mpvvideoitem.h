// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QQmlEngine>
#include <QQuickFramebufferObject>
#include <QUrl>

class MpvCore;

/**
 * MpvVideoItem renders one libmpv instance into the Qt Quick scene graph.
 *
 * mpv's render API produces OpenGL, so this is a QQuickFramebufferObject and
 * the app pins the OpenGL RHI backend at startup (see main.cpp). Rendering into
 * the item's own framebuffer rather than the window's is what keeps the UI
 * frame rate independent of the video's.
 *
 * The item does not own its core: MpvPool hands one over while the item is
 * actually playing and takes it back when it is not, so a conversation full of
 * videos never holds more than a few decoders alive.
 */
class MpvVideoItem : public QQuickFramebufferObject
{
    Q_OBJECT
    QML_NAMED_ELEMENT(MpvVideo)

    /// Media to play. Setting it claims a core from the pool.
    Q_PROPERTY(QUrl source READ source WRITE setSource NOTIFY sourceChanged)
    /// Identifies the message this item is showing, so the pool can report
    /// which bubble lost its core.
    Q_PROPERTY(QString messageId READ messageId WRITE setMessageId NOTIFY messageIdChanged)
    Q_PROPERTY(bool playing READ playing WRITE setPlaying NOTIFY playingChanged)
    Q_PROPERTY(bool muted READ muted WRITE setMuted NOTIFY mutedChanged)
    Q_PROPERTY(bool loop READ loop WRITE setLoop NOTIFY loopChanged)
    Q_PROPERTY(double position READ position NOTIFY positionChanged)
    Q_PROPERTY(double duration READ duration NOTIFY durationChanged)
    /// True while this item holds a core. A bubble that cannot get one keeps
    /// showing its thumbnail instead.
    Q_PROPERTY(bool active READ active NOTIFY activeChanged)
    /// Playback speed and volume, for the full-screen viewer's transport.
    Q_PROPERTY(double speed READ speed WRITE setSpeed NOTIFY speedChanged)
    Q_PROPERTY(double volume READ volume WRITE setVolume NOTIFY volumeChanged)
    /// Where the file should open, in seconds. Applied as mpv's --start at load
    /// rather than as a seek afterwards, so resuming does not flash the opening
    /// frame first. Set it before playing; changing it later has no effect
    /// until the next load.
    Q_PROPERTY(double startPosition READ startPosition WRITE setStartPosition NOTIFY startPositionChanged)
    Q_PROPERTY(int videoWidth READ videoWidth NOTIFY videoSizeChanged)
    Q_PROPERTY(int videoHeight READ videoHeight NOTIFY videoSizeChanged)

public:
    explicit MpvVideoItem(QQuickItem *parent = nullptr);
    ~MpvVideoItem() override;

    Renderer *createRenderer() const override;

    QUrl source() const
    {
        return m_source;
    }
    void setSource(const QUrl &source);

    QString messageId() const
    {
        return m_messageId;
    }
    void setMessageId(const QString &messageId);

    bool playing() const;
    void setPlaying(bool playing);
    bool muted() const
    {
        return m_muted;
    }
    void setMuted(bool muted);
    bool loop() const
    {
        return m_loop;
    }
    void setLoop(bool loop);

    double position() const;
    double duration() const;
    double speed() const
    {
        return m_speed;
    }
    void setSpeed(double speed);
    double volume() const
    {
        return m_volume;
    }
    void setVolume(double volume);
    double startPosition() const
    {
        return m_startPosition;
    }
    void setStartPosition(double seconds);
    bool active() const
    {
        return m_core != nullptr;
    }
    int videoWidth() const;
    int videoHeight() const;

    Q_INVOKABLE void seek(double seconds);
    Q_INVOKABLE void togglePlaying();
    /// Gives the core back to the pool and returns to showing a thumbnail.
    Q_INVOKABLE void release();

    /**
     * The renderer telling us mpv's render context now exists.
     *
     * Nothing may be loaded before this. mpv initializes the video output when
     * it opens a file, and with `vo=libmpv` and no render context yet it logs
     * "No render context set", reports "Video: no video", and plays the file
     * through to eof with the video track disabled. The context arriving a
     * frame later does not undo that: the decision is made per file, at load.
     * MpvQt has the same handshake, as MpvAbstractItem::ready().
     */
    Q_INVOKABLE void notifyRendererReady();

    MpvCore *core() const
    {
        return m_core;
    }

Q_SIGNALS:
    void sourceChanged();
    void messageIdChanged();
    void playingChanged();
    void mutedChanged();
    void loopChanged();
    void positionChanged();
    void durationChanged();
    void activeChanged();
    void videoSizeChanged();
    void speedChanged();
    void volumeChanged();
    void startPositionChanged();
    void endOfFile();

private:
    void attachCore();
    void detachCore();
    void applyStateToCore();

    MpvCore *m_core = nullptr;
    QUrl m_source;
    QString m_messageId;
    /// Whether mpv's render context exists, and whether the current core has
    /// actually been given the file yet. See notifyRendererReady().
    bool m_rendererReady = false;
    bool m_loadedIntoCore = false;
    bool m_wantPlaying = false;
    bool m_muted = false;
    bool m_loop = false;
    double m_startPosition = 0.0;
    double m_speed = 1.0;
    double m_volume = 100.0;
};

