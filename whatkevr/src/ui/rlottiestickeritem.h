#pragma once

#include <QImage>
#include <QQuickPaintedItem>
#include <QUrl>
#include <QVariantAnimation>
#include <QtQml/qqmlregistration.h>

#include <cstdint>
#include <memory>

namespace rlottie {
class Animation;
}

class RlottieStickerItem : public QQuickPaintedItem
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

    void paint(QPainter *painter) override;

Q_SIGNALS:
    void sourceChanged();
    void playingChanged();
    void renderScaleChanged();
    void statusChanged();

protected:
    void geometryChange(const QRectF &newGeometry, const QRectF &oldGeometry) override;

private:
    void loadSource();
    void setStatus(Status status);
    void updateAnimationState();
    void renderCurrentFrame();
    void clearFrame();

    QUrl m_source;
    bool m_playing = false;
    qreal m_renderScale = 2.0;
    Status m_status = Status::Null;
    std::unique_ptr<rlottie::Animation> m_animation;
    QVariantAnimation m_playback;
    QImage m_frame;
    int m_currentFrame = 0;
    int m_renderedFrame = -1;
};
