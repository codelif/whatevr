#pragma once

#include <QImage>
#include <QQuickItem>
#include <QSize>
#include <QString>
#include <QUrl>
#include <QVariantAnimation>
#include <QtQml/qqmlregistration.h>

#include <cstdint>
#include <memory>

namespace rlottie {
class Animation;
}

// Owns the rlottie animation and does every expensive operation — JSON parsing
// and frame rasterisation — on a shared background thread, so a chat full of
// animated stickers never blocks the GUI thread while scrolling.
class LottieRenderWorker : public QObject
{
    Q_OBJECT

public:
    explicit LottieRenderWorker(QObject *parent = nullptr);
    ~LottieRenderWorker() override;

public Q_SLOTS:
    void load(const QString &path, quint64 generation);
    void render(int frame, QSize size, quint64 generation);

Q_SIGNALS:
    void loaded(quint64 generation, bool ok, int totalFrames, double durationSeconds);
    void rendered(quint64 generation, QImage image, int frame);

private:
    std::unique_ptr<rlottie::Animation> m_animation;
};

class RlottieStickerItem : public QQuickItem
{
    Q_OBJECT
    QML_NAMED_ELEMENT(RlottieSticker)

    Q_PROPERTY(QUrl source READ source WRITE setSource NOTIFY sourceChanged)
    Q_PROPERTY(bool playing READ playing WRITE setPlaying NOTIFY playingChanged)
    Q_PROPERTY(qreal renderScale READ renderScale WRITE setRenderScale NOTIFY renderScaleChanged)
    Q_PROPERTY(Status status READ status NOTIFY statusChanged)

public:
    enum class Status : std::uint8_t {
        Null,
        Loading,
        Ready,
        Error,
    };
    Q_ENUM(Status)

    explicit RlottieStickerItem(QQuickItem *parent = nullptr);
    ~RlottieStickerItem() override;

    [[nodiscard]] QUrl source() const;
    void setSource(const QUrl &source);

    [[nodiscard]] bool playing() const;
    void setPlaying(bool playing);

    [[nodiscard]] qreal renderScale() const;
    void setRenderScale(qreal renderScale);

    [[nodiscard]] Status status() const;

Q_SIGNALS:
    void sourceChanged();
    void playingChanged();
    void renderScaleChanged();
    void statusChanged();

protected:
    QSGNode *updatePaintNode(QSGNode *oldNode, UpdatePaintNodeData *data) override;
    void geometryChange(const QRectF &newGeometry, const QRectF &oldGeometry) override;

private:
    void loadSource();
    void setStatus(Status status);
    void updateAnimationState();
    void requestFrame(int frame);
    [[nodiscard]] QSize targetPixelSize() const;
    [[nodiscard]] int frameForPosition(double position) const;

    void onLoaded(quint64 generation, bool ok, int totalFrames, double durationSeconds);
    void onRendered(quint64 generation, const QImage &image, int frame);

    QUrl m_source;
    bool m_playing = false;
    qreal m_renderScale = 2.0;
    Status m_status = Status::Null;

    LottieRenderWorker *m_worker = nullptr;
    quint64 m_generation = 0;
    int m_totalFrames = 0;

    QVariantAnimation m_playback;
    int m_currentFrame = 0;
    int m_renderedFrame = -1;

    bool m_renderInFlight = false;
    int m_pendingFrame = -1;

    // Written on the GUI thread, read by updatePaintNode while the GUI thread is
    // blocked during the scene-graph sync phase, so no lock is required.
    QImage m_frontImage;
    bool m_textureDirty = false;
};
