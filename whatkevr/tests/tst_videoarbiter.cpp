// SPDX-License-Identifier: BSD-3-Clause
#include <QColor>
#include <QImage>
#include <QSignalSpy>
#include <QTest>

#include "videoarbiter.h"

/**
 * Who is allowed to be playing.
 *
 * Every case here is a bug that reached Harsh. Several videos audible at once,
 * a clip that opened full screen while the copy in the conversation kept
 * running, a video note that spun a busy indicator forever because it asked for
 * a decoder, was refused, and was never told when one came free.
 *
 * The claimants are plain QObjects: the arbiter deliberately knows nothing
 * about surfaces or engines, which is what lets the same rule hold on the Qt
 * Multimedia path, where there is no decoder pool at all.
 */
class TestVideoArbiter : public QObject
{
    Q_OBJECT

private:
    /// A fresh arbiter per case. The app-wide instance() is a singleton, and a
    /// test that inherited another test's grants would pass or fail by order.
    VideoPlaybackArbiter *arbiter()
    {
        m_arbiter = std::make_unique<VideoPlaybackArbiter>(nullptr);
        return m_arbiter.get();
    }

    std::unique_ptr<VideoPlaybackArbiter> m_arbiter;

private Q_SLOTS:
    void exclusiveIsExactlyOneAndTheNewestRequestWins()
    {
        VideoPlaybackArbiter *pool = arbiter();
        QObject first;
        QObject second;

        QVERIFY(pool->request(&first, VideoPlaybackArbiter::Exclusive));
        QVERIFY(pool->holds(&first));

        QSignalSpy revoked(pool, &VideoPlaybackArbiter::revoked);
        // Never refused: a video the user just tapped must start, and the one
        // that was running must stop. Refusing here is what left a tap on a
        // second clip doing nothing at all.
        QVERIFY(pool->request(&second, VideoPlaybackArbiter::Exclusive));

        QCOMPARE(revoked.count(), 1);
        QCOMPARE(revoked.first().first().value<QObject *>(), &first);
        QVERIFY(pool->holds(&second));
        QVERIFY(!pool->holds(&first));
    }

    void theRevokedHolderCanReleaseWithoutTakingTheNewOneWithIt()
    {
        VideoPlaybackArbiter *pool = arbiter();
        QObject first;
        QObject second;

        QVERIFY(pool->request(&first, VideoPlaybackArbiter::Exclusive));

        // A surface stops synchronously when it hears it was revoked, and its
        // release lands while the handover is still in progress. If the arbiter
        // handed over after announcing rather than before, this release would
        // clear the grant it had just given away, and the new clip would hold
        // the lane on paper while playing nothing.
        connect(pool, &VideoPlaybackArbiter::revoked, this, [pool](QObject *claimant) {
            pool->release(claimant);
        });
        QVERIFY(pool->request(&second, VideoPlaybackArbiter::Exclusive));
        QVERIFY(pool->holds(&second));
    }

    void theAnimatedLaneIsBoundedAndServesTheOldestWaiterFirst()
    {
        VideoPlaybackArbiter *pool = arbiter();
        pool->setAnimatedLimit(2);

        QObject first;
        QObject second;
        QObject thirdInLine;
        QObject fourthInLine;

        QVERIFY(pool->request(&first, VideoPlaybackArbiter::Animated));
        QVERIFY(pool->request(&second, VideoPlaybackArbiter::Animated));
        // Refused rather than granted, and refused rather than evicting: a GIF
        // is decoration and does not get to interrupt anything.
        QVERIFY(!pool->request(&thirdInLine, VideoPlaybackArbiter::Animated));
        QVERIFY(!pool->request(&fourthInLine, VideoPlaybackArbiter::Animated));
        QCOMPARE(pool->animatedHolderCount(), 2);

        QSignalSpy offered(pool, &VideoPlaybackArbiter::grantOffered);
        pool->release(&first);

        // One offer, to the one that has been waiting longest. Broadcasting to
        // everyone would have them race for a single slot.
        QCOMPARE(offered.count(), 1);
        QCOMPARE(offered.first().first().value<QObject *>(), &thirdInLine);
        QVERIFY(pool->request(&thirdInLine, VideoPlaybackArbiter::Animated));
        QCOMPARE(pool->animatedHolderCount(), 2);
    }

