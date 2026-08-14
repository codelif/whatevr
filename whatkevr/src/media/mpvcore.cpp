// SPDX-License-Identifier: BSD-3-Clause
#include "mpvcore.h"

#include <QDebug>
#include <QMetaObject>
#include <QStringList>
#include <QVariant>

#include <clocale>

#include <mpv/client.h>

namespace
{

void wakeup(void *ctx)
{
    // Called on an mpv thread. Hop to the GUI thread before touching anything.
    auto *core = static_cast<MpvCore *>(ctx);
    QMetaObject::invokeMethod(core, &MpvCore::handleEventsFromMpv, Qt::QueuedConnection);
}

void setOption(mpv_handle *mpv, const char *name, const char *value)
{
    if (const int status = mpv_set_option_string(mpv, name, value); status < 0) {
        qWarning("whatkevr: mpv option %s=%s rejected: %s", name, value, mpv_error_string(status));
    }
}

}

void MpvCore::ensureNumericLocale()
{
    if (const char *numeric = std::setlocale(LC_NUMERIC, nullptr);
        numeric && qstrcmp(numeric, "C") == 0) {
        return;
    }
    std::setlocale(LC_NUMERIC, "C");
}

MpvCore::MpvCore(QObject *parent)
    : QObject(parent)
{
    ensureNumericLocale();
    m_mpv = mpv_create();
    if (!m_mpv) {
        // The usual cause is a non-C LC_NUMERIC, which main.cpp resets right
        // after the QApplication constructor puts the user's locale back.
        qWarning("whatkevr: could not create an mpv instance");
        return;
    }

    // Shared behaviour. audio-pitch-correction is the reason voice notes can
    // play at 1.5x and 2x without sounding like chipmunks.
    setOption(m_mpv, "audio-pitch-correction", "yes");
    setOption(m_mpv, "keep-open", "yes");
    setOption(m_mpv, "idle", "yes");
    setOption(m_mpv, "cache", "yes");
    // Errors from mpv itself, and mpv's own account of the libav* libraries
    // underneath it, which is chatty about its hardware-decoding probe.
    //
    // Note that this does not reach the bare "Cannot load libcuda.so.1" lines
    // an Intel or AMD box gets on every decoder start. Those come out of the
    // ffnvcodec loader compiled into libmpv, and they escape mpv's log system
    // entirely: neither msg-level=all=no, nor terminal=no, nor libavutil's own
    // av_log_set_level suppresses them (all three were tried). They are noise,
    // not a failure: the probe is meant to walk the list, and the driver that
    // answers is the result, which on this machine is vaapi.
    //
    // Overridable, because when playback does not work the first question is
    // always what mpv thought it was doing, and the answer is otherwise thrown
    // away: WHATKEVR_MPV_LOG=v (or =debug, or =ffmpeg/video=trace) puts it
    // back. That switch is what found the "No render context set" bug that
    // stopped every video from playing.
    const QByteArray logOverride = qgetenv("WHATKEVR_MPV_LOG");
    const QByteArray msgLevel = logOverride.isEmpty()
        ? QByteArray("all=error,ffmpeg=no")
        : (logOverride.contains('=') ? logOverride : QByteArray("all=") + logOverride);
    setOption(m_mpv, "msg-level", msgLevel.constData());
    setOption(m_mpv, "terminal", logOverride.isEmpty() ? "no" : "yes");
    setOption(m_mpv, "config", "no");
    setOption(m_mpv, "ytdl", "no");
    // The daemon's loopback range server is the only network source we ever
    // open, and it is already authenticated by its token.
    setOption(m_mpv, "network-timeout", "30");

    setOption(m_mpv, "vid", "no");
    setOption(m_mpv, "audio-display", "no");

    // Tests run without an audio device; everything else takes mpv's own pick.
    if (const QByteArray ao = qgetenv("WHATKEVR_MPV_AO"); !ao.isEmpty()) {
        setOption(m_mpv, "ao", ao.constData());
    }

    if (const int status = mpv_initialize(m_mpv); status < 0) {
        qWarning("whatkevr: could not initialize mpv: %s", mpv_error_string(status));
        mpv_terminate_destroy(m_mpv);
        m_mpv = nullptr;
        return;
    }

    observeProperties();
    mpv_set_wakeup_callback(m_mpv, wakeup, this);
}

