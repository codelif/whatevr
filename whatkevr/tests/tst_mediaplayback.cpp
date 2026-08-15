// SPDX-License-Identifier: BSD-3-Clause
#include <QSignalSpy>
#include <QTemporaryDir>
#include <QTest>
#include <QUrl>

#include "audioplayer.h"
#include "mpvcore.h"

/**
 * Playback regression tests.
 *
 * These exist because of a failure with no symptom: QApplication's constructor
 * calls setlocale(LC_ALL, "") and libmpv refuses to create an instance under a
 * non-C LC_NUMERIC, so every mpv_create() returned null, MpvCore::load()
 * early-returned, and every play button in the app quietly did nothing. Nothing
 * in the test suite could see it, because nothing asked whether an engine had
 * actually been created.
 *
 * The test process is built the same way: QTest's main constructs a
 * QCoreApplication, which runs the same initLocale(). If the guard in MpvCore
 * ever goes away, coreIsValidUnderTheApplicationLocale fails here first.
 */
class TestMediaPlayback : public QObject
{
    Q_OBJECT

private Q_SLOTS:
    void initTestCase();
    void coreIsValidUnderTheApplicationLocale();
    void loadingAFileReportsItsDuration();
    void audioPlayerReportsFinishedAtEndOfFile();
    void audioPlayerRefusesToPlayNothing();

private:
    /// Writes a short silent WAV, so the tests need no fixture binary and no
    /// encoder: a RIFF header plus zeroed PCM is a file mpv will play.
    QString writeSilentWav(double seconds);

    QTemporaryDir m_dir;
};

void TestMediaPlayback::initTestCase()
{
    QVERIFY(m_dir.isValid());
    // CI has no audio device; mpv's null output still decodes and still ends.
    qputenv("WHATKEVR_MPV_AO", "null");
}

QString TestMediaPlayback::writeSilentWav(double seconds)
{
    constexpr int sampleRate = 8000;
    constexpr int channels = 1;
    constexpr int bitsPerSample = 16;

    const int frames = int(seconds * sampleRate);
    const int dataBytes = frames * channels * (bitsPerSample / 8);

    QByteArray wav;
    QDataStream out(&wav, QIODevice::WriteOnly);
    out.setByteOrder(QDataStream::LittleEndian);

    wav.append("RIFF");
    out.device()->seek(wav.size());
    out << quint32(36 + dataBytes);
    wav.append("WAVEfmt ");
    out.device()->seek(wav.size());
    out << quint32(16) // PCM header size
        << quint16(1) // PCM
        << quint16(channels)
        << quint32(sampleRate)
        << quint32(sampleRate * channels * (bitsPerSample / 8))
        << quint16(channels * (bitsPerSample / 8))
        << quint16(bitsPerSample);
    wav.append("data");
    out.device()->seek(wav.size());
    out << quint32(dataBytes);
    wav.append(QByteArray(dataBytes, '\0'));

    const QString path = m_dir.filePath(QStringLiteral("silence.wav"));
    QFile file(path);
    if (!file.open(QIODevice::WriteOnly)) {
        return {};
    }
    file.write(wav);
    file.close();
    return path;
}

void TestMediaPlayback::coreIsValidUnderTheApplicationLocale()
{
    MpvCore core(MpvCore::Mode::Audio);
    QVERIFY2(core.isValid(),
             "mpv_create() failed: LC_NUMERIC is probably not \"C\" (see MpvCore::ensureNumericLocale)");
}

void TestMediaPlayback::loadingAFileReportsItsDuration()
{
    const QString path = writeSilentWav(0.4);
    QVERIFY(!path.isEmpty());

    MpvCore core(MpvCore::Mode::Audio);
    QVERIFY(core.isValid());

    QSignalSpy durations(&core, &MpvCore::durationChanged);
    core.load(QUrl::fromLocalFile(path), false);

    QTRY_VERIFY_WITH_TIMEOUT(core.duration() > 0.0, 5000);
    QVERIFY(core.duration() < 5.0);
    QVERIFY(!durations.isEmpty());
}

void TestMediaPlayback::audioPlayerReportsFinishedAtEndOfFile()
{
    const QString path = writeSilentWav(0.4);
    QVERIFY(!path.isEmpty());

    AudioPlayer player(nullptr);
    QVERIFY2(player.available(), "AudioPlayer has no usable mpv instance");

    QSignalSpy finished(&player, &AudioPlayer::finished);
    QSignalSpy started(&player, &AudioPlayer::started);
    player.play(QStringLiteral("msg-1"), QUrl::fromLocalFile(path), 0.4);

    QCOMPARE(player.messageId(), QStringLiteral("msg-1"));
    // started is what sends the played receipt, so it has to fire on real
    // playback and not merely on the request.
    QTRY_VERIFY_WITH_TIMEOUT(!started.isEmpty(), 5000);
    QCOMPARE(started.first().first().toString(), QStringLiteral("msg-1"));

    QTRY_VERIFY_WITH_TIMEOUT(!finished.isEmpty(), 10000);
    QCOMPARE(finished.first().first().toString(), QStringLiteral("msg-1"));
    // A note played to the end has no resume point to come back to.
    QCOMPARE(player.resumePosition(QStringLiteral("msg-1")), 0.0);
}

void TestMediaPlayback::audioPlayerRefusesToPlayNothing()
{
    AudioPlayer player(nullptr);
    QSignalSpy started(&player, &AudioPlayer::started);
    player.play(QString(), QUrl::fromLocalFile(QStringLiteral("/nonexistent.wav")), 0);
    QVERIFY(player.messageId().isEmpty());
    QVERIFY(started.isEmpty());
}

QTEST_MAIN(TestMediaPlayback)

#include "tst_mediaplayback.moc"
