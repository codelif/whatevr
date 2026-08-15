// SPDX-License-Identifier: BSD-3-Clause
#include "videoarbiter.h"

#include <algorithm>

#include <QQmlEngine>
#include <QTimer>

#include "app/settings.h"

VideoPlaybackArbiter *VideoPlaybackArbiter::instance()
{
    static VideoPlaybackArbiter arbiter(nullptr);
    return &arbiter;
}

VideoPlaybackArbiter *VideoPlaybackArbiter::create(QQmlEngine *, QJSEngine *)
{
    // The engine would otherwise take ownership of a statically-stored object.
    QQmlEngine::setObjectOwnership(instance(), QQmlEngine::CppOwnership);
    return instance();
}

VideoPlaybackArbiter::VideoPlaybackArbiter(QObject *parent)
    : QObject(parent)
{
}

int VideoPlaybackArbiter::animatedLimit() const
{
    if (m_animatedLimitOverride >= 0) {
        return m_animatedLimitOverride;
    }
    const Settings *settings = Settings::instance();
    return settings ? settings->gifPlayerLimit() : 3;
}

void VideoPlaybackArbiter::setAnimatedLimit(int limit)
{
    m_animatedLimitOverride = limit;
}

bool VideoPlaybackArbiter::request(QObject *claimant, Lane lane)
{
    if (!claimant) {
        return false;
    }
    return admit(claimant, lane);
}

PlaybackSession *VideoPlaybackArbiter::acquire(QObject *claimant,
                                               const QString &messageId,
                                               const QUrl &source,
                                               double startAt,
                                               Lane lane)
{
    if (!claimant) {
        return nullptr;
    }

    // A claimant switching messages first lets go of the session it held;
    // pausing into the grace period rather than parking outright, in case the
    // old message is re-acquired right away by someone else.
    if (PlaybackSession *held = m_bound.value(claimant)) {
        if (held->messageId() == messageId && !messageId.isEmpty()) {
            if (!admit(claimant, lane)) {
                return nullptr;
            }
            return held;
        }
        m_bound.remove(claimant);
        scheduleParking(held);
    }

    PlaybackSession *live = messageId.isEmpty() ? nullptr : liveSessionFor(messageId);
    QObject *previousHolder = nullptr;
    if (live) {
        for (auto it = m_bound.constBegin(); it != m_bound.constEnd(); ++it) {
            if (it.value() == live) {
                previousHolder = const_cast<QObject *>(it.key());
                break;
            }
        }
    }

    // Rebind before any revocation lands: the previous holder reacts to
    // revoked() by releasing, and a release that still found it bound would
    // pause the session mid-handoff. This ordering is the gapless part.
    QObject *const preAdmitExclusive = m_exclusive.data();
    if (live && previousHolder && previousHolder != claimant) {
        m_bound.remove(previousHolder);
    }
    if (live) {
        bindSession(claimant, live);
    }

    if (!admit(claimant, lane)) {
        // Animated lane full; the claimant is queued. Hand the session back
        // to whoever had it rather than leaving it bound to a claimant that
        // was refused.
        if (live) {
            m_bound.remove(claimant);
            if (previousHolder && previousHolder != claimant) {
                m_bound.insert(previousHolder, live);
            }
        }
        return nullptr;
    }

    if (live) {
        // admit() already revoked the previous exclusive holder; only a
        // holder it did not know about (the same clip running in the other
        // lane) still needs to be told to let go of its view.
        if (previousHolder && previousHolder != claimant && previousHolder != preAdmitExclusive) {
            Q_EMIT revoked(previousHolder);
        }
        return live;
    }

    PlaybackSession *session = obtainParkedSession();
    const double at = startAt > 0.0 ? startAt : resumePosition(messageId);
    session->configure(messageId, source, at);
    bindSession(claimant, session);
    return session;
}

void VideoPlaybackArbiter::updateSource(QObject *claimant, const QUrl &source)
{
    if (PlaybackSession *held = m_bound.value(claimant)) {
        held->promoteSource(source);
    }
}

PlaybackSession *VideoPlaybackArbiter::sessionFor(const QObject *claimant) const
{
    return m_bound.value(claimant);
}

PlaybackSession *VideoPlaybackArbiter::liveSessionFor(const QString &messageId) const
{
    if (messageId.isEmpty()) {
        return nullptr;
    }
    for (PlaybackSession *session : m_sessions) {
        // park() clears the message id, so a non-empty match is live: either
        // bound to a view or paused in its post-release grace.
        if (session->messageId() == messageId) {
            return session;
        }
    }
    return nullptr;
}

void VideoPlaybackArbiter::pauseAudibleSessions()
{
    for (PlaybackSession *session : m_sessions) {
        if (session->audible()) {
            session->setPlaying(false);
        }
    }
}