    void givingUpTakesTheClaimantOutOfTheQueue()
    {
        VideoPlaybackArbiter *pool = arbiter();
        pool->setAnimatedLimit(1);

        QObject holder;
        QObject gaveUp;
        QObject stillWants;

        QVERIFY(pool->request(&holder, VideoPlaybackArbiter::Animated));
        QVERIFY(!pool->request(&gaveUp, VideoPlaybackArbiter::Animated));
        QVERIFY(!pool->request(&stillWants, VideoPlaybackArbiter::Animated));

        // Scrolled out of view before a slot ever came free.
        pool->release(&gaveUp);

        QSignalSpy offered(pool, &VideoPlaybackArbiter::grantOffered);
        pool->release(&holder);

        QCOMPARE(offered.count(), 1);
        QCOMPARE(offered.first().first().value<QObject *>(), &stillWants);
    }

    void aDestroyedWaiterIsSkippedRatherThanStallingTheQueue()
    {
        VideoPlaybackArbiter *pool = arbiter();
        pool->setAnimatedLimit(1);

        QObject holder;
        QObject survivor;
        QVERIFY(pool->request(&holder, VideoPlaybackArbiter::Animated));
        {
            QObject recycled;
            QVERIFY(!pool->request(&recycled, VideoPlaybackArbiter::Animated));
            QVERIFY(!pool->request(&survivor, VideoPlaybackArbiter::Animated));
        }

        // A list delegate destroyed while it waited is the common case, not the
        // exception. The offer has to walk past it.
        QSignalSpy offered(pool, &VideoPlaybackArbiter::grantOffered);
        pool->release(&holder);

        QCOMPARE(offered.count(), 1);
        QCOMPARE(offered.first().first().value<QObject *>(), &survivor);
    }

    void theTwoLanesDoNotCompete()
    {
        VideoPlaybackArbiter *pool = arbiter();
        pool->setAnimatedLimit(2);

        QObject gifOne;
        QObject gifTwo;
        QObject video;

        QVERIFY(pool->request(&gifOne, VideoPlaybackArbiter::Animated));
        QVERIFY(pool->request(&gifTwo, VideoPlaybackArbiter::Animated));

        QSignalSpy revoked(pool, &VideoPlaybackArbiter::revoked);
        QVERIFY(pool->request(&video, VideoPlaybackArbiter::Exclusive));

        // A full animated lane must not refuse a video, and starting a video
        // must not stop the GIFs: they are decoration, silent, and cheap.
        QCOMPARE(revoked.count(), 0);
        QVERIFY(pool->holds(&video));
        QCOMPARE(pool->animatedHolderCount(), 2);
    }

    void aClaimantChangingLanesDoesNotHoldBoth()
    {
        VideoPlaybackArbiter *pool = arbiter();
        pool->setAnimatedLimit(1);

        // A recycled list delegate is reused for a message of another kind, so
        // the same surface asks in the other lane. Leaving it counted in the
        // old one holds a slot nothing can ever free.
        QObject surface;
        QVERIFY(pool->request(&surface, VideoPlaybackArbiter::Animated));
        QCOMPARE(pool->animatedHolderCount(), 1);

        QVERIFY(pool->request(&surface, VideoPlaybackArbiter::Exclusive));
        QCOMPARE(pool->animatedHolderCount(), 0);
        QVERIFY(pool->holds(&surface));

        QObject gif;
        QVERIFY(pool->request(&gif, VideoPlaybackArbiter::Animated));
    }

    void positionsAreRememberedForgottenAndEvicted()
    {
        VideoPlaybackArbiter *pool = arbiter();

        pool->setResumePosition(QStringLiteral("first"), 12.5);
        QCOMPARE(pool->resumePosition(QStringLiteral("first")), 12.5);
        QCOMPARE(pool->resumePosition(QStringLiteral("never-played")), 0.0);

        // A clip that ran to its end is not resumed from its last frame.
        pool->clearResumePosition(QStringLiteral("first"));
        QCOMPARE(pool->resumePosition(QStringLiteral("first")), 0.0);

        QSignalSpy changed(pool, &VideoPlaybackArbiter::resumePositionChanged);
        pool->setResumePosition(QStringLiteral("watched"), 3.0);
        QCOMPARE(changed.count(), 1);
        QCOMPARE(changed.first().first().toString(), QStringLiteral("watched"));

        // Bounded, or a long session in a media-heavy chat grows it without
        // end. The oldest goes; the one just touched stays.
        for (int i = 0; i < 40; ++i) {
            pool->setResumePosition(QStringLiteral("clip-%1").arg(i), i + 1);
        }
        QCOMPARE(pool->resumePosition(QStringLiteral("watched")), 0.0);
        QCOMPARE(pool->resumePosition(QStringLiteral("clip-0")), 0.0);
        QCOMPARE(pool->resumePosition(QStringLiteral("clip-39")), 40.0);
    }

