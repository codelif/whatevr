// SPDX-License-Identifier: BSD-3-Clause
//
// Does video actually reach the screen, and does it survive being moved between
// views?
//
// Both questions used to be answerable only by running the app and looking at
// it, which is how a whole release shipped playing sound over a black
// rectangle: mpv decides whether a file has video when it opens it, and a file
// opened before its render context exists plays through to the end with the
// video track switched off. This test opens a real engine, renders a real
// frame into a real scene graph and reads the pixels back.
//
// The fixture is uncompressed YUV4MPEG written here rather than an encoded clip
// checked in: it needs no encoder, no fixture file and no codec to be present,
// and mpv demuxes it by content like anything else.

#include <QGuiApplication>
#include <QImage>
#include <QQuickItem>
#include <QQuickWindow>
#include <QSGRendererInterface>
#include <QSignalSpy>
#include <QTemporaryDir>
#include <QTest>

#include "mpvcore.h"
#include "playbacksession.h"

namespace
{

constexpr int frameWidth = 64;
constexpr int frameHeight = 64;
constexpr int frameCount = 30;
/// Mid-grey luma, well clear of the black mpv clears an empty framebuffer to.
constexpr char lumaValue = char(0xB0);
constexpr char chromaValue = char(0x80);

/// A 64x64 grey clip as YUV4MPEG2, the simplest thing a demuxer will accept.
bool writeFixture(const QString &path)
{
    QFile file(path);
    if (!file.open(QIODevice::WriteOnly)) {
        return false;
    }
    file.write(QStringLiteral("YUV4MPEG2 W%1 H%2 F10:1 Ip A1:1 C420\n")
                   .arg(frameWidth)
                   .arg(frameHeight)
                   .toUtf8());
    const QByteArray luma(frameWidth * frameHeight, lumaValue);
    const QByteArray chroma((frameWidth / 2) * (frameHeight / 2), chromaValue);
    for (int i = 0; i < frameCount; ++i) {
        file.write("FRAME\n");
        file.write(luma);
        file.write(chroma);
        file.write(chroma);
    }
    file.close();
    return true;
}

/// Whether anything brighter than the cleared framebuffer was drawn.
bool hasBrightPixels(const QImage &image)
{
    for (int y = 0; y < image.height(); ++y) {
        for (int x = 0; x < image.width(); ++x) {
            const QRgb pixel = image.pixel(x, y);
            if (qRed(pixel) > 60 && qGreen(pixel) > 60 && qBlue(pixel) > 60) {
                return true;
            }
        }
    }
    return false;
}

}

class TestMpvRender : public QObject
{
    Q_OBJECT

private Q_SLOTS:
    void initTestCase()
    {
        // Same pin as main(): libmpv renders through OpenGL and a scene graph on
        // any other RHI backend cannot show a frame at all.
        QQuickWindow::setGraphicsApi(QSGRendererInterface::OpenGL);

        QVERIFY(m_dir.isValid());
        m_fixture = m_dir.filePath(QStringLiteral("grey.y4m"));
        QVERIFY(writeFixture(m_fixture));

        MpvCore probe(MpvCore::Mode::Video);
        if (!probe.isValid()) {
            QSKIP("libmpv could not create an instance here");
        }
    }

    void aSessionDrawsItsClipIntoTheViewThatHoldsIt()
    {
        QQuickWindow window;
        window.resize(frameWidth, frameHeight);
        auto *container = new QQuickItem(window.contentItem());
        container->setSize(QSizeF(frameWidth, frameHeight));
        window.show();
        if (!QTest::qWaitForWindowExposed(&window)) {
            QSKIP("no exposed window on this platform");
        }
        if (window.rendererInterface()->graphicsApi() != QSGRendererInterface::OpenGL) {
            // The offscreen platform falls back to the software renderer, which
            // has no framebuffer objects and so nothing for mpv to draw into.
            QSKIP("scene graph is not on OpenGL here");
        }

        PlaybackSession session;
        session.attachView(container);
        session.setMuted(true);
        session.configure(QStringLiteral("clip"), QUrl::fromLocalFile(m_fixture), 0.0);
        session.setPlaying(true);

        // hasVideo is mpv reporting a decoded size, which it only has once a
        // frame exists: the property every bubble swaps its poster out on.
        QTRY_VERIFY_WITH_TIMEOUT(session.hasVideo(), 15000);
        QTRY_VERIFY_WITH_TIMEOUT(hasBrightPixels(window.grabWindow()), 15000);
    }

