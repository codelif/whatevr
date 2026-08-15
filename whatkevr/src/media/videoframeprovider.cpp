// SPDX-License-Identifier: BSD-3-Clause
#include "videoframeprovider.h"

#include <QUrl>

#include "videoarbiter.h"

VideoFrameImageProvider::VideoFrameImageProvider()
    : QQuickImageProvider(QQuickImageProvider::Image)
{
}

QImage VideoFrameImageProvider::requestImage(const QString &id, QSize *size, const QSize &requestedSize)
{
    // Everything from the first '?' is the cache-busting revision, which is not
    // part of the message id. Qt hands the query through untouched.
    QString messageId = id;
    const qsizetype query = messageId.indexOf(QLatin1Char('?'));
    if (query >= 0) {
        messageId.truncate(query);
    }
    // Callers build the url with encodeURIComponent, so an id carrying '/',
    // '+' or '=' arrives percent-escaped and would miss the store unescaped.
    messageId = QUrl::fromPercentEncoding(messageId.toUtf8());

    QImage image = VideoPlaybackArbiter::instance()->frameImage(messageId);
    if (image.isNull()) {
        // A miss is ordinary: the caller asks for a still it may not have
        // (a clip never played, or one whose still has been evicted). Reporting
        // an empty size makes the Image status Error, which callers already
        // treat as "no still, use the poster".
        if (size) {
            *size = QSize();
        }
        return {};
    }

    if (size) {
        *size = image.size();
    }
    if (requestedSize.isValid() && !requestedSize.isEmpty()) {
        return image.scaled(requestedSize, Qt::KeepAspectRatio, Qt::SmoothTransformation);
    }
    return image;
}
