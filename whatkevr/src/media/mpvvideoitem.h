// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QPointer>
#include <QQuickFramebufferObject>

class MpvCore;

/**
 * MpvVideoItem draws one libmpv core into the Qt Quick scene graph.
 *
 * mpv's render API produces OpenGL, so this is a QQuickFramebufferObject and
 * the app pins the OpenGL RHI backend at startup (see main.cpp). Rendering into
 * the item's own framebuffer rather than the window's keeps the UI frame rate
 * independent of the video's.
 *
 * One of these belongs to each PlaybackSession, not to a view: the session
 * moves the item between the inline bubble and the full-screen viewer by
 * reparenting it, which is what makes that handoff free. Tearing the item down
 * instead would free mpv's render context, and freeing a render context takes
 * the video track of the playing file with it.
 */
class MpvVideoItem : public QQuickFramebufferObject
{
    Q_OBJECT

public:
    explicit MpvVideoItem(MpvCore *core, QQuickItem *parent = nullptr);

    Renderer *createRenderer() const override;

    [[nodiscard]] MpvCore *core() const
    {
        return m_core;
    }

    /**
     * Whether mpv's render context exists.
     *
     * Nothing may be loaded before it does. mpv decides whether a file has
     * video when it opens it, and with vo=libmpv and no render context yet it
     * logs "No render context set", reports "Video: no video", and plays the
     * file through to eof with the video track disabled. A context arriving a
     * frame later does not undo that.
     */
    [[nodiscard]] bool rendererReady() const
    {
        return m_rendererReady;
    }

    /// Called by the renderer, on the GUI thread, once its context exists.
    Q_INVOKABLE void notifyRendererReady();
    /// Called by the renderer when it gives its context up, which happens when
    /// the item leaves the scene.
    Q_INVOKABLE void notifyRendererGone();

Q_SIGNALS:
    /// The render context appeared. A session waiting to open a file, or
    /// holding one whose video track died with the previous context, acts here.
    void rendererReadyChanged();

private:
    QPointer<MpvCore> m_core;
    bool m_rendererReady = false;
};
