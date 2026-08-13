// SPDX-License-Identifier: BSD-3-Clause
#include "mediabackend.h"

#include "mpvcore.h"
#include "mpvpool.h"
#include "settings.h"

#include <QDebug>
#include <QGuiApplication>
#include <QQmlEngine>
#include <QQuickWindow>
#include <QSGRendererInterface>
#include <QWindow>

#include <KLocalizedString>

namespace
{
constexpr auto kBackendEnvVar = "WHATKEVR_VIDEO_BACKEND";
}

MediaBackend *MediaBackend::instance()
{
    static MediaBackend backend(nullptr);
    return &backend;
}

MediaBackend *MediaBackend::create(QQmlEngine *, QJSEngine *)
{
    // The engine would otherwise take ownership of a statically-stored object.
    QQmlEngine::setObjectOwnership(instance(), QQmlEngine::CppOwnership);
    return instance();
}

MediaBackend::MediaBackend(QObject *parent)
    : QObject(parent)
{
    m_hardwareDecoding = Settings::instance()->hardwareDecoding();
    connect(Settings::instance(), &Settings::hardwareDecodingChanged, this, [this] {
        setHardwareDecoding(Settings::instance()->hardwareDecoding());
    });
}

void MediaBackend::ensureResolved() const
{
    if (m_resolved) {
        return;
    }
    const_cast<MediaBackend *>(this)->resolve();
}

void MediaBackend::resolve()
{
    if (m_resolved) {
        return;
    }
    m_resolved = true;

    // Whatever branch below is taken, it ends in announce(): the chosen engine
    // and the reason for it belong in the log, so a report of "video does not
    // play" carries them without anyone opening the settings page.
    // The signal goes out through the event loop, not from here: the first read
    // that resolves is usually a QML binding on usingMpv, and emitting inside
    // its evaluation invalidates the binding that is running, which Qt reports
    // as a binding loop.
    const auto announce = [this] {
        qInfo("whatkevr: video engine: %s", qPrintable(m_description));
        QMetaObject::invokeMethod(this, &MediaBackend::videoBackendChanged, Qt::QueuedConnection);
    };

    const QString override = qEnvironmentVariable(kBackendEnvVar).trimmed().toLower();
    if (override == QLatin1String("qt")) {
        m_videoBackend = QStringLiteral("qt");
        m_description = i18nc("@info media playback engine", "Qt Multimedia, forced by %1", QLatin1String(kBackendEnvVar));
        announce();
        return;
    }
    if (override == QLatin1String("mpv")) {
        m_videoBackend = QStringLiteral("mpv");
        m_description = i18nc("@info media playback engine", "mpv, forced by %1", QLatin1String(kBackendEnvVar));
        announce();
        return;
    }

    const QString preference = Settings::instance()->videoBackend();
    if (preference == QLatin1String("qt")) {
        m_videoBackend = QStringLiteral("qt");
        m_description = i18nc("@info media playback engine", "Qt Multimedia, chosen in settings");
        announce();
        return;
    }

    const bool forcedMpv = preference == QLatin1String("mpv");
    if (!sceneGraphIsOpenGL()) {
        // mpv's render API is OpenGL; on any other scene graph backend it
        // cannot draw into our items at all.
        m_videoBackend = QStringLiteral("qt");
        m_description = forcedMpv
            ? i18nc("@info media playback engine", "Qt Multimedia: mpv was requested, but the scene graph is not using OpenGL")
            : i18nc("@info media playback engine", "Qt Multimedia: the scene graph is not using OpenGL");
        announce();
        return;
    }

    // A core that will not initialize (no libmpv at runtime, a broken driver)
    // is worth finding out about now rather than on the first tapped video.
    MpvCore probe(MpvCore::Mode::Video);
    if (!probe.isValid()) {
        m_videoBackend = QStringLiteral("qt");
        m_description = i18nc("@info media playback engine", "Qt Multimedia: mpv could not be initialized");
        announce();
        return;
    }

    m_videoBackend = QStringLiteral("mpv");
    m_description = m_hardwareDecoding
        ? i18nc("@info media playback engine", "mpv, with hardware decoding")
        : i18nc("@info media playback engine", "mpv, with software decoding");
    announce();
}

bool MediaBackend::sceneGraphIsOpenGL() const
{
    // Ask a real window: the scene graph's graphics API is only settled once
    // one exists, and main() pins OpenGL before any window is created.
    const auto windows = QGuiApplication::topLevelWindows();
    for (QWindow *window : windows) {
        if (auto *quickWindow = qobject_cast<QQuickWindow *>(window)) {
            if (auto *renderer = quickWindow->rendererInterface()) {
                return renderer->graphicsApi() == QSGRendererInterface::OpenGL;
            }
        }
    }
    // No window yet: fall back to what was requested, which main() has set.
    return QQuickWindow::graphicsApi() == QSGRendererInterface::OpenGL;
}

void MediaBackend::setHardwareDecoding(bool enabled)
{
    if (m_hardwareDecoding == enabled) {
        return;
    }
    m_hardwareDecoding = enabled;
    MpvPool::instance()->setHardwareDecoding(enabled);
    if (usingMpv()) {
        m_description = enabled
            ? i18nc("@info media playback engine", "mpv, with hardware decoding")
            : i18nc("@info media playback engine", "mpv, with software decoding");
        Q_EMIT videoBackendChanged();
    }
    Q_EMIT hardwareDecodingChanged();
}

