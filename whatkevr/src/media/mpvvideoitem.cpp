// SPDX-License-Identifier: BSD-3-Clause
#include "mpvvideoitem.h"

#include "mpvcore.h"
#include "mpvpool.h"

#include <algorithm>
#include <memory>

#include <QGuiApplication>
#include <QOpenGLContext>
#include <QOpenGLFramebufferObject>
#include <QPointer>
#include <QQuickWindow>

#if defined(Q_OS_UNIX) && !defined(Q_OS_DARWIN)
#include <QtGui/qguiapplication_platform.h>
#endif

#include <mpv/client.h>
#include <mpv/render_gl.h>

namespace
{

void *getProcAddress(void *ctx, const char *name)
{
    Q_UNUSED(ctx)
    QOpenGLContext *context = QOpenGLContext::currentContext();
    if (!context) {
        return nullptr;
    }
    return reinterpret_cast<void *>(context->getProcAddress(QByteArray(name)));
}

/// Bounded retries for a render context that could not be created because the
/// previous owner of this core has not torn its own down yet. The pool frees a
/// core on the GUI thread while the render thread is a frame behind, so the
/// next item to claim it can arrive first; libmpv allows exactly one context
/// per handle and refuses the second. One or two frames is all it ever takes,
/// and the cap is what stops a genuinely broken driver from spinning.
constexpr int renderContextRetryLimit = 30;

/**
 * The mpv-thread frame callback.
 *
 * Held by value in the render context, so it outlives the item it points at
 * whenever a bubble is scrolled away mid-frame. QPointer is what makes the
 * queued update safe there; MpvQt's renderer holds its item the same way.
 */
struct FrameCallbackTarget {
    QPointer<MpvVideoItem> item;
};

void onMpvFrame(void *ctx)
{
    auto *target = static_cast<FrameCallbackTarget *>(ctx);
    if (!target) {
        return;
    }
    // On an mpv thread: queue, never touch the item from here.
    if (MpvVideoItem *item = target->item.data()) {
        QMetaObject::invokeMethod(item, "update", Qt::QueuedConnection);
    }
}

/**
 * MpvRenderer lives on the scene graph's render thread. It creates mpv's render
 * context with that thread's OpenGL context current and asks mpv to draw into
 * the framebuffer Qt Quick allocated for the item.
 *
 * The libmpv contract here is subtle enough that it is worth naming the
 * reference: KDE's MpvQt (invent.kde.org/libraries/mpvqt, the wrapper Haruna
 * uses), whose `mpvrenderer.cpp` this file was checked against. Four things
 * below come straight from it, all of them places where we had it wrong.
 */
class MpvRenderer : public QQuickFramebufferObject::Renderer
{
public:
    explicit MpvRenderer(MpvVideoItem *item)
        : m_item(item)
    {
    }

    ~MpvRenderer() override
    {
        releaseContext();
    }

    void render() override
    {
        if (!m_renderContext) {
            return;
        }

        QOpenGLFramebufferObject *target = framebufferObject();
        if (!target) {
            return;
        }

        mpv_opengl_fbo fbo{
            static_cast<int>(target->handle()),
            target->width(),
            target->height(),
            0,
        };
        // Zero, not one. A QQuickFramebufferObject's texture is sampled in the
        // same orientation mpv renders in, so asking mpv to flip as well is one
        // flip too many and every frame comes out upside down. This is the pair
        // MpvQt renders correctly with: flip_y 0 and no setMirrorVertically.
        int flipY = 0;
        mpv_render_param params[] = {
            {MPV_RENDER_PARAM_OPENGL_FBO, &fbo},
            {MPV_RENDER_PARAM_FLIP_Y, &flipY},
            {MPV_RENDER_PARAM_INVALID, nullptr},
        };
        // mpv issues raw GL commands, which the scene graph has to be told
        // about so it does not assume its own state survived.
        QQuickWindow *window = m_item ? m_item->window() : nullptr;
        if (window) {
            window->beginExternalCommands();
        }
        mpv_render_context_render(m_renderContext, params);
        if (window) {
            window->endExternalCommands();
        }
    }