    void fullScreenHandoffCarriesPositionAndPlayIntent()
    {
        VideoPlaybackArbiter *pool = arbiter();
        QSignalSpy handoff(pool, &VideoPlaybackArbiter::inlineHandoff);

        pool->handoffToInline(QStringLiteral("video"), 18.25, true);
        QCOMPARE(pool->resumePosition(QStringLiteral("video")), 18.25);
        QCOMPARE(handoff.count(), 1);
        QCOMPARE(handoff.first().at(0).toString(), QStringLiteral("video"));
        QCOMPARE(handoff.first().at(1).toDouble(), 18.25);
        QCOMPARE(handoff.first().at(2).toBool(), true);

        pool->handoffToInline(QStringLiteral("video"), 22.0, false);
        QCOMPARE(handoff.count(), 2);
        QCOMPARE(handoff.last().at(2).toBool(), false);
    }

    // A position alone does not hand a clip over: the receiving surface has to
    // open a file and seek before it can draw, and until then it showed the
    // daemon's poster, which is the clip's *first* frame. The still is what
    // fills that gap in both directions.
    void stillsAreKeptRevisionedAndEvicted()
    {
        VideoPlaybackArbiter *pool = arbiter();
        QSignalSpy captured(pool, &VideoPlaybackArbiter::frameCaptured);

        QVERIFY(!pool->hasFrame(QStringLiteral("clip")));
        QCOMPARE(pool->frameRevision(QStringLiteral("clip")), 0);
        QVERIFY(pool->frameImage(QStringLiteral("clip")).isNull());

        pool->captureImage(QStringLiteral("clip"), frameOfColour(Qt::red));
        QVERIFY(pool->hasFrame(QStringLiteral("clip")));
        QCOMPARE(pool->frameRevision(QStringLiteral("clip")), 1);
        QCOMPARE(pool->frameImage(QStringLiteral("clip")).pixelColor(0, 0), QColor(Qt::red));
        QCOMPARE(captured.count(), 1);
        QCOMPARE(captured.first().first().toString(), QStringLiteral("clip"));

        // The revision is what makes a url reload: without it a second capture
        // of the same message would be answered from the image cache and the
        // viewer would keep showing the first one forever.
        pool->captureImage(QStringLiteral("clip"), frameOfColour(Qt::green));
        QCOMPARE(pool->frameRevision(QStringLiteral("clip")), 2);
        QCOMPARE(pool->frameImage(QStringLiteral("clip")).pixelColor(0, 0), QColor(Qt::green));

        // An invalid frame is what a decoder that has opened a file but drawn
        // nothing hands out; storing it would replace a good still with black.
        pool->captureImage(QStringLiteral("clip"), QImage());
        QCOMPARE(pool->frameRevision(QStringLiteral("clip")), 2);
        QCOMPARE(pool->frameImage(QStringLiteral("clip")).pixelColor(0, 0), QColor(Qt::green));
        QCOMPARE(captured.count(), 2);

        // Images, not doubles: far fewer are worth keeping than positions.
        for (int i = 0; i < 12; ++i) {
            pool->captureImage(QStringLiteral("other-%1").arg(i), frameOfColour(Qt::blue));
        }
        QVERIFY(!pool->hasFrame(QStringLiteral("clip")));
        QVERIFY(!pool->hasFrame(QStringLiteral("other-0")));
        QVERIFY(pool->hasFrame(QStringLiteral("other-11")));
    }

    void acquireHandsOutOneSessionPerMessage()
    {
        VideoPlaybackArbiter *pool = arbiter();
        QObject bubble;

        PlaybackSession *session = pool->acquire(&bubble, QStringLiteral("clip"),
                                                 QUrl(QStringLiteral("file:///tmp/a.mp4")), 0,
                                                 VideoPlaybackArbiter::Exclusive);
        QVERIFY(session);
        QCOMPARE(session->messageId(), QStringLiteral("clip"));
        QVERIFY(pool->holds(&bubble));
        QCOMPARE(pool->sessionFor(&bubble), session);

        // Asking again for the same message is idempotent, not a rebuild.
        QCOMPARE(pool->acquire(&bubble, QStringLiteral("clip"), QUrl(), 0,
                               VideoPlaybackArbiter::Exclusive),
                 session);
    }