MpvCore::~MpvCore()
{
    if (m_mpv) {
        mpv_set_wakeup_callback(m_mpv, nullptr, nullptr);
        mpv_terminate_destroy(m_mpv);
        m_mpv = nullptr;
    }
}

void MpvCore::observeProperties()
{
    mpv_observe_property(m_mpv, 0, "time-pos", MPV_FORMAT_DOUBLE);
    mpv_observe_property(m_mpv, 0, "duration", MPV_FORMAT_DOUBLE);
    mpv_observe_property(m_mpv, 0, "pause", MPV_FORMAT_FLAG);
    mpv_observe_property(m_mpv, 0, "speed", MPV_FORMAT_DOUBLE);
    mpv_observe_property(m_mpv, 0, "mute", MPV_FORMAT_FLAG);
    // keep-open holds the last frame instead of ending the file, which means
    // MPV_EVENT_END_FILE never arrives for a clean finish. This property is how
    // the end is actually observable.
    mpv_observe_property(m_mpv, 0, "eof-reached", MPV_FORMAT_FLAG);
}

void MpvCore::command(const QStringList &args)
{
    if (!m_mpv) {
        return;
    }
    QList<QByteArray> owned;
    owned.reserve(args.size());
    std::vector<const char *> argv;
    argv.reserve(args.size() + 1);
    for (const QString &arg : args) {
        owned.append(arg.toUtf8());
        argv.push_back(owned.last().constData());
    }
    argv.push_back(nullptr);

    if (const int status = mpv_command(m_mpv, argv.data()); status < 0) {
        const QString message = QString::fromUtf8(mpv_error_string(status));
        qWarning() << "whatkevr: mpv command" << args << "failed:" << message;
        Q_EMIT errorOccurred(message);
    }
}

void MpvCore::setProperty(const QString &name, const QVariant &value)
{
    if (!m_mpv) {
        return;
    }
    const QByteArray key = name.toUtf8();
    switch (value.typeId()) {
    case QMetaType::Bool: {
        int flag = value.toBool() ? 1 : 0;
        mpv_set_property(m_mpv, key.constData(), MPV_FORMAT_FLAG, &flag);
        break;
    }
    case QMetaType::Double: {
        double number = value.toDouble();
        mpv_set_property(m_mpv, key.constData(), MPV_FORMAT_DOUBLE, &number);
        break;
    }
    default: {
        const QByteArray text = value.toString().toUtf8();
        mpv_set_property_string(m_mpv, key.constData(), text.constData());
        break;
    }
    }
}

void MpvCore::load(const QUrl &source, bool autoplay, double startSeconds)
{
    if (!m_mpv) {
        return;
    }
    m_source = source;
    m_position = startSeconds > 0.0 ? startSeconds : 0.0;
    m_duration = 0.0;
    Q_EMIT positionChanged();
    Q_EMIT durationChanged();

    if (source.isEmpty()) {
        command({QStringLiteral("stop")});
        return;
    }

    // Where the file opens, as mpv's own --start option rather than a seek
    // issued once it is playing: a seek races the first frame, so resuming a
    // clip flashed its opening frame before jumping. Set every time, including
    // back to zero, because it is a persistent option and would otherwise carry
    // into whatever this core is reused for next.
    setProperty(QStringLiteral("start"), startSeconds > 0.0 ? QString::number(startSeconds, 'f', 3) : QStringLiteral("0"));
    // Pause state is set before loading so a bubble that only wants a first
    // frame does not briefly play audio.
    setProperty(QStringLiteral("pause"), !autoplay);
    const QString target = source.isLocalFile() ? source.toLocalFile() : source.toString();
    command({QStringLiteral("loadfile"), target});
}

