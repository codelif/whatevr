// SPDX-License-Identifier: BSD-3-Clause
#include "mpvvideoitem.h"

#include "mpvcore.h"

#include <memory>

#include <QGuiApplication>
#include <QOpenGLContext>
#include <QOpenGLFramebufferObject>
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

/// Bounded retries for a render context that could not be created because a
/// previous one on the same handle has not been torn down yet. Freeing happens
/// on the render thread a frame behind the GUI thread that asked for it, and
/// libmpv allows exactly one context per handle and refuses the second. One or
/// two frames is all it ever takes, and the cap is what stops a genuinely
/// broken driver from spinning.
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
 * uses), whose `mpvrenderer.cpp` this file was checked against. Several things
 * below come straight from it, all of them places we had it wrong.
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

        // Qt renders this item whenever the window renders: on every scroll
        // frame, every hover, every animation elsewhere in the app. Drawing
        // the same video frame again each time is a full GPU pass for nothing,
        // so ask mpv whether there is anything new and leave the last frame in
        // the framebuffer when there is not. A framebuffer Qt just replaced
        // has nothing in it to leave, so that one is always drawn.
        const bool targetIsNew = target->handle() != m_lastTarget;
        const bool hasNewFrame = (mpv_render_context_update(m_renderContext) & MPV_RENDER_UPDATE_FRAME) != 0;
        if (!hasNewFrame && !targetIsNew) {
            return;
        }
        m_lastTarget = target->handle();

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
        // Advanced control expects to be told the frame reached the screen; it
        // is what mpv times the next one against.
        mpv_render_context_report_swap(m_renderContext);
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
        // hardware decoding cannot bind to the display at all, and mpv's hwdec
        // probe walks past the working driver into whatever else is on its
        // list; on this box that meant a "Cannot load libcuda.so.1" on a
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

        // Advanced control, which is what lets render() above skip a pass when
        // there is no new frame. It is a contract: mpv may now call the update
        // callback without a frame behind it, and the answer has to come from
        // mpv_render_context_update(). Honouring it is also what enables mpv's
        // direct rendering, saving a copy per frame.
        int advancedControl = 1;
        mpv_render_param params[] = {
            {MPV_RENDER_PARAM_API_TYPE, const_cast<char *>(MPV_RENDER_API_TYPE_OPENGL)},
            {MPV_RENDER_PARAM_OPENGL_INIT_PARAMS, &glInit},
            {MPV_RENDER_PARAM_ADVANCED_CONTROL, &advancedControl},
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
                // Almost always the teardown race above, which the next frame
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

        // Before anything else: under advanced control the core can block
        // waiting to be asked for a frame, and it is this callback that starts
        // the asking. Every update becomes an item update, which is what drives
        // render() above.
        m_frameTarget = std::make_unique<FrameCallbackTarget>();
        m_frameTarget->item = videoItem;
        mpv_render_context_set_update_callback(m_renderContext, onMpvFrame, m_frameTarget.get());

        // Only now may a file be loaded into this core. Queued, because we are
        // on the render thread with the GUI thread blocked.
        QMetaObject::invokeMethod(videoItem, "notifyRendererReady", Qt::QueuedConnection);
    }

    void releaseContext()
    {
        if (m_renderContext) {
            // Cleared before the free, so a frame arriving mid-teardown cannot
            // reach a callback target that is about to go.
            mpv_render_context_set_update_callback(m_renderContext, nullptr, nullptr);
            mpv_render_context_free(m_renderContext);
            m_renderContext = nullptr;
            if (m_item) {
                QMetaObject::invokeMethod(m_item.data(), "notifyRendererGone", Qt::QueuedConnection);
            }
        }
        m_frameTarget.reset();
        m_handle.reset();
    }

    QPointer<MpvVideoItem> m_item;
    mpv_render_context *m_renderContext = nullptr;
    /// The framebuffer the last frame went into. Qt hands out a new one on
    /// resize, and a new one is empty until something is drawn into it.
    uint m_lastTarget = 0;
    std::shared_ptr<MpvHandleOwner> m_handle;
    std::unique_ptr<FrameCallbackTarget> m_frameTarget;
    int m_retries = 0;
};

}

MpvVideoItem::MpvVideoItem(MpvCore *core, QQuickItem *parent)
    : QQuickFramebufferObject(parent)
    , m_core(core)
{
    // Left at Qt's default, and stated rather than assumed: it is one half of a
    // pair with MPV_RENDER_PARAM_FLIP_Y in the renderer, and turning on either
    // one alone puts every frame upside down. See the note there.
    setMirrorVertically(false);
}

QQuickFramebufferObject::Renderer *MpvVideoItem::createRenderer() const
{
    return new MpvRenderer(const_cast<MpvVideoItem *>(this));
}

void MpvVideoItem::notifyRendererReady()
{
    if (m_rendererReady) {
        return;
    }
    m_rendererReady = true;
    Q_EMIT rendererReadyChanged();
}

void MpvVideoItem::notifyRendererGone()
{
    if (!m_rendererReady) {
        return;
    }
    m_rendererReady = false;
    Q_EMIT rendererReadyChanged();
}
