// SPDX-License-Identifier: BSD-3-Clause
#include "playbacksession.h"

#include <algorithm>
#include <cmath>

#include "mpvcore.h"
#include "mpvvideoitem.h"

PlaybackSession::PlaybackSession(QObject *parent)
    : QObject(parent)
    , m_core(new MpvCore(MpvCore::Mode::Video, this))
{
    // A seek that neither lands nor fails within this window is abandoned so
    // busy clears and the failure backstops above re-arm; a held target used to
    // pin the spinner forever and disable exactly the timers meant to catch a
    // wedged seek.
    m_seekSettleTimer.setSingleShot(true);
    m_seekSettleTimer.setInterval(8000);
    connect(&m_seekSettleTimer, &QTimer::timeout, this, [this]() {
        if (m_seekTarget >= 0.0) {
            m_seekTarget = -1.0;
            Q_EMIT seekingChanged();
            Q_EMIT busyChanged();
        }
    });

    connect(m_core, &MpvCore::positionChanged, this, [this]() {
        handlePosition();
        Q_EMIT positionChanged();
    });
    connect(m_core, &MpvCore::durationChanged, this, &PlaybackSession::durationChanged);
    connect(m_core, &MpvCore::videoSizeChanged, this, &PlaybackSession::hasVideoChanged);
    connect(m_core, &MpvCore::progressStateChanged, this, &PlaybackSession::handleProgressState);
    connect(m_core, &MpvCore::endOfFile, this, [this]() {
        if (!m_loop) {
            Q_EMIT endOfMedia();
        }
    });
    connect(m_core, &MpvCore::errorOccurred, this, [this](const QString &message) {
        m_failed = true;
        m_errorText = message;
        Q_EMIT failedChanged();
    });
    // mpv pausing itself is not something this app does, but the flag is the
    // authority on what is running; keeping the session's own idea of it in
    // step is what stops the transport claiming a clip is playing when it is
    // not.
    connect(m_core, &MpvCore::playingChanged, this, [this]() {
        const bool playing = m_core->playing();
        if (playing == m_playing || m_source.isEmpty()) {
            return;
        }
        m_playing = playing;
        Q_EMIT playingChanged();
        maybeAnnounceAudible();
    });
}

QUrl PlaybackSession::source() const
{
    return m_source;
}

double PlaybackSession::position() const
{
    // Before the file is open, mpv has no position but the session does: the
    // second it was told to open at. Reporting zero there is what made a
    // handoff briefly rewind the transport to the beginning.
    if (!m_loaded && m_startAt > 0.0) {
        return m_startAt;
    }
    return m_core->position();
}

double PlaybackSession::duration() const
{
    return m_core->duration();
}

bool PlaybackSession::hasVideo() const
{
    const QSize size = m_core->videoSize();
    return size.width() > 0 && size.height() > 0;
}

bool PlaybackSession::atEnd() const
{
    return m_core->endReached();
}

bool PlaybackSession::busy() const
{
    if (m_source.isEmpty()) {
        return false;
    }
    if (seeking() || m_core->seeking() || m_core->waitingForCache()) {
        return true;
    }
    if (!m_loaded) {
        return true;
    }
    // Idle while it is supposed to be playing and has not finished: opening a
    // file, or waiting on bytes the daemon has not fetched yet.
    return m_core->coreIdle() && m_playing && !m_core->endReached();
}

void PlaybackSession::setPlaying(bool playing)
{
    if (m_playing == playing) {
        return;
    }
    m_playing = playing;
    if (m_loaded) {
        if (playing && m_core->endReached()) {
            // Playing from the last frame means playing again, not resuming an
            // exhausted file; mpv would otherwise sit where it is.
            m_core->seek(0.0);
        }
        if (playing) {
            m_core->play();
        } else {
            m_core->pause();
        }
    } else if (playing) {
        // Nothing is open yet, so this only decides how the file will start.
        // Stopping is deliberately not a reason to open one: a surface letting
        // go pauses the session it never got to use.
        loadIfReady();
    }
    Q_EMIT playingChanged();
    maybeAnnounceAudible();
}

void PlaybackSession::setMuted(bool muted)
{
    if (m_muted == muted) {
        return;
    }
    m_muted = muted;
    m_core->setMuted(muted);
    Q_EMIT mutedChanged();
    maybeAnnounceAudible();
}