void VideoPlaybackArbiter::promoteStreamedSource(const QString &messageId, const QString &localPath)
{
    if (localPath.isEmpty()) {
        return;
    }
    if (PlaybackSession *session = liveSessionFor(messageId)) {
        session->promoteSource(QUrl::fromLocalFile(localPath));
    }
}

void VideoPlaybackArbiter::bindSession(QObject *claimant, PlaybackSession *session)
{
    m_bound.insert(claimant, session);
    // A claimant destroyed without releasing (a delegate the list recycled
    // mid-teardown) must not leave its session bound forever.
    connect(claimant, &QObject::destroyed, this, &VideoPlaybackArbiter::release, Qt::UniqueConnection);
}

PlaybackSession *VideoPlaybackArbiter::obtainParkedSession()
{
    const auto isBound = [this](PlaybackSession *session) {
        return std::find(m_bound.constBegin(), m_bound.constEnd(), session) != m_bound.constEnd();
    };
    // A parked session first; failing that, one resting in its grace period,
    // which loses its chance of a seamless resume to a clip that actually
    // wants to play right now.
    for (PlaybackSession *session : m_sessions) {
        if (!isBound(session) && session->messageId().isEmpty()) {
            return session;
        }
    }
    for (PlaybackSession *session : m_sessions) {
        if (!isBound(session)) {
            session->park();
            return session;
        }
    }
    auto *session = new PlaybackSession(this);
    QQmlEngine::setObjectOwnership(session, QQmlEngine::CppOwnership);
    connect(session, &PlaybackSession::audiblePlaybackStarted, this, &VideoPlaybackArbiter::audiblePlaybackStarted);
    connect(session, &PlaybackSession::stillGrabbed, this, &VideoPlaybackArbiter::captureImage);
    m_sessions.append(session);
    return session;
}

void VideoPlaybackArbiter::scheduleParking(PlaybackSession *session)
{
    if (!session) {
        return;
    }
    session->setPlaying(false);
    QPointer<PlaybackSession> guarded(session);
    QTimer::singleShot(parkGraceMs, this, [this, guarded]() {
        if (!guarded) {
            return;
        }
        const bool bound = std::find(m_bound.constBegin(), m_bound.constEnd(), guarded.data()) != m_bound.constEnd();
        if (!bound) {
            guarded->park();
        }
    });
}

bool VideoPlaybackArbiter::admit(QObject *claimant, Lane lane)
{
    if (lane == Exclusive) {
        // A claimant already in the animated lane is changing its mind about
        // what it is, which happens when a bubble is reused for another
        // message. Take it out of there first, or it holds a slot forever.
        m_animated.removeAll(QPointer<QObject>(claimant));
        m_animatedWaiting.removeAll(QPointer<QObject>(claimant));

        if (m_exclusive == claimant) {
            return true;
        }
        QObject *previous = m_exclusive.data();
        m_exclusive = claimant;
        if (previous) {
            // After the handover, not before: a surface that stops on this
            // signal may release synchronously, and a release arriving while
            // m_exclusive is still the old holder would clear the new one.
            Q_EMIT revoked(previous);
        }
        // Whatever the old holder was doing on the animated side is now moot,
        // but a slot it frees should still reach whoever is waiting.
        offerAnimatedSlot();
        return true;
    }

    if (m_exclusive == claimant) {
        m_exclusive.clear();
    }
    if (m_animated.contains(QPointer<QObject>(claimant))) {
        return true;
    }

    m_animated.removeAll(QPointer<QObject>(nullptr));
    if (m_animated.size() >= animatedLimit()) {
        if (!m_animatedWaiting.contains(QPointer<QObject>(claimant))) {
            m_animatedWaiting.append(claimant);
        }
        return false;
    }
    m_animatedWaiting.removeAll(QPointer<QObject>(claimant));
    m_animated.append(claimant);
    return true;
}

void VideoPlaybackArbiter::release(QObject *claimant)
{
    if (!claimant) {
        return;
    }
    if (m_exclusive == claimant) {
        m_exclusive.clear();
    }
    const bool heldAnimated = m_animated.removeAll(QPointer<QObject>(claimant)) > 0;
    // Also drops a place in the queue: a bubble scrolled out of view stops
    // wanting to play, and leaving it queued would hand a slot to something
    // that is not on screen any more.
    m_animatedWaiting.removeAll(QPointer<QObject>(claimant));
    // A session transferred to another claimant is no longer in m_bound under
    // this key, so a revoked holder releasing cannot pause the handoff.
    if (PlaybackSession *held = m_bound.take(claimant)) {
        scheduleParking(held);
    }
    if (heldAnimated) {
        offerAnimatedSlot();
    }
}