    void aHandoffTransfersTheSessionWithoutStoppingIt()
    {
        VideoPlaybackArbiter *pool = arbiter();
        QObject bubble;
        QObject viewer;

        PlaybackSession *session = pool->acquire(&bubble, QStringLiteral("clip"),
                                                 QUrl(QStringLiteral("file:///tmp/a.mp4")), 0,
                                                 VideoPlaybackArbiter::Exclusive);
        QVERIFY(session);
        session->setPlaying(true);

        QSignalSpy revoked(pool, &VideoPlaybackArbiter::revoked);
        QSignalSpy sourceChanged(session, &PlaybackSession::sourceChanged);
        QSignalSpy playingChanged(session, &PlaybackSession::playingChanged);

        // The viewer opening on the same clip gets the same engine, running.
        PlaybackSession *transferred = pool->acquire(&viewer, QStringLiteral("clip"), QUrl(), 0,
                                                     VideoPlaybackArbiter::Exclusive);
        QCOMPARE(transferred, session);
        QCOMPARE(revoked.count(), 1);
        QCOMPARE(revoked.first().first().value<QObject *>(), &bubble);
        QVERIFY(session->playing());
        QCOMPARE(sourceChanged.count(), 0);
        QCOMPARE(playingChanged.count(), 0);

        // The revoked holder letting go afterwards must not pause the clip it
        // no longer owns: this exact release used to be the audio gap.
        pool->release(&bubble);
        QVERIFY(session->playing());
        QCOMPARE(pool->sessionFor(&viewer), session);
    }

    void theLastReleaseParksAfterAGraceForAnImmediateReacquire()
    {
        VideoPlaybackArbiter *pool = arbiter();
        QObject viewer;

        PlaybackSession *session = pool->acquire(&viewer, QStringLiteral("clip"),
                                                 QUrl(QStringLiteral("file:///tmp/a.mp4")), 0,
                                                 VideoPlaybackArbiter::Exclusive);
        QVERIFY(session);
        session->setPlaying(true);
        pool->release(&viewer);

        // Paused, not parked: the viewer closing back into the bubble is a
        // release and a re-acquire one tick apart, and re-opening the file
        // for that would put the audio gap back.
        QVERIFY(!session->playing());
        QCOMPARE(pool->liveSessionFor(QStringLiteral("clip")), session);

        QObject bubble;
        QCOMPARE(pool->acquire(&bubble, QStringLiteral("clip"), QUrl(), 0,
                               VideoPlaybackArbiter::Exclusive),
                 session);

        // Once nobody comes back for it, the grace runs out and the session
        // is parked for reuse under another message.
        pool->release(&bubble);
        QTest::qWait(2600);
        QCOMPARE(pool->liveSessionFor(QStringLiteral("clip")), nullptr);
        QVERIFY(session->messageId().isEmpty());
    }

    void anAnimatedSessionIsTransferredToAnExclusiveClaimant()
    {
        VideoPlaybackArbiter *pool = arbiter();
        pool->setAnimatedLimit(2);
        QObject gifBubble;
        QObject viewer;

        PlaybackSession *session = pool->acquire(&gifBubble, QStringLiteral("gif"),
                                                 QUrl(QStringLiteral("file:///tmp/a.gif")), 0,
                                                 VideoPlaybackArbiter::Animated);
        QVERIFY(session);

        QSignalSpy revoked(pool, &VideoPlaybackArbiter::revoked);
        // Full screen for a GIF is the exclusive lane, but it must not leave
        // the inline copy decoding the same clip behind the modal.
        PlaybackSession *transferred = pool->acquire(&viewer, QStringLiteral("gif"), QUrl(), 0,
                                                     VideoPlaybackArbiter::Exclusive);
        QCOMPARE(transferred, session);
        QCOMPARE(revoked.count(), 1);
        QCOMPARE(revoked.first().first().value<QObject *>(), &gifBubble);
        QVERIFY(pool->holds(&viewer));
        QCOMPARE(pool->sessionFor(&viewer), session);
    }

private:
    /// A one-colour frame, the smallest thing captureImage() will accept.
    static QImage frameOfColour(const QColor &colour)
    {
        QImage image(4, 4, QImage::Format_RGBA8888);
        image.fill(colour);
        return image;
    }
};

QTEST_GUILESS_MAIN(TestVideoArbiter)
#include "tst_videoarbiter.moc"
