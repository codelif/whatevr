// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QQuickImageProvider>

/**
 * Serves the stills VideoPlaybackArbiter keeps, as `image://videoframe/<id>`.
 *
 * An image provider rather than a custom item because every consumer already
 * has an Image: the bubble's poster layer and the full-screen viewer's backdrop
 * both want "a picture for this message id", with the same fill modes, rounding
 * and fade the daemon's poster gets. Swapping the url is the whole change.
 *
 * Urls carry a `?rev=` query the arbiter bumps on each capture. Without it the
 * pipeline would answer a second look at the same message from its cache and
 * the still would never update.
 */
class VideoFrameImageProvider : public QQuickImageProvider
{
public:
    VideoFrameImageProvider();

    QImage requestImage(const QString &id, QSize *size, const QSize &requestedSize) override;
};
