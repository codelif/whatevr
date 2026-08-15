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

/// Frees an mpv_node's contents however the reader below leaves the function.
struct node_cleanup {
    mpv_node *node;

    ~node_cleanup()
    {
        mpv_free_node_contents(node);
    }
};

void setOption(mpv_handle *mpv, const char *name, const char *value)
{
    if (const int status = mpv_set_option_string(mpv, name, value); status < 0) {
        qWarning("whatkevr: mpv option %s=%s rejected: %s", name, value, mpv_error_string(status));
    }
}

}

MpvHandleOwner::MpvHandleOwner(mpv_handle *handle)
    : handle(handle)
{
}

MpvHandleOwner::~MpvHandleOwner()
{
    if (handle) {
        // terminate_destroy rather than destroy: it blocks until the core has
        // actually shut down, which is what makes the teardown order between
        // this and the render context observable rather than hopeful.
        mpv_terminate_destroy(handle);
        handle = nullptr;
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

MpvCore::MpvCore(Mode mode, QObject *parent)
    : QObject(parent)
    , m_mode(mode)
{
    ensureNumericLocale();
    if (mpv_handle *created = mpv_create()) {
        m_handle = std::make_shared<MpvHandleOwner>(created);
        m_mpv = created;
    }
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

    if (m_mode == Mode::Audio) {
        setOption(m_mpv, "vid", "no");
        setOption(m_mpv, "audio-display", "no");
    } else {
        // The scene graph owns the window; mpv draws into a framebuffer we hand
        // it, so it must not go looking for output of its own.
        setOption(m_mpv, "vo", "libmpv");
        setOption(m_mpv, "hwdec", "auto-safe");
        // Land scrubs where the user let go rather than on the nearest
        // keyframe, which on a WhatsApp video can be several seconds away.
        setOption(m_mpv, "hr-seek", "yes");
        setOption(m_mpv, "hr-seek-framedrop", "yes");
    }

    // Tests run without an audio device; everything else takes mpv's own pick.
    if (const QByteArray ao = qgetenv("WHATKEVR_MPV_AO"); !ao.isEmpty()) {
        setOption(m_mpv, "ao", ao.constData());
    }

    if (const int status = mpv_initialize(m_mpv); status < 0) {
        qWarning("whatkevr: could not initialize mpv: %s", mpv_error_string(status));
        m_handle.reset();
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
        m_mpv = nullptr;
    }
    // Dropping the reference is all this does. If a render context on the
    // render thread still holds one, the handle stays alive until that context
    // has been freed there, which is the only order libmpv accepts.
    m_handle.reset();
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
    // What the spinner is allowed to be about. Reading these rather than
    // guessing from position updates is the difference between "this engine is
    // working" and "this clip is broken".
    mpv_observe_property(m_mpv, 0, "seeking", MPV_FORMAT_FLAG);
    mpv_observe_property(m_mpv, 0, "paused-for-cache", MPV_FORMAT_FLAG);
    mpv_observe_property(m_mpv, 0, "core-idle", MPV_FORMAT_FLAG);
    if (m_mode == Mode::Video) {
        // dwidth/dheight are the displayed size, and they only exist once a
        // frame has been decoded: the first honest "there is a picture".
        mpv_observe_property(m_mpv, 0, "dwidth", MPV_FORMAT_INT64);
        mpv_observe_property(m_mpv, 0, "dheight", MPV_FORMAT_INT64);
    }
}

bool MpvCore::updateFlag(bool &target, bool value)
{
    if (target == value) {
        return false;
    }
    target = value;
    return true;
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
    m_videoSize = QSize();
    m_endReached = false;
    m_errorText.clear();
    Q_EMIT positionChanged();
    Q_EMIT durationChanged();
    Q_EMIT videoSizeChanged();
    Q_EMIT progressStateChanged();

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
    // exact, not the default keyframe snap: a scrub that lands two seconds from
    // where the thumb was left reads as a broken seekbar.
    command({QStringLiteral("seek"), QString::number(seconds), QStringLiteral("absolute+exact")});
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

void MpvCore::reopenVideoTrack()
{
    if (!m_mpv || m_mode != Mode::Video || m_source.isEmpty()) {
        return;
    }
    setProperty(QStringLiteral("vid"), QStringLiteral("no"));
    setProperty(QStringLiteral("vid"), QStringLiteral("auto"));
}

QImage MpvCore::grabStill() const
{
    if (!m_mpv || m_mode != Mode::Video || !m_videoSize.isValid() || m_videoSize.isEmpty()) {
        return {};
    }

    // "video" rather than "subtitles" or "window": the decoded frame with
    // nothing painted over it and no scaling to the item's size.
    mpv_node result;
    const char *args[] = {"screenshot-raw", "video", nullptr};
    if (mpv_command_ret(m_mpv, args, &result) < 0) {
        return {};
    }
    node_cleanup cleanup{&result};
    if (result.format != MPV_FORMAT_NODE_MAP) {
        return {};
    }

    qint64 width = 0;
    qint64 height = 0;
    qint64 stride = 0;
    QByteArray format;
    const mpv_byte_array *data = nullptr;
    const mpv_node_list *map = result.u.list;
    for (int i = 0; i < map->num; ++i) {
        const QLatin1StringView key(map->keys[i]);
        const mpv_node &value = map->values[i];
        if (key == QLatin1StringView("w") && value.format == MPV_FORMAT_INT64) {
            width = value.u.int64;
        } else if (key == QLatin1StringView("h") && value.format == MPV_FORMAT_INT64) {
            height = value.u.int64;
        } else if (key == QLatin1StringView("stride") && value.format == MPV_FORMAT_INT64) {
            stride = value.u.int64;
        } else if (key == QLatin1StringView("format") && value.format == MPV_FORMAT_STRING) {
            format = QByteArray(value.u.string);
        } else if (key == QLatin1StringView("data") && value.format == MPV_FORMAT_BYTE_ARRAY) {
            data = value.u.ba;
        }
    }

    if (width <= 0 || height <= 0 || stride <= 0 || !data || !data->data) {
        return {};
    }
    // mpv documents bgr0 as the only format screenshot-raw currently produces,
    // and refusing anything else is better than reading a buffer as something
    // it is not.
    if (format != QByteArrayLiteral("bgr0")) {
        qWarning() << "whatkevr: unexpected screenshot format" << format;
        return {};
    }
    if (qint64(data->size) < stride * height) {
        return {};
    }

    const QImage view(static_cast<const uchar *>(data->data),
                      int(width),
                      int(height),
                      int(stride),
                      QImage::Format_RGB32);
    // copy(): the node, and the buffer behind it, is freed on the way out.
    return view.copy();
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
                if (updateFlag(m_endReached, reached)) {
                    Q_EMIT progressStateChanged();
                }
                if (reached && !m_looping) {
                    Q_EMIT endOfFile();
                }
            } else if (name == QLatin1StringView("seeking") && property->format == MPV_FORMAT_FLAG) {
                if (updateFlag(m_seeking, *static_cast<int *>(property->data) != 0)) {
                    Q_EMIT progressStateChanged();
                }
            } else if (name == QLatin1StringView("paused-for-cache") && property->format == MPV_FORMAT_FLAG) {
                if (updateFlag(m_waitingForCache, *static_cast<int *>(property->data) != 0)) {
                    Q_EMIT progressStateChanged();
                }
            } else if (name == QLatin1StringView("core-idle") && property->format == MPV_FORMAT_FLAG) {
                if (updateFlag(m_coreIdle, *static_cast<int *>(property->data) != 0)) {
                    Q_EMIT progressStateChanged();
                }
            } else if (name == QLatin1StringView("dwidth") || name == QLatin1StringView("dheight")) {
                // A track that goes away reports the property as unavailable
                // rather than as zero, and a size left standing from the last
                // file would claim there is a picture when there is none.
                const int value = property->format == MPV_FORMAT_INT64
                    ? int(*static_cast<int64_t *>(property->data))
                    : 0;
                if (name == QLatin1StringView("dwidth")) {
                    m_videoSize.setWidth(value);
                } else {
                    m_videoSize.setHeight(value);
                }
                Q_EMIT videoSizeChanged();
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
                m_errorText = QString::fromUtf8(mpv_error_string(end->error));
                Q_EMIT errorOccurred(m_errorText);
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