void VideoPlaybackArbiter::offerAnimatedSlot()
{
    m_animated.removeAll(QPointer<QObject>(nullptr));
    while (m_animated.size() < animatedLimit() && !m_animatedWaiting.isEmpty()) {
        QPointer<QObject> next = m_animatedWaiting.takeFirst();
        if (!next) {
            // Destroyed while it waited, which for a scrolling list is the
            // common case.
            continue;
        }
        // It re-checks whether it still wants to play and asks again; nothing
        // is granted here, so a stale offer costs nothing.
        Q_EMIT grantOffered(next.data());
        return;
    }
}

bool VideoPlaybackArbiter::holds(const QObject *claimant) const
{
    if (!claimant) {
        return false;
    }
    if (m_exclusive == claimant) {
        return true;
    }
    return m_animated.contains(QPointer<QObject>(const_cast<QObject *>(claimant)));
}

int VideoPlaybackArbiter::animatedHolderCount() const
{
    int count = 0;
    for (const QPointer<QObject> &holder : m_animated) {
        if (holder) {
            ++count;
        }
    }
    return count;
}

double VideoPlaybackArbiter::resumePosition(const QString &messageId) const
{
    return m_positions.value(messageId, 0.0);
}

void VideoPlaybackArbiter::setResumePosition(const QString &messageId, double seconds)
{
    if (messageId.isEmpty()) {
        return;
    }
    if (seconds <= 0.0) {
        clearResumePosition(messageId);
        return;
    }
    m_positions.insert(messageId, seconds);
    rememberOrder(messageId);
    Q_EMIT resumePositionChanged(messageId);
}

void VideoPlaybackArbiter::clearResumePosition(const QString &messageId)
{
    if (m_positions.remove(messageId) == 0) {
        return;
    }
    m_positionOrder.removeAll(messageId);
    Q_EMIT resumePositionChanged(messageId);
}

void VideoPlaybackArbiter::handoffToInline(const QString &messageId, double seconds, bool resumePlayback)
{
    if (messageId.isEmpty()) {
        return;
    }
    if (seconds > 0.0) {
        setResumePosition(messageId, seconds);
    }
    Q_EMIT inlineHandoff(messageId, seconds, resumePlayback);
}

void VideoPlaybackArbiter::rememberOrder(const QString &messageId)
{
    m_positionOrder.removeAll(messageId);
    m_positionOrder.append(messageId);
    while (m_positionOrder.size() > resumeHistoryLimit) {
        m_positions.remove(m_positionOrder.takeFirst());
    }
}

void VideoPlaybackArbiter::captureImage(const QString &messageId, const QImage &source)
{
    if (messageId.isEmpty() || source.isNull()) {
        return;
    }
    QImage image = source;
    // A decoder that has opened a file but not produced a picture yet answers
    // with an empty frame. Storing that would replace a good still with a black
    // rectangle, which is the failure this whole mechanism exists to avoid.
    if (image.width() <= 0 || image.height() <= 0) {
        return;
    }
    if (image.width() > frameMaxEdge || image.height() > frameMaxEdge) {
        image = image.scaled(frameMaxEdge, frameMaxEdge, Qt::KeepAspectRatio, Qt::SmoothTransformation);
    }
    {
        QMutexLocker locker(&m_frameMutex);
        m_frames.insert(messageId, image);
        m_frameRevisions[messageId] = m_frameRevisions.value(messageId, 0) + 1;
        rememberFrameOrder(messageId);
    }
    Q_EMIT frameCaptured(messageId);
}

bool VideoPlaybackArbiter::hasFrame(const QString &messageId) const
{
    QMutexLocker locker(&m_frameMutex);
    return m_frames.contains(messageId);
}

int VideoPlaybackArbiter::frameRevision(const QString &messageId) const
{
    QMutexLocker locker(&m_frameMutex);
    return m_frameRevisions.value(messageId, 0);
}

QImage VideoPlaybackArbiter::frameImage(const QString &messageId) const
{
    QMutexLocker locker(&m_frameMutex);
    return m_frames.value(messageId);
}

// Called with m_frameMutex held.
void VideoPlaybackArbiter::rememberFrameOrder(const QString &messageId)
{
    m_frameOrder.removeAll(messageId);
    m_frameOrder.append(messageId);
    while (m_frameOrder.size() > frameHistoryLimit) {
        const QString evicted = m_frameOrder.takeFirst();
        m_frames.remove(evicted);
        // The revision is kept: an evicted still may be captured again, and a
        // url that went back to a revision it has already shown would come out
        // of the pipeline's cache rather than being re-read.
    }
}
