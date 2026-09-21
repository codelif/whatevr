#include "audiorecorder.h"

#include <QAudioInput>
#include <QDir>
#include <QFile>
#include <QMediaCaptureSession>
#include <QMediaFormat>
#include <QMediaRecorder>
#include <QStandardPaths>
#include <QUrl>
#include <QUuid>

AudioRecorder::AudioRecorder(QObject *parent)
    : QObject(parent)
    , m_audioInput(new QAudioInput(this))
    , m_capture(new QMediaCaptureSession(this))
    , m_recorder(new QMediaRecorder(this))
{
    m_capture->setAudioInput(m_audioInput);
    m_capture->setRecorder(m_recorder);
    connect(m_recorder, &QMediaRecorder::recorderStateChanged, this, [this](QMediaRecorder::RecorderState state) {
        const bool recording = state == QMediaRecorder::RecordingState;
        const bool finished = m_recording && state == QMediaRecorder::StoppedState;
        if (m_recording != recording) {
            m_recording = recording;
            Q_EMIT recordingChanged();
        }
        if (finished && !m_outputPath.isEmpty())
            Q_EMIT recordingFinished(m_outputPath);
    });
    connect(m_recorder, &QMediaRecorder::errorOccurred, this, [this](QMediaRecorder::Error, const QString &message) {
        Q_EMIT errorOccurred(message);
    });
}

AudioRecorder::~AudioRecorder()
{
    cancel();
}

bool AudioRecorder::start()
{
    if (m_recording) {
        return false;
    }
    // Drop an unsent previous recording (never handed to the daemon).
    // Files already handed off have m_outputPath cleared by resetAfterSend
    // and are left for the daemon to copy.
    if (!m_outputPath.isEmpty()) {
        QFile::remove(m_outputPath);
        m_outputPath.clear();
    }
    QString dir = QStandardPaths::writableLocation(QStandardPaths::TempLocation);
    if (dir.isEmpty()) {
        dir = QDir::tempPath();
    }
    const QString path = QDir(dir).filePath(QStringLiteral("whatevr-voice-%1.ogg")
                                                .arg(QUuid::createUuid().toString(QUuid::WithoutBraces)));
    QMediaFormat format;
    format.setFileFormat(QMediaFormat::Ogg);
    format.setAudioCodec(QMediaFormat::AudioCodec::Opus);
    m_recorder->setMediaFormat(format);
    m_recorder->setOutputLocation(QUrl::fromLocalFile(path));
    m_outputPath = path;
    Q_EMIT outputPathChanged();
    m_recorder->record();
    return true;
}

QString AudioRecorder::stop()
{
    if (!m_recording) {
        return {};
    }
    m_recorder->stop();
    return m_outputPath;
}

void AudioRecorder::resetAfterSend()
{
    // The daemon copies the file asynchronously after sendMedia returns, so
    // the temp file must NOT be deleted here — doing so produced
    // "media file is not accessible". The file is removed on the next
    // start()/cancel() or on destruction; /tmp aging reclaims any leftovers.
    m_outputPath.clear();
    Q_EMIT outputPathChanged();
}

void AudioRecorder::cancel()
{
    if (m_recording) {
        m_recorder->stop();
        m_recording = false;
        Q_EMIT recordingChanged();
    }
    if (!m_outputPath.isEmpty()) {
        QFile::remove(m_outputPath);
        m_outputPath.clear();
        Q_EMIT outputPathChanged();
    }
}
