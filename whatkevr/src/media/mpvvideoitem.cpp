// SPDX-License-Identifier: BSD-3-Clause
#include "mpvvideoitem.h"

#include "mpvcore.h"
#include "mpvpool.h"

#include <algorithm>

#include <QOpenGLContext>
#include <QOpenGLFramebufferObject>
#include <QQuickWindow>

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

/**
 * MpvRenderer lives on the scene graph's render thread. It creates mpv's render
 * context lazily, once, with that thread's OpenGL context current, and asks mpv
 * to draw into the framebuffer Qt Quick allocated for the item.
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
        if (m_renderContext) {
            mpv_render_context_free(m_renderContext);
            m_renderContext = nullptr;
        }
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
        // Qt Quick's framebuffers are bottom-up relative to mpv's expectation.
        int flipY = 1;
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
        MpvCore *core = videoItem->core();

        if (!core || !core->isValid()) {
            // The item lost its core (or never had one): tear the render
            // context down so the decoder can be reused elsewhere.
            if (m_renderContext) {
                mpv_render_context_free(m_renderContext);
                m_renderContext = nullptr;
                m_boundHandle = nullptr;
            }
            return;
        }
        if (m_renderContext && m_boundHandle == core->handle()) {
            return;
        }
        if (m_renderContext) {
            mpv_render_context_free(m_renderContext);
            m_renderContext = nullptr;
        }

        mpv_opengl_init_params glInit{getProcAddress, nullptr};
        int advancedControl = 1;
        mpv_render_param params[] = {
            {MPV_RENDER_PARAM_API_TYPE, const_cast<char *>(MPV_RENDER_API_TYPE_OPENGL)},
            {MPV_RENDER_PARAM_OPENGL_INIT_PARAMS, &glInit},
            {MPV_RENDER_PARAM_ADVANCED_CONTROL, &advancedControl},
            {MPV_RENDER_PARAM_INVALID, nullptr},
        };
        if (const int status = mpv_render_context_create(&m_renderContext, core->handle(), params); status < 0) {
            qWarning("whatkevr: mpv render context unavailable: %s", mpv_error_string(status));
            m_renderContext = nullptr;
            return;
        }
        m_boundHandle = core->handle();

        // Every new frame becomes an item update, which is what drives render()
        // above. The callback fires on an mpv thread, so it only queues.
        mpv_render_context_set_update_callback(
            m_renderContext,
            [](void *ctx) {
                auto *self = static_cast<MpvVideoItem *>(ctx);
                QMetaObject::invokeMethod(self, "update", Qt::QueuedConnection);
            },
            videoItem);
    }

private:
    MpvVideoItem *m_item = nullptr;
    mpv_render_context *m_renderContext = nullptr;
    mpv_handle *m_boundHandle = nullptr;
};

}

MpvVideoItem::MpvVideoItem(QQuickItem *parent)
    : QQuickFramebufferObject(parent)
{
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
        m_core->load(m_source, m_wantPlaying);
    } else if (m_wantPlaying) {
        attachCore();
    }
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
        // Every core is busy; the bubble keeps its thumbnail. This is the
        // concurrency cap doing its job, not an error.
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
    m_core->load(m_source, m_wantPlaying);
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

void MpvVideoItem::setReserved(bool reserved)
{
    if (m_reserved == reserved) {
        return;
    }
    m_reserved = reserved;
    // The pool keeps exactly one reserved item, so claiming is enough; the
    // viewer is the only thing that ever asks.
    MpvPool::instance()->setReservedItem(m_reserved ? this : nullptr);
    Q_EMIT reservedChanged();
}

