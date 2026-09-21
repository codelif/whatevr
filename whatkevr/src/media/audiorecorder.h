#pragma once

#include <QObject>
#include <QString>
#include <qqmlintegration.h>

class QAudioInput;
class QMediaCaptureSession;
class QMediaRecorder;

class AudioRecorder : public QObject
{
    Q_OBJECT
    QML_NAMED_ELEMENT(AudioRecorder)
    Q_PROPERTY(bool recording READ recording NOTIFY recordingChanged FINAL)
    Q_PROPERTY(QString outputPath READ outputPath NOTIFY outputPathChanged FINAL)

public:
    explicit AudioRecorder(QObject *parent = nullptr);
    ~AudioRecorder() override;

    [[nodiscard]] bool recording() const { return m_recording; }
    [[nodiscard]] QString outputPath() const { return m_outputPath; }

    Q_INVOKABLE bool start();
    Q_INVOKABLE QString stop();
    Q_INVOKABLE void cancel();
    Q_INVOKABLE void resetAfterSend();

Q_SIGNALS:
    void recordingChanged();
    void outputPathChanged();
    void errorOccurred(const QString &message);
    void recordingFinished(const QString &path);

private:
    QAudioInput *m_audioInput = nullptr;
    QMediaCaptureSession *m_capture = nullptr;
    QMediaRecorder *m_recorder = nullptr;
    QString m_outputPath;
    bool m_recording = false;
};