    void aStillCanBeTakenFromWhatIsOnScreen()
    {
        QQuickWindow window;
        window.resize(frameWidth, frameHeight);
        auto *container = new QQuickItem(window.contentItem());
        container->setSize(QSizeF(frameWidth, frameHeight));
        window.show();
        if (!QTest::qWaitForWindowExposed(&window)) {
            QSKIP("no exposed window on this platform");
        }
        if (window.rendererInterface()->graphicsApi() != QSGRendererInterface::OpenGL) {
            // The offscreen platform falls back to the software renderer, which
            // has no framebuffer objects and so nothing for mpv to draw into.
            QSKIP("scene graph is not on OpenGL here");
        }

        PlaybackSession session;
        session.attachView(container);
        session.setMuted(true);
        session.configure(QStringLiteral("clip"), QUrl::fromLocalFile(m_fixture), 0.0);
        session.setPlaying(true);
        QTRY_VERIFY_WITH_TIMEOUT(session.hasVideo(), 15000);

        QSignalSpy stills(&session, &PlaybackSession::stillGrabbed);
        session.captureStill();
        QCOMPARE(stills.count(), 1);
        const QImage still = stills.first().at(1).value<QImage>();
        QCOMPARE(still.size(), QSize(frameWidth, frameHeight));
        QVERIFY(hasBrightPixels(still));
    }

    void movingASessionToAnotherViewKeepsTheClipRunningAndDrawing()
    {
        QQuickWindow window;
        window.resize(frameWidth, frameHeight * 2);
        auto *first = new QQuickItem(window.contentItem());
        first->setSize(QSizeF(frameWidth, frameHeight));
        auto *second = new QQuickItem(window.contentItem());
        second->setY(frameHeight);
        second->setSize(QSizeF(frameWidth, frameHeight));
        window.show();
        if (!QTest::qWaitForWindowExposed(&window)) {
            QSKIP("no exposed window on this platform");
        }
        if (window.rendererInterface()->graphicsApi() != QSGRendererInterface::OpenGL) {
            // The offscreen platform falls back to the software renderer, which
            // has no framebuffer objects and so nothing for mpv to draw into.
            QSKIP("scene graph is not on OpenGL here");
        }

        PlaybackSession session;
        session.attachView(first);
        session.setMuted(true);
        session.configure(QStringLiteral("clip"), QUrl::fromLocalFile(m_fixture), 0.0);
        session.setPlaying(true);
        QTRY_VERIFY_WITH_TIMEOUT(session.hasVideo(), 15000);

        QSignalSpy sources(&session, &PlaybackSession::sourceChanged);
        // What a handoff does: the view being left goes first (it is revoked
        // before the new one is handed the session), and the file is never
        // reopened.
        session.detachView(first);
        session.attachView(second);
        QCOMPARE(sources.count(), 0);
        QVERIFY(session.playing());

        // The picture comes back in the new view. It may take a moment: leaving
        // the scene frees mpv's render context, which takes the video track
        // with it, and the session reopens the track once a context exists
        // again.
        QTRY_VERIFY_WITH_TIMEOUT(session.hasVideo(), 15000);
        QTRY_VERIFY_WITH_TIMEOUT(hasBrightPixels(window.grabWindow()), 15000);
    }

private:
    QTemporaryDir m_dir;
    QString m_fixture;
};

QTEST_MAIN(TestMpvRender)
#include "tst_mpvrender.moc"
