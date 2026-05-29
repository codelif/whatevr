#include "rlottiestickeritem.h"

#include <QPainter>

#include <algorithm>
#include <cmath>

#include <rlottie.h>

namespace {
constexpr qreal kMinimumRenderScale = 1.0;
constexpr qreal kMaximumRenderScale = 4.0;
}

RlottieStickerItem::RlottieStickerItem(QQuickItem *parent)
    : QQuickPaintedItem(parent)
{
    setAntialiasing(true);
    setOpaquePainting(false);
    setRenderTarget(QQuickPaintedItem::FramebufferObject);
    m_playback.setStartValue(0.0);
    m_playback.setEndValue(1.0);
    m_playback.setLoopCount(-1);
    connect(&m_playback, &QVariantAnimation::valueChanged, this, [this](const QVariant &value) {
        if (!m_animation) {
            return;
        }
        m_currentFrame = static_cast<int>(m_animation->frameAtPos(value.toDouble()));
        renderCurrentFrame();
    });
}

RlottieStickerItem::~RlottieStickerItem() = default;

QUrl RlottieStickerItem::source() const
{
    return m_source;
}

void RlottieStickerItem::setSource(const QUrl &source)
{
    if (m_source == source) {
        return;
    }
    m_source = source;
    Q_EMIT sourceChanged();
    loadSource();
}

bool RlottieStickerItem::playing() const
{
    return m_playing;
}

void RlottieStickerItem::setPlaying(bool playing)
{
    if (m_playing == playing) {
        return;
    }
    m_playing = playing;
    Q_EMIT playingChanged();
    updateAnimationState();
}

qreal RlottieStickerItem::renderScale() const
{
    return m_renderScale;
}

void RlottieStickerItem::setRenderScale(qreal renderScale)
{
    const qreal boundedScale = std::clamp(renderScale, kMinimumRenderScale, kMaximumRenderScale);
    if (qFuzzyCompare(m_renderScale, boundedScale)) {
        return;
    }
    m_renderScale = boundedScale;
    Q_EMIT renderScaleChanged();
    renderCurrentFrame();
}

RlottieStickerItem::Status RlottieStickerItem::status() const
{
    return m_status;
}

void RlottieStickerItem::paint(QPainter *painter)
{
    if (m_frame.isNull()) {
        return;
    }
    painter->setRenderHint(QPainter::SmoothPixmapTransform, true);
    painter->drawImage(boundingRect(), m_frame);
}

void RlottieStickerItem::geometryChange(const QRectF &newGeometry, const QRectF &oldGeometry)
{
    QQuickPaintedItem::geometryChange(newGeometry, oldGeometry);
    if (newGeometry.size() != oldGeometry.size()) {
        renderCurrentFrame();
    }
}

void RlottieStickerItem::loadSource()
{
    m_playback.stop();
    m_animation.reset();
    m_renderedFrame = -1;
    clearFrame();

    const QString path = m_source.isLocalFile() ? m_source.toLocalFile() : m_source.toString(QUrl::PreferLocalFile);
    if (path.isEmpty()) {
        setStatus(Status::Null);
        return;
    }

    setStatus(Status::Loading);
    m_animation = rlottie::Animation::loadFromFile(path.toStdString());
    if (!m_animation) {
        setStatus(Status::Error);
        return;
    }

    m_currentFrame = static_cast<int>(m_animation->frameAtPos(0.0));
    m_renderedFrame = -1;
    m_playback.setDuration(std::max(1, static_cast<int>(std::ceil(m_animation->duration() * 1000.0))));

    renderCurrentFrame();
    setStatus(Status::Ready);
    updateAnimationState();
}

void RlottieStickerItem::setStatus(Status status)
{
    if (m_status == status) {
        return;
    }
    m_status = status;
    Q_EMIT statusChanged();
}

void RlottieStickerItem::updateAnimationState()
{
    if (!m_playing || m_status != Status::Ready || !m_animation) {
        m_playback.stop();
        return;
    }

    if (m_playback.state() != QAbstractAnimation::Running) {
        m_playback.start();
    }
}

void RlottieStickerItem::renderCurrentFrame()
{
    if (!m_animation || width() <= 0 || height() <= 0) {
        clearFrame();
        return;
    }

    if (m_currentFrame == m_renderedFrame && !m_frame.isNull()) {
        update();
        return;
    }

    const QSize size(std::max(1, static_cast<int>(std::ceil(width() * m_renderScale))),
                     std::max(1, static_cast<int>(std::ceil(height() * m_renderScale))));
    if (m_frame.size() != size) {
        m_frame = QImage(size, QImage::Format_ARGB32_Premultiplied);
    }
    m_frame.fill(Qt::transparent);

    rlottie::Surface surface(reinterpret_cast<uint32_t *>(m_frame.bits()),
                             static_cast<size_t>(m_frame.width()),
                             static_cast<size_t>(m_frame.height()),
                             static_cast<size_t>(m_frame.bytesPerLine()));
    m_animation->renderSync(static_cast<size_t>(m_currentFrame), surface, true);
    m_renderedFrame = m_currentFrame;
    update();
}

void RlottieStickerItem::clearFrame()
{
    m_frame = QImage();
    m_renderedFrame = -1;
    update();
}