void PlaybackSession::setVolume(double volume)
{
    const double clamped = std::clamp(volume, 0.0, 100.0);
    if (qFuzzyCompare(m_volume, clamped)) {
        return;
    }
    m_volume = clamped;
    m_core->setVolume(clamped);
    Q_EMIT volumeChanged();
    maybeAnnounceAudible();
}

void PlaybackSession::setRate(double rate)
{
    if (rate <= 0.0 || qFuzzyCompare(m_rate, rate)) {
        return;
    }
    m_rate = rate;
    m_core->setSpeed(rate);
    Q_EMIT rateChanged();
}

void PlaybackSession::setLoop(bool loop)
{
    if (m_loop == loop) {
        return;
    }
    m_loop = loop;
    m_core->setLooping(loop);
    Q_EMIT loopChanged();
}

void PlaybackSession::configure(const QString &messageId, const QUrl &source, double startAt)
{
    resetSeekState();
    m_failed = false;
    m_errorText.clear();
    Q_EMIT failedChanged();

    if (m_messageId != messageId) {
        m_messageId = messageId;
        Q_EMIT messageIdChanged();
    }
    m_source = source;
    m_startAt = std::max(0.0, startAt);
    m_loaded = false;
    Q_EMIT sourceChanged();
    Q_EMIT activeChanged();
    Q_EMIT positionChanged();
    Q_EMIT busyChanged();

    applyStateToCore();
    loadIfReady();
}

void PlaybackSession::promoteSource(const QUrl &source)
{
    if (m_source == source || source.isEmpty()) {
        return;
    }
    // Keep whichever destination is more current: a seek the user has in flight
    // beats the position the old source had reached.
    const double resumeAt = m_seekTarget >= 0.0 ? m_seekTarget : position();
    resetSeekState();
    m_source = source;
    m_startAt = std::max(0.0, resumeAt);
    m_loaded = false;
    Q_EMIT sourceChanged();
    loadIfReady();
}

void PlaybackSession::park()
{
    resetSeekState();
    m_playing = false;
    m_loaded = false;
    m_startAt = 0.0;
    m_core->stop();
    m_core->setLooping(false);
    m_core->setMuted(false);
    m_core->setSpeed(1.0);
    m_core->setVolume(100.0);
    m_loop = false;
    m_muted = false;
    m_rate = 1.0;
    m_volume = 100.0;
    m_failed = false;
    m_errorText.clear();
    m_wasAtEnd = false;
    m_wasAudible = false;
    m_source.clear();
    if (!m_messageId.isEmpty()) {
        m_messageId.clear();
        Q_EMIT messageIdChanged();
    }
    Q_EMIT sourceChanged();
    Q_EMIT playingChanged();
    Q_EMIT failedChanged();
    Q_EMIT activeChanged();
}

void PlaybackSession::attachView(QQuickItem *container)
{
    if (!container || !m_core->isValid()) {
        return;
    }
    if (m_container == container && m_item) {
        return;
    }

    if (m_container) {
        disconnect(m_container, nullptr, this, nullptr);
    }
    m_container = container;
    m_window = container->window();
    connect(container, &QQuickItem::widthChanged, this, &PlaybackSession::syncItemGeometry);
    connect(container, &QQuickItem::heightChanged, this, &PlaybackSession::syncItemGeometry);

    if (!m_item) {
        m_item = new MpvVideoItem(m_core, nullptr);
        // Owned by the session, never by whichever view happens to be showing
        // it: the visual parent below changes on every handoff, and a QML
        // container going away must not take the item with it.
        m_item->setParent(this);
        connect(m_item, &MpvVideoItem::rendererReadyChanged, this, [this]() {
            if (!m_item->rendererReady()) {
                return;
            }
            if (!m_loaded) {
                loadIfReady();
                return;
            }
            // A file is already open, which means this is a context that was
            // freed and remade (the item left the scene and came back). The
            // video track went with the old context; this is what brings the
            // picture back without touching audio or position.
            m_core->reopenVideoTrack();
        });
    }
    m_item->setParentItem(container);
    m_item->setVisible(true);
    syncItemGeometry();
    loadIfReady();
}

