#include "rlottiestickeritem.h"

#include <QQuickWindow>
#include <QSGSimpleTextureNode>
#include <QThread>

#include <algorithm>
#include <cmath>

#include <rlottie.h>

namespace {
constexpr qreal kMinimumRenderScale = 1.0;
constexpr qreal kMaximumRenderScale = 4.0;

// All Lottie parsing and rasterisation runs on one shared, low-priority thread.
// A handful of stickers are ever visible at once, so serialising their renders
// here keeps the GUI thread free without spawning a thread per sticker. The
// thread lives for the lifetime of the process.
QThread *lottieRenderThread()
{
    static QThread *thread = [] {
        auto *t = new QThread;
        t->setObjectName(QStringLiteral("lottie-render"));
        t->start(QThread::LowPriority);
        return t;
    }();
    return thread;
}
}

LottieRenderWorker::LottieRenderWorker(QObject *parent)
    : QObject(parent)
{
}

LottieRenderWorker::~LottieRenderWorker() = default;

void LottieRenderWorker::load(const QString &path, quint64 generation)
{
    m_animation.reset();
    if (path.isEmpty()) {
        Q_EMIT loaded(generation, false, 0, 0.0);
        return;
    }

    m_animation = rlottie::Animation::loadFromFile(path.toStdString());
    if (!m_animation) {
        Q_EMIT loaded(generation, false, 0, 0.0);
        return;
    }

    Q_EMIT loaded(generation,
                  true,
                  static_cast<int>(m_animation->totalFrame()),
                  m_animation->duration());
}

void LottieRenderWorker::render(int frame, QSize size, quint64 generation)
{
    if (!m_animation || size.isEmpty()) {
        return;
    }

    QImage image(size, QImage::Format_ARGB32_Premultiplied);
    image.fill(Qt::transparent);

    rlottie::Surface surface(reinterpret_cast<uint32_t *>(image.bits()),
                             static_cast<size_t>(size.width()),
                             static_cast<size_t>(size.height()),
                             static_cast<size_t>(image.bytesPerLine()));
    m_animation->renderSync(static_cast<size_t>(frame), surface, true);

    Q_EMIT rendered(generation, image, frame);
}

RlottieStickerItem::RlottieStickerItem(QQuickItem *parent)
    : QQuickItem(parent)
{
    setFlag(ItemHasContents, true);

    m_worker = new LottieRenderWorker;
    m_worker->moveToThread(lottieRenderThread());
    connect(m_worker, &LottieRenderWorker::loaded, this, &RlottieStickerItem::onLoaded);
    connect(m_worker, &LottieRenderWorker::rendered, this, &RlottieStickerItem::onRendered);

    m_playback.setStartValue(0.0);
    m_playback.setEndValue(1.0);
    m_playback.setLoopCount(-1);
    connect(&m_playback, &QVariantAnimation::valueChanged, this, [this](const QVariant &value) {
        requestFrame(frameForPosition(value.toDouble()));
    });
}

RlottieStickerItem::~RlottieStickerItem()
{
    m_playback.stop();
    // The worker lives on the render thread; hand its destruction to that thread.
    // Any queued render results aimed at this (now destroyed) item are dropped
    // automatically by Qt's event system.
    m_worker->deleteLater();
}

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
    // Re-rasterise the current frame at the new resolution.
    m_renderedFrame = -1;
    requestFrame(m_currentFrame);
}

RlottieStickerItem::Status RlottieStickerItem::status() const
{
    return m_status;
}

void RlottieStickerItem::loadSource()
{
    m_playback.stop();
    m_renderInFlight = false;
    m_pendingFrame = -1;
    m_currentFrame = 0;
    m_renderedFrame = -1;
    m_totalFrames = 0;

    // Bump the generation so any in-flight load/render results for the previous
    // source are recognised as stale and discarded when they arrive.
    const quint64 generation = ++m_generation;

    if (!m_frontImage.isNull()) {
        m_frontImage = QImage();
        m_textureDirty = true;
        update();
    }

    const QString path = m_source.isLocalFile() ? m_source.toLocalFile()
                                                 : m_source.toString(QUrl::PreferLocalFile);
    if (path.isEmpty()) {
        setStatus(Status::Null);
        return;
    }

    setStatus(Status::Loading);
    QMetaObject::invokeMethod(m_worker,
                              "load",
                              Qt::QueuedConnection,
                              Q_ARG(QString, path),
                              Q_ARG(quint64, generation));
}