void MpvCore::play()
{
    setProperty(QStringLiteral("pause"), false);
}

void MpvCore::pause()
{
    setProperty(QStringLiteral("pause"), true);
}

void MpvCore::togglePlaying()
{
    setProperty(QStringLiteral("pause"), m_playing);
}

void MpvCore::stop()
{
    command({QStringLiteral("stop")});
    m_source.clear();
}

void MpvCore::seek(double seconds)
{
    if (seconds < 0.0) {
        seconds = 0.0;
    }
    command({QStringLiteral("seek"), QString::number(seconds), QStringLiteral("absolute")});
}

void MpvCore::setSpeed(double speed)
{
    if (speed <= 0.0) {
        return;
    }
    setProperty(QStringLiteral("speed"), speed);
}

void MpvCore::setMuted(bool muted)
{
    setProperty(QStringLiteral("mute"), muted);
}

void MpvCore::setLooping(bool looping)
{
    m_looping = looping;
    setProperty(QStringLiteral("loop-file"), looping ? QStringLiteral("inf") : QStringLiteral("no"));
}

void MpvCore::setVolume(double volume)
{
    setProperty(QStringLiteral("volume"), qBound(0.0, volume, 100.0));
}

void MpvCore::handleEventsFromMpv()
{
    if (!m_mpv) {
        return;
    }
    while (true) {
        mpv_event *event = mpv_wait_event(m_mpv, 0);
        if (!event || event->event_id == MPV_EVENT_NONE) {
            break;
        }
        switch (event->event_id) {
        case MPV_EVENT_PROPERTY_CHANGE: {
            auto *property = static_cast<mpv_event_property *>(event->data);
            const QLatin1StringView name(property->name);
            if (name == QLatin1StringView("time-pos") && property->format == MPV_FORMAT_DOUBLE) {
                m_position = *static_cast<double *>(property->data);
                Q_EMIT positionChanged();
            } else if (name == QLatin1StringView("duration") && property->format == MPV_FORMAT_DOUBLE) {
                m_duration = *static_cast<double *>(property->data);
                Q_EMIT durationChanged();
            } else if (name == QLatin1StringView("pause") && property->format == MPV_FORMAT_FLAG) {
                const bool playing = *static_cast<int *>(property->data) == 0;
                if (playing != m_playing) {
                    m_playing = playing;
                    Q_EMIT playingChanged();
                }
            } else if (name == QLatin1StringView("speed") && property->format == MPV_FORMAT_DOUBLE) {
                m_speed = *static_cast<double *>(property->data);
                Q_EMIT speedChanged();
            } else if (name == QLatin1StringView("mute") && property->format == MPV_FORMAT_FLAG) {
                m_muted = *static_cast<int *>(property->data) != 0;
                Q_EMIT mutedChanged();
            } else if (name == QLatin1StringView("eof-reached") && property->format == MPV_FORMAT_FLAG) {
                const bool reached = *static_cast<int *>(property->data) != 0;
                if (reached && !m_looping) {
                    Q_EMIT endOfFile();
                }
            }
            break;
        }
        case MPV_EVENT_END_FILE: {
            // With keep-open the clean-finish case arrives as eof-reached above;
            // this still covers a file that ends without it and, either way,
            // errors.
            auto *end = static_cast<mpv_event_end_file *>(event->data);
            if (end->reason == MPV_END_FILE_REASON_EOF && !m_looping) {
                Q_EMIT endOfFile();
            } else if (end->reason == MPV_END_FILE_REASON_ERROR) {
                Q_EMIT errorOccurred(QString::fromUtf8(mpv_error_string(end->error)));
            }
            break;
        }
        case MPV_EVENT_SHUTDOWN:
            return;
        default:
            break;
        }
    }
}