    void synchronize(QQuickFramebufferObject *item) override
    {
        auto *videoItem = static_cast<MpvVideoItem *>(item);
        m_item = videoItem;
        MpvCore *core = videoItem->core();

        if (!core || !core->isValid()) {
            // The item lost its core (or never had one): tear the render
            // context down so the decoder can be reused elsewhere.
            releaseContext();
            m_retries = 0;
            return;
        }
        if (m_renderContext && m_handle && m_handle->handle == core->handle()) {
            return;
        }
        releaseContext();

        createContext(core, videoItem);
    }

private:
    void createContext(MpvCore *core, MpvVideoItem *videoItem)
    {
        mpv_opengl_init_params glInit{getProcAddress, nullptr};

        // The native display, which is how mpv reaches the platform's video
        // acceleration (VA-API on Wayland, VDPAU/VA-API on X11). Without it
        // hardware decoding cannot bind to the display at all, and mpv's
        // hwdec probe walks past the working driver into whatever else is on
        // its list; on this box that meant a "Cannot load libcuda.so.1" on a
        // machine with no CUDA anywhere. MpvQt passes it; we did not.
        mpv_render_param display{MPV_RENDER_PARAM_INVALID, nullptr};
#if defined(Q_OS_UNIX) && !defined(Q_OS_DARWIN)
        const QString platform = QGuiApplication::platformName();
        if (platform == QLatin1String("wayland")) {
            if (auto *native = qGuiApp->nativeInterface<QNativeInterface::QWaylandApplication>()) {
                display.type = MPV_RENDER_PARAM_WL_DISPLAY;
                display.data = native->display();
            }
        } else if (platform == QLatin1String("xcb")) {
            if (auto *native = qGuiApp->nativeInterface<QNativeInterface::QX11Application>()) {
                display.type = MPV_RENDER_PARAM_X11_DISPLAY;
                display.data = native->display();
            }
        }
#endif

        // No MPV_RENDER_PARAM_ADVANCED_CONTROL. It opts into driving
        // mpv_render_context_update() ourselves, which we never did; setting it
        // without honouring it is a misuse, and MpvQt does not set it either.
        mpv_render_param params[] = {
            {MPV_RENDER_PARAM_API_TYPE, const_cast<char *>(MPV_RENDER_API_TYPE_OPENGL)},
            {MPV_RENDER_PARAM_OPENGL_INIT_PARAMS, &glInit},
            display,
            {MPV_RENDER_PARAM_INVALID, nullptr},
        };

        auto owner = core->handleOwner();
        if (!owner) {
            return;
        }
        if (const int status = mpv_render_context_create(&m_renderContext, owner->handle, params); status < 0) {
            m_renderContext = nullptr;
            if (m_retries < renderContextRetryLimit) {
                ++m_retries;
                // Almost always the handoff race above, which the next frame
                // resolves. Ask for that frame rather than leaving the bubble
                // permanently blank with a decoder attached.
                QMetaObject::invokeMethod(videoItem, "update", Qt::QueuedConnection);
                return;
            }
            qWarning("whatkevr: mpv render context unavailable: %s", mpv_error_string(status));
            return;
        }
        m_retries = 0;
        // Holding the owner, not the raw handle: the core can be destroyed on
        // the GUI thread at any point, and the handle has to outlive the
        // context that was built from it.
        m_handle = std::move(owner);

        // Only now may a file be loaded into this core. Queued, because we are
        // on the render thread with the GUI thread blocked.
        QMetaObject::invokeMethod(videoItem, "notifyRendererReady", Qt::QueuedConnection);

        // Every new frame becomes an item update, which is what drives render()
        // above.
        m_frameTarget = std::make_unique<FrameCallbackTarget>();
        m_frameTarget->item = videoItem;
        mpv_render_context_set_update_callback(m_renderContext, onMpvFrame, m_frameTarget.get());
    }

    void releaseContext()
    {
        if (m_renderContext) {
            // Cleared before the free, so a frame arriving mid-teardown cannot
            // reach a callback target that is about to go.
            mpv_render_context_set_update_callback(m_renderContext, nullptr, nullptr);
            mpv_render_context_free(m_renderContext);
            m_renderContext = nullptr;
        }
        m_frameTarget.reset();
        m_handle.reset();
    }

    QPointer<MpvVideoItem> m_item;
    mpv_render_context *m_renderContext = nullptr;
    std::shared_ptr<MpvHandleOwner> m_handle;
    std::unique_ptr<FrameCallbackTarget> m_frameTarget;
    int m_retries = 0;
};

}

MpvVideoItem::MpvVideoItem(QQuickItem *parent)
    : QQuickFramebufferObject(parent)
{
    // Left at Qt's default, and stated rather than assumed: it is one half of a
    // pair with MPV_RENDER_PARAM_FLIP_Y in the renderer, and turning on either
    // one alone puts every frame upside down. See the note there.
    setMirrorVertically(false);
}

MpvVideoItem::~MpvVideoItem()
{
    detachCore();
}

QQuickFramebufferObject::Renderer *MpvVideoItem::createRenderer() const
{
    return new MpvRenderer(const_cast<MpvVideoItem *>(this));
}

void MpvVideoItem::setSource(const QUrl &source)
{
    if (m_source == source) {
        return;
    }
    m_source = source;
    Q_EMIT sourceChanged();

    if (m_source.isEmpty()) {
        detachCore();
        return;
    }
    if (m_core) {
        m_loadedIntoCore = false;
        if (m_rendererReady) {
            m_core->load(m_source, m_wantPlaying, m_startPosition);
            m_loadedIntoCore = true;
        }
    } else if (m_wantPlaying) {
        attachCore();
    }
}

