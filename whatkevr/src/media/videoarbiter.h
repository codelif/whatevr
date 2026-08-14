// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QHash>
#include <QList>
#include <QObject>
#include <QPointer>
#include <QQmlEngine>
#include <QString>

/**
 * VideoPlaybackArbiter decides what is allowed to be playing, app-wide.
 *
 * This sits above QtVideoSurface so decoder limits and exclusive playback are
 * decided before a MediaPlayer is instantiated.
 *
 * Two lanes, because two kinds of clip want opposite things:
 *
 *  - Exclusive, for videos and video notes. They carry sound and the user
 *    started them on purpose, so exactly one plays anywhere, ever, and a new
 *    request always wins: the previous holder is revoked rather than refused.
 *  - Animated, for GIFs. They are silent decorative loops, so several may run,
 *    bounded by the user's limit, and a request that does not fit waits its
 *    turn instead of taking someone else's.
 *
 * It also remembers where each message was left, which is what makes bubble to
 * viewer and back continuous. That is view state the daemon has no concept of,
 * not a cache of anything it owns.
 */
class VideoPlaybackArbiter : public QObject
{
    Q_OBJECT
    QML_NAMED_ELEMENT(VideoPlayback)
    QML_SINGLETON

public:
    enum Lane {
        /// One at a time, app-wide, newest request wins.
        Exclusive,
        /// Several at a time, bounded, first come first served.
        Animated,
    };
    Q_ENUM(Lane)

    // Not defaulted because a default-constructible QML_SINGLETON is built by the engine itself rather
    // than through create(), which would fork the QML-visible object off from
    // the one C++ and the tests hold.
    explicit VideoPlaybackArbiter(QObject *parent);

    static VideoPlaybackArbiter *instance();
    static VideoPlaybackArbiter *create(QQmlEngine *qmlEngine, QJSEngine *jsEngine);

    /**
     * Asks to play. Returns whether the claimant may.
     *
     * Exclusive always returns true and revokes whoever held it. Animated
     * returns false when the lane is full, and remembers the claimant so it is
     * offered the next free slot in the order it asked.
     */
    Q_INVOKABLE bool request(QObject *claimant, VideoPlaybackArbiter::Lane lane);

    /// Gives up a grant, and gives up a place in the animated queue. Safe to
    /// call for a claimant that holds nothing, which is what a surface does
    /// every time playback stops.
    Q_INVOKABLE void release(QObject *claimant);

    /// Where this message was left, in seconds, or 0 if it has not been played.
    Q_INVOKABLE double resumePosition(const QString &messageId) const;
    Q_INVOKABLE void setResumePosition(const QString &messageId, double seconds);
    /// Forgets a position, so the next play starts from the beginning. What a
    /// clip that ran to its end does.
    Q_INVOKABLE void clearResumePosition(const QString &messageId);

    /// Testing hooks. The live limit is the user's setting; the tests set it
    /// directly so they do not depend on a QSettings store.
    void setAnimatedLimit(int limit);
    int animatedLimit() const;
    bool holds(const QObject *claimant) const;
    int animatedHolderCount() const;

Q_SIGNALS:
    /// This claimant must stop now: something else took the exclusive lane.
    /// Broadcast, so every surface checks whether it is the one named.
    void revoked(QObject *claimant);
    /// An animated slot came free and this claimant is next in line. It should
    /// ask again if it still wants to play.
    void grantOffered(QObject *claimant);
    /// Where this message was left has changed. A bubble cannot simply read the
    /// position when its surface lets go: both are reacting to the same
    /// property change, in an order QML does not promise, so it would as often
    /// read the value from before the save.
    void resumePositionChanged(const QString &messageId);

private:
    void offerAnimatedSlot();
    void rememberOrder(const QString &messageId);

    /// The most recent positions, so scrolling a long way back does not resume
    /// a clip from where it was hours ago and the hash cannot grow without end.
    static constexpr int resumeHistoryLimit = 32;

    QPointer<QObject> m_exclusive;
    /// QPointer throughout: a bubble is destroyed by the list recycling it at
    /// any moment, holding a grant or waiting for one.
    QList<QPointer<QObject>> m_animated;
    QList<QPointer<QObject>> m_animatedWaiting;

    QHash<QString, double> m_positions;
    QList<QString> m_positionOrder;

    /// -1 means "read the user's setting". Tests override it.
    int m_animatedLimitOverride = -1;
};
