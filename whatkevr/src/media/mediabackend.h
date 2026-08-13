// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QObject>
#include <QQmlEngine>
#include <QString>

/**
 * MediaBackend decides which engine plays video, once, at startup.
 *
 * libmpv is the default: it hardware-decodes, seeks exactly and handles every
 * codec WhatsApp might send. It needs the scene graph on OpenGL, which the app
 * pins in main(). When that pinning does not take (a driver without OpenGL, or
 * a scene graph forced onto another backend), Qt Multimedia carries video
 * instead. Audio is always mpv: it needs no rendering integration at all, and
 * only mpv can change a voice note's speed without changing its pitch.
 *
 * The choice, and the reason for it, is readable from the Advanced settings
 * page so a report of "video is broken" says which engine was live.
 */
class MediaBackend : public QObject
{
    Q_OBJECT
    QML_ELEMENT
    QML_SINGLETON

    /// "mpv" or "qt".
    Q_PROPERTY(QString videoBackend READ videoBackend NOTIFY videoBackendChanged)
    /// Human-readable summary of what was chosen and why.
    Q_PROPERTY(QString description READ description NOTIFY videoBackendChanged)
    Q_PROPERTY(bool usingMpv READ usingMpv NOTIFY videoBackendChanged)
    /// Whether hardware decoding is on, mirroring the user setting.
    Q_PROPERTY(bool hardwareDecoding READ hardwareDecoding WRITE setHardwareDecoding NOTIFY hardwareDecodingChanged)

public:
    // Not defaulted, for the same reason as Settings' and ProtocolController's:
    // a default-constructible QML_SINGLETON is built by the engine itself
    // instead of through create(), which forked the QML-visible object off from
    // the one main() resolves. That fork is why the Advanced page read an empty
    // description and why no video surface ever built a decoder.
    explicit MediaBackend(QObject *parent);

    /// The process-wide instance. Both C++ (main, on startup) and QML (the
    /// Advanced settings page) talk to the same object.
    static MediaBackend *instance();
    static MediaBackend *create(QQmlEngine *qmlEngine, QJSEngine *jsEngine);

    // Every accessor resolves first. Nothing can read a placeholder and act on
    // it: an empty description on the Advanced page and a surface that never
    // built a decoder were the same bug, a resolve() that had not run yet.
    QString videoBackend() const
    {
        ensureResolved();
        return m_videoBackend;
    }
    QString description() const
    {
        ensureResolved();
        return m_description;
    }
    bool usingMpv() const
    {
        ensureResolved();
        return m_videoBackend == QLatin1String("mpv");
    }
    bool hardwareDecoding() const
    {
        return m_hardwareDecoding;
    }
    void setHardwareDecoding(bool enabled);

    /**
     * Resolves the backend, once. Safe to call at any point: it wants a window
     * to exist (the scene graph's graphics API is only knowable then) but falls
     * back to the API main() requested when there is none yet, and the
     * accessors above call it anyway on first read.
     *
     * Precedence: the WHATKEVR_VIDEO_BACKEND environment variable, then the
     * user's setting, then automatic detection.
     */
    Q_INVOKABLE void resolve();

Q_SIGNALS:
    void videoBackendChanged();
    void hardwareDecodingChanged();

private:
    bool sceneGraphIsOpenGL() const;
    void ensureResolved() const;

    QString m_videoBackend = QStringLiteral("mpv");
    QString m_description;
    bool m_hardwareDecoding = true;
    // Mutable so a const accessor can resolve on first read; resolution is
    // idempotent and observable only through videoBackendChanged.
    mutable bool m_resolved = false;
};