void MpvVideoItem::setStartPosition(double seconds)
{
    if (seconds < 0.0) {
        seconds = 0.0;
    }
    if (qFuzzyCompare(m_startPosition, seconds)) {
        return;
    }
    m_startPosition = seconds;
    Q_EMIT startPositionChanged();
}

void MpvVideoItem::setMessageId(const QString &messageId)
{
    if (m_messageId == messageId) {
        return;
    }
    m_messageId = messageId;
    Q_EMIT messageIdChanged();
}

bool MpvVideoItem::playing() const
{
    return m_core && m_core->playing();
}

void MpvVideoItem::setPlaying(bool playing)
{
    m_wantPlaying = playing;
    if (playing) {
        if (!m_core) {
            attachCore();
        } else {
            m_core->play();
        }
        return;
    }
    if (m_core) {
        m_core->pause();
    }
}

void MpvVideoItem::setMuted(bool muted)
{
    if (m_muted == muted) {
        return;
    }
    m_muted = muted;
    Q_EMIT mutedChanged();
    if (m_core) {
        m_core->setMuted(muted);
    }
}

void MpvVideoItem::setLoop(bool loop)
{
    if (m_loop == loop) {
        return;
    }
    m_loop = loop;
    Q_EMIT loopChanged();
    if (m_core) {
        m_core->setLooping(loop);
    }
}

double MpvVideoItem::position() const
{
    return m_core ? m_core->position() : 0.0;
}

double MpvVideoItem::duration() const
{
    return m_core ? m_core->duration() : 0.0;
}

int MpvVideoItem::videoWidth() const
{
    return m_core ? m_core->videoSize().width() : 0;
}

int MpvVideoItem::videoHeight() const
{
    return m_core ? m_core->videoSize().height() : 0;
}

void MpvVideoItem::seek(double seconds)
{
    if (m_core) {
        m_core->seek(seconds);
    }
}

void MpvVideoItem::togglePlaying()
{
    setPlaying(!playing());
}

void MpvVideoItem::release()
{
    m_wantPlaying = false;
    detachCore();
}

void MpvVideoItem::attachCore()
{
    if (m_core || m_source.isEmpty()) {
        return;
    }
    m_core = MpvPool::instance()->acquire(this);
    if (!m_core) {
        // Only reachable if the pool's safety ceiling is hit, which the arbiter
        // above should have made impossible. The bubble keeps its thumbnail.
        return;
    }

    connect(m_core, &MpvCore::playingChanged, this, &MpvVideoItem::playingChanged);
    connect(m_core, &MpvCore::positionChanged, this, &MpvVideoItem::positionChanged);
    connect(m_core, &MpvCore::durationChanged, this, &MpvVideoItem::durationChanged);
    connect(m_core, &MpvCore::videoSizeChanged, this, &MpvVideoItem::videoSizeChanged);
    connect(m_core, &MpvCore::endOfFile, this, &MpvVideoItem::endOfFile);

    applyStateToCore();
    Q_EMIT activeChanged();
    update();
}

void MpvVideoItem::detachCore()
{
    if (!m_core) {
        return;
    }
    disconnect(m_core, nullptr, this, nullptr);
    MpvPool::instance()->release(this, m_core);
    m_core = nullptr;
    m_loadedIntoCore = false;
    Q_EMIT activeChanged();
    Q_EMIT playingChanged();
    // Drop the last frame so a released bubble does not keep a stale still.
    update();
}

void MpvVideoItem::applyStateToCore()
{
    if (!m_core) {
        return;
    }
    m_core->setMuted(m_muted);
    m_core->setLooping(m_loop);
    m_core->setSpeed(m_speed);
    m_core->setVolume(m_volume);
    if (!m_rendererReady) {
        // Held until the render context exists; loading now would give us a
        // file with its video track switched off. notifyRendererReady() picks
        // this up. The item is already visible by construction (a bubble only
        // wants a core when it wants to play), so the wait is a frame or two.
        return;
    }
    m_core->load(m_source, m_wantPlaying, m_startPosition);
    m_loadedIntoCore = true;
}

void MpvVideoItem::notifyRendererReady()
{
    m_rendererReady = true;
    if (m_core && !m_loadedIntoCore) {
        m_core->load(m_source, m_wantPlaying, m_startPosition);
        m_loadedIntoCore = true;
    }
}

void MpvVideoItem::setSpeed(double speed)
{
    if (speed <= 0.0 || qFuzzyCompare(m_speed, speed)) {
        return;
    }
    m_speed = speed;
    if (m_core) {
        m_core->setSpeed(m_speed);
    }
    Q_EMIT speedChanged();
}

void MpvVideoItem::setVolume(double volume)
{
    volume = std::clamp(volume, 0.0, 100.0);
    if (qFuzzyCompare(m_volume, volume)) {
        return;
    }
    m_volume = volume;
    if (m_core) {
        m_core->setVolume(m_volume);
    }
    Q_EMIT volumeChanged();
}

