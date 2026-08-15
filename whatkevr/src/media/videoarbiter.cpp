// SPDX-License-Identifier: BSD-3-Clause
#include "videoarbiter.h"

#include <QQmlEngine>

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

void VideoPlaybackArbiter::captureFrame(const QString &messageId, const QVideoFrame &frame)
{
    if (messageId.isEmpty() || !frame.isValid()) {
        return;
    }
    QImage image = frame.toImage();
    if (image.isNull()) {
        return;
    }
    // A decoder that has opened a file but not produced a picture yet hands out
    // a frame of the right size filled with nothing. Storing that would replace
    // a good still with a black rectangle, which is the failure this whole
    // mechanism exists to avoid.
    if (image.width() <= 0 || image.height() <= 0) {
        return;
    }
    if (image.width() > frameMaxEdge || image.height() > frameMaxEdge) {
        image = image.scaled(frameMaxEdge, frameMaxEdge, Qt::KeepAspectRatio, Qt::SmoothTransformation);
    }
    // detach(): toImage() may hand back a view onto the frame's own buffer,
    // which the decoder reuses for the next frame.
    image.detach();

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