void RlottieStickerItem::onLoaded(quint64 generation, bool ok, int totalFrames, double durationSeconds)
{
    if (generation != m_generation) {
        return;
    }

    if (!ok || totalFrames <= 0) {
        setStatus(Status::Error);
        return;
    }

    m_totalFrames = totalFrames;
    m_playback.setDuration(std::max(1, static_cast<int>(std::ceil(durationSeconds * 1000.0))));
    m_currentFrame = 0;
    m_renderedFrame = -1;
    setStatus(Status::Ready);

    // Render the first frame so a paused, in-view sticker still shows something,
    // then start playback if requested.
    requestFrame(0);
    updateAnimationState();
}

void RlottieStickerItem::onRendered(quint64 generation, const QImage &image, int frame)
{
    if (generation != m_generation) {
        // Stale frame from a previous source; a render slot freed up though.
        m_renderInFlight = false;
        return;
    }

    m_renderInFlight = false;
    m_frontImage = image;
    m_renderedFrame = frame;
    m_textureDirty = true;
    update();

    // A newer frame was requested while this one was rendering — chase it now,
    // dropping any intermediate frames that piled up.
    if (m_pendingFrame >= 0 && m_pendingFrame != frame) {
        const int next = m_pendingFrame;
        m_pendingFrame = -1;
        requestFrame(next);
    } else {
        m_pendingFrame = -1;
    }
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
    if (!m_playing || m_status != Status::Ready) {
        m_playback.stop();
        return;
    }

    if (m_playback.state() != QAbstractAnimation::Running) {
        m_playback.start();
    }
}

int RlottieStickerItem::frameForPosition(double position) const
{
    if (m_totalFrames <= 1) {
        return 0;
    }
    const int frame = static_cast<int>(std::lround(position * (m_totalFrames - 1)));
    return std::clamp(frame, 0, m_totalFrames - 1);
}

QSize RlottieStickerItem::targetPixelSize() const
{
    return QSize(std::max(1, static_cast<int>(std::ceil(width() * m_renderScale))),
                 std::max(1, static_cast<int>(std::ceil(height() * m_renderScale))));
}

void RlottieStickerItem::requestFrame(int frame)
{
    if (m_status != Status::Ready || width() <= 0 || height() <= 0) {
        return;
    }

    m_currentFrame = frame;
    if (frame == m_renderedFrame && !m_frontImage.isNull()) {
        return;
    }

    // Only one render may be outstanding per item, so the background thread is
    // never backed up; the latest requested frame wins once it frees up.
    if (m_renderInFlight) {
        m_pendingFrame = frame;
        return;
    }

    m_renderInFlight = true;
    QMetaObject::invokeMethod(m_worker,
                              "render",
                              Qt::QueuedConnection,
                              Q_ARG(int, frame),
                              Q_ARG(QSize, targetPixelSize()),
                              Q_ARG(quint64, m_generation));
}

void RlottieStickerItem::geometryChange(const QRectF &newGeometry, const QRectF &oldGeometry)
{
    QQuickItem::geometryChange(newGeometry, oldGeometry);
    if (newGeometry.size() != oldGeometry.size()) {
        m_renderedFrame = -1;
        requestFrame(m_currentFrame);
    }
}

QSGNode *RlottieStickerItem::updatePaintNode(QSGNode *oldNode, UpdatePaintNodeData *)
{
    auto *node = static_cast<QSGSimpleTextureNode *>(oldNode);

    if (m_frontImage.isNull()) {
        delete node;
        return nullptr;
    }

    if (!node) {
        node = new QSGSimpleTextureNode;
        node->setOwnsTexture(true);
        node->setFiltering(QSGTexture::Linear);
    }

    if (m_textureDirty || !node->texture()) {
        // setTexture deletes the previously owned texture for us.
        node->setTexture(window()->createTextureFromImage(m_frontImage,
                                                          QQuickWindow::TextureHasAlphaChannel));
        m_textureDirty = false;
    }

    node->setRect(boundingRect());
    return node;
}
