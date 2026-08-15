// SPDX-License-Identifier: BSD-3-Clause
#include "audioplayer.h"

#include "app/settings.h"
#include "mpvcore.h"

#include <KLocalizedString>

namespace
{
// The speeds WhatsApp offers, in the order the pill cycles them.
constexpr double playbackSpeeds[] = {1.0, 1.5, 2.0};

// A note is only "resumable" if the user stopped somewhere in the middle;
// stopping in the last second is finishing it.
constexpr double resumeTailSeconds = 1.0;
}

AudioPlayer *AudioPlayer::instance()
{
    static AudioPlayer player(nullptr);
    return &player;
}

AudioPlayer *AudioPlayer::create(QQmlEngine *, QJSEngine *)
{
    // The engine would otherwise take ownership of a statically-stored object.
    QQmlEngine::setObjectOwnership(instance(), QQmlEngine::CppOwnership);
    return instance();
}

AudioPlayer::AudioPlayer(QObject *parent)
    : QObject(parent)
    , m_core(new MpvCore(MpvCore::Mode::Audio, this))
{
    connect(m_core, &MpvCore::playingChanged, this, [this] {
        if (m_core->playing() && !m_startedReported && !m_messageId.isEmpty()) {
            m_startedReported = true;
            Q_EMIT started(m_messageId);
        }
        Q_EMIT playingChanged();
    });
    connect(m_core, &MpvCore::positionChanged, this, &AudioPlayer::positionChanged);
    connect(m_core, &MpvCore::durationChanged, this, &AudioPlayer::durationChanged);
    connect(m_core, &MpvCore::speedChanged, this, &AudioPlayer::speedChanged);
    connect(m_core, &MpvCore::errorOccurred, this, [this](const QString &message) {
        setError(message);
    });
    connect(m_core, &MpvCore::endOfFile, this, [this] {
        const QString finishedId = m_messageId;
        if (finishedId.isEmpty()) {
            return;
        }
        // A note played to the end has no resume point.
        m_resumePositions.remove(finishedId);
        Q_EMIT finished(finishedId);
    });
}

AudioPlayer::~AudioPlayer() = default;

bool AudioPlayer::available() const
{
    return m_core && m_core->isValid();
}

void AudioPlayer::setError(const QString &error)
{
    if (m_error == error) {
        return;
    }
    m_error = error;
    Q_EMIT errorChanged();
}

bool AudioPlayer::playing() const
{
    return m_core && m_core->playing();
}

double AudioPlayer::position() const
{
    return m_core ? m_core->position() : 0.0;
}

double AudioPlayer::duration() const
{
    // mpv only knows the duration once it has opened the file; until then the
    // message row's own duration keeps the scrub bar honest.
    const double known = m_core ? m_core->duration() : 0.0;
    return known > 0.0 ? known : m_durationHint;
}

double AudioPlayer::speed() const
{
    return m_core ? m_core->speed() : 1.0;
}

void AudioPlayer::setSpeed(double speed)
{
    if (!m_core || speed <= 0.0) {
        return;
    }
    m_core->setSpeed(speed);
}

void AudioPlayer::play(const QString &messageId, const QUrl &source, double durationHint)
{
    if (!m_core || messageId.isEmpty()) {
        return;
    }
    if (!available()) {
        // A dead audio engine used to make every play button a no-op with
        // nothing on screen to explain it.
        setError(i18nc("@info", "Audio playback is unavailable: mpv could not be initialized."));
        return;
    }
    setError(QString());

    if (messageId == m_messageId) {
        m_core->play();
        return;
    }

    // Switching away from a note mid-listen leaves a resume point behind.
    rememberPosition();

    m_messageId = messageId;
    m_durationHint = durationHint;
    m_startedReported = false;
    Q_EMIT messageIdChanged();

    m_core->load(source, true);
    // Settings is constructed by main(); a test harness may have none.
    if (const Settings *settings = Settings::instance()) {
        m_core->setSpeed(settings->defaultPlaybackSpeed());
    }
    if (const double resume = m_resumePositions.value(messageId, 0.0); resume > 0.0) {
        m_core->seek(resume);
    }
    Q_EMIT durationChanged();
}

void AudioPlayer::toggle(const QString &messageId, const QUrl &source, double durationHint)
{
    if (messageId == m_messageId && playing()) {
        pause();
        return;
    }
    play(messageId, source, durationHint);
}

void AudioPlayer::pause()
{
    if (!m_core) {
        return;
    }
    m_core->pause();
    rememberPosition();
}

void AudioPlayer::stop()
{
    if (!m_core) {
        return;
    }
    rememberPosition();
    m_core->stop();
    m_messageId.clear();
    m_durationHint = 0.0;
    m_startedReported = false;
    Q_EMIT messageIdChanged();
    Q_EMIT playingChanged();
    Q_EMIT positionChanged();
}

void AudioPlayer::seek(double seconds)
{
    if (!m_core) {
        return;
    }
    m_core->seek(seconds);
}

double AudioPlayer::cycleSpeed()
{
    const double current = speed();
    for (size_t i = 0; i < std::size(playbackSpeeds); ++i) {
        if (qFuzzyCompare(current, playbackSpeeds[i])) {
            const double next = playbackSpeeds[(i + 1) % std::size(playbackSpeeds)];
            setSpeed(next);
            return next;
        }
    }
    setSpeed(playbackSpeeds[0]);
    return playbackSpeeds[0];
}

double AudioPlayer::resumePosition(const QString &messageId) const
{
    return m_resumePositions.value(messageId, 0.0);
}

void AudioPlayer::rememberPosition()
{
    if (m_messageId.isEmpty() || !m_core) {
        return;
    }
    const Settings *settings = Settings::instance();
    if (settings && !settings->rememberPlaybackPosition()) {
        m_resumePositions.remove(m_messageId);
        Q_EMIT resumePositionChanged(m_messageId);
        return;
    }
    const double at = m_core->position();
    const double total = duration();
    if (at > 0.0 && (total <= 0.0 || at < total - resumeTailSeconds)) {
        m_resumePositions.insert(m_messageId, at);
    } else {
        m_resumePositions.remove(m_messageId);
    }
    Q_EMIT resumePositionChanged(m_messageId);
}
