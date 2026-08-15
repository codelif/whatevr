// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QHash>
#include <QImage>
#include <QList>
#include <QMutex>
#include <QObject>
#include <QPointer>
#include <QQmlEngine>
#include <QString>
#include <QVideoFrame>

#include "playbacksession.h"

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

    /**
     * Asks to play and hands back the session to play with, or nullptr when
     * the animated lane is full (the claimant is queued and offered a slot
     * later, exactly as request()).
     *
     * If a live session already exists for this message it is transferred:
     * the previous claimant is revoked and only lets go of its view, while
     * the session itself keeps running untouched. That transfer is the
     * bubble to full-screen handoff, and it is also what stops a clip from
     * ever decoding twice.
     */
    Q_INVOKABLE PlaybackSession *acquire(QObject *claimant,
                                         const QString &messageId,
                                         const QUrl &source,
                                         double startAt,
                                         VideoPlaybackArbiter::Lane lane);

    /// Points a held session at a different source (a stream promoted to its
    /// completed file), keeping position and play state.
    Q_INVOKABLE void updateSource(QObject *claimant, const QUrl &source);

    /// Gives up a grant, and gives up a place in the animated queue. Safe to
    /// call for a claimant that holds nothing, which is what a surface does
    /// every time playback stops. A session it held is paused and, after a
    /// short grace for an immediate re-acquire (the viewer closing back into
    /// the bubble), parked for reuse.
    Q_INVOKABLE void release(QObject *claimant);

    /// The session a claimant currently holds, or nullptr.
    [[nodiscard]] PlaybackSession *sessionFor(const QObject *claimant) const;
    /// The live session for a message, held or in its post-release grace, or
    /// nullptr.
    [[nodiscard]] PlaybackSession *liveSessionFor(const QString &messageId) const;

    /// Pauses every session that is currently making sound. What starting a
    /// voice note does to videos.
    Q_INVOKABLE void pauseAudibleSessions();

    /// Where this message was left, in seconds, or 0 if it has not been played.
    Q_INVOKABLE double resumePosition(const QString &messageId) const;
    Q_INVOKABLE void setResumePosition(const QString &messageId, double seconds);
    /// Forgets a position, so the next play starts from the beginning. What a
    /// clip that ran to its end does.
    Q_INVOKABLE void clearResumePosition(const QString &messageId);
    /// Delivers a completed full-screen handoff to the matching inline bubble.
    Q_INVOKABLE void handoffToInline(const QString &messageId, double seconds, bool resumePlayback);

    /**
     * Keeps the frame a decoder was showing when it let go.
     *
     * A position alone does not hand a clip over: the receiving side has to
     * open a file and seek before it can draw anything, and until then it fell
     * back to the daemon's poster: the *first* frame of the video, which is
     * neither where the user was nor, on a clip that opens on black, a picture
     * at all. The still is what the poster should have been for the length of
     * that gap, in both directions.
     *
     * Cheap to take (the sink already holds the frame; nothing is rendered or
     * grabbed) but expensive to keep, so far fewer are remembered than
     * positions.
     */
    Q_INVOKABLE void captureFrame(const QString &messageId, const QVideoFrame &frame);
    Q_INVOKABLE bool hasFrame(const QString &messageId) const;
    /// Bumped on every capture. QML puts it in the image url's query string,
    /// which is the only thing that makes a cached provider url reload.
    Q_INVOKABLE int frameRevision(const QString &messageId) const;
    /// Reads a still back. Called from the image provider, which Qt may run on
    /// its own thread, so the store is mutex-guarded rather than thread-affine.
    [[nodiscard]] QImage frameImage(const QString &messageId) const;

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
    void inlineHandoff(const QString &messageId, double seconds, bool resumePlayback);
    /// A new still is available for this message. Whoever is showing it re-reads
    /// frameRevision() and reloads.
    void frameCaptured(const QString &messageId);
    /// Some session began producing sound. What pauses the voice-note player.
    void audiblePlaybackStarted();

private:
    void offerAnimatedSlot();
    void rememberOrder(const QString &messageId);
    void rememberFrameOrder(const QString &messageId);
    /// Lane admission alone, shared by request() and acquire().
    bool admit(QObject *claimant, Lane lane);
    void bindSession(QObject *claimant, PlaybackSession *session);
    PlaybackSession *obtainParkedSession();
    void scheduleParking(PlaybackSession *session);

    /// The most recent positions, so scrolling a long way back does not resume
    /// a clip from where it was hours ago and the hash cannot grow without end.
    static constexpr int resumeHistoryLimit = 32;
    /// Stills are images, not doubles. Only the handful in play matter: the
    /// bubble handed to full screen and back, and whatever the user last
    /// scrolled away from.
    static constexpr int frameHistoryLimit = 6;
    /// Cap on a stored still's long edge. Everything showing one is at most a
    /// bubble or a screen wide, and a 4K frame per entry is not worth holding.
    static constexpr int frameMaxEdge = 1280;

    /// How long a released session keeps its decoder before parking, so the
    /// viewer closing back into a bubble (a release and a re-acquire one tick
    /// apart) resumes the same engine instead of opening the file again.
    static constexpr int parkGraceMs = 2000;

    QPointer<QObject> m_exclusive;
    /// QPointer throughout: a bubble is destroyed by the list recycling it at
    /// any moment, holding a grant or waiting for one.
    QList<QPointer<QObject>> m_animated;
    QList<QPointer<QObject>> m_animatedWaiting;

    /// Every session ever created, all owned by this object. Bound ones are
    /// values of m_bound; the rest are parked or in their post-release grace.
    QList<PlaybackSession *> m_sessions;
    QHash<const QObject *, PlaybackSession *> m_bound;

    QHash<QString, double> m_positions;
    QList<QString> m_positionOrder;

    /// Guards the still store alone: the image provider reads it from Qt's
    /// image-loading thread while QML writes it from the GUI thread.
    mutable QMutex m_frameMutex;
    QHash<QString, QImage> m_frames;
    QHash<QString, int> m_frameRevisions;
    QList<QString> m_frameOrder;

    /// -1 means "read the user's setting". Tests override it.
    int m_animatedLimitOverride = -1;
};