void PlaybackSession::detachView(QQuickItem *container)
{
    if (!container || m_container != container) {
        return;
    }
    disconnect(container, nullptr, this, nullptr);
    m_container.clear();
    // Leaving the scene, not just this container: the scene graph destroys the
    // renderer, which frees mpv's render context, which switches the video
    // track of the playing file off. In a handoff the next view attaches later
    // in the same turn, so give it that turn before moving the item out.
    QMetaObject::invokeMethod(
        this,
        [this]() {
            if (m_container || !m_item) {
                return;
            }
            // Parked out of sight but still in the scene, so the render context
            // and everything mpv built on it survive. A conversation scrolled
            // through rebuilt that context per bubble otherwise, and building
            // one is GPU work on the render thread: exactly the frame the
            // scroll cannot spare. Invisible items are not drawn, which is all
            // a paused session needs.
            m_item->setVisible(false);
            if (QQuickItem *holder = parkingHolder()) {
                m_item->setParentItem(holder);
            } else {
                m_item->setParentItem(nullptr);
            }
        },
        Qt::QueuedConnection);
}

QQuickItem *PlaybackSession::parkingHolder() const
{
    return m_window ? m_window->contentItem() : nullptr;
}

void PlaybackSession::syncItemGeometry()
{
    if (!m_item || !m_container) {
        return;
    }
    m_item->setSize(QSizeF(m_container->width(), m_container->height()));
}

void PlaybackSession::applyStateToCore()
{
    m_core->setMuted(m_muted);
    m_core->setLooping(m_loop);
    m_core->setSpeed(m_rate);
    m_core->setVolume(m_volume);
}

void PlaybackSession::loadIfReady()
{
    if (m_loaded || m_source.isEmpty() || !m_core->isValid()) {
        return;
    }
    // The render context first, always. mpv decides whether a file has video
    // when it opens it, and a file opened without one plays through to the end
    // with its video track switched off.
    if (!m_item || !m_item->rendererReady()) {
        return;
    }
    m_loaded = true;
    m_core->load(m_source, m_playing, m_startAt);
    Q_EMIT busyChanged();
}

void PlaybackSession::seek(double seconds)
{
    double target = std::max(0.0, seconds);
    // Sender-declared durations are frequently rounded or simply wrong; once
    // the decoder knows the real length, never aim past it.
    if (const double total = duration(); total > 0.0) {
        target = std::min(target, std::max(0.0, total - 0.1));
    }
    m_seekTarget = target;
    m_seekSettleTimer.start();
    Q_EMIT seekingChanged();
    Q_EMIT busyChanged();

    if (!m_loaded) {
        // Nothing is open yet: the target becomes where the file will open,
        // which is one operation instead of an open followed by a seek.
        m_startAt = target;
        return;
    }
    m_core->seek(target);
}

void PlaybackSession::replayFromStart()
{
    resetSeekState();
    m_startAt = 0.0;
    if (m_loaded) {
        m_core->seek(0.0);
        m_core->play();
    }
    if (!m_playing) {
        m_playing = true;
        Q_EMIT playingChanged();
    }
    loadIfReady();
    maybeAnnounceAudible();
}

void PlaybackSession::captureStill()
{
    if (m_messageId.isEmpty()) {
        return;
    }
    const QImage image = m_core->grabStill();
    if (image.isNull()) {
        return;
    }
    Q_EMIT stillGrabbed(m_messageId, image);
}

void PlaybackSession::handlePosition()
{
    if (m_seekTarget < 0.0) {
        return;
    }
    // mpv seeks exactly, so landing near the target is the end of it; a clip
    // that has moved on from there under its own steam is equally done.
    const double at = m_core->position();
    if (std::abs(at - m_seekTarget) < seekSettleEpsilon || (m_playing && at > m_seekTarget)) {
        m_seekTarget = -1.0;
        m_seekSettleTimer.stop();
        Q_EMIT seekingChanged();
        Q_EMIT busyChanged();
    }
}

void PlaybackSession::handleProgressState()
{
    const bool nowAtEnd = atEnd();
    if (nowAtEnd != m_wasAtEnd) {
        m_wasAtEnd = nowAtEnd;
        Q_EMIT atEndChanged();
    }
    const bool nowBusy = busy();
    if (nowBusy != m_wasBusy) {
        m_wasBusy = nowBusy;
        Q_EMIT busyChanged();
    }
}

void PlaybackSession::resetSeekState()
{
    m_seekSettleTimer.stop();
    const bool wasSeeking = seeking();
    m_seekTarget = -1.0;
    if (wasSeeking) {
        Q_EMIT seekingChanged();
        Q_EMIT busyChanged();
    }
}

void PlaybackSession::maybeAnnounceAudible()
{
    const bool nowAudible = audible();
    if (nowAudible && !m_wasAudible) {
        Q_EMIT audiblePlaybackStarted();
    }
    m_wasAudible = nowAudible;
}
