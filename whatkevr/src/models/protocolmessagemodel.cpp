#include "protocolmessagemodel.h"

#include <QDateTime>
#include <QGuiApplication>
#include <QLocale>
#include <QSet>
#include <QTextBoundaryFinder>
#include <QTimeZone>

#include <KLocalizedString>

#include <algorithm>
#include <cmath>

namespace
{
constexpr qint64 kSenderGroupGapSeconds = 5LL * 60;
constexpr int kCollapsedTextMaxGraphemes = 900;
constexpr int kCollapsedTextMaxLines = 12;

int graphemeBoundaryAfter(const QString &text, int maxGraphemes)
{
    QTextBoundaryFinder finder(QTextBoundaryFinder::Grapheme, text);
    int boundary = 0;
    for (int count = 0; count < maxGraphemes; ++count) {
        const int next = finder.toNextBoundary();
        if (next < 0) {
            return text.size();
        }
        boundary = next;
    }
    return boundary;
}

int lineBoundaryAfter(const QString &text, int maxLines)
{
    int lines = 1;
    for (int i = 0; i < text.size(); ++i) {
        if (text.at(i) == QLatin1Char('\n') && ++lines > maxLines) {
            return i;
        }
    }
    return text.size();
}

QString collapsedMessageText(const QString &text)
{
    const int lineBoundary = lineBoundaryAfter(text, kCollapsedTextMaxLines);
    if (text.size() <= kCollapsedTextMaxGraphemes && lineBoundary >= text.size()) {
        return text;
    }

    int boundary = lineBoundary;
    if (text.size() > kCollapsedTextMaxGraphemes) {
        boundary = std::min(boundary, graphemeBoundaryAfter(text, kCollapsedTextMaxGraphemes));
    }
    if (boundary >= text.size()) {
        return text;
    }
    QString preview = text.left(boundary).trimmed();
    if (preview.isEmpty()) {
        preview = text.left(boundary);
    }
    return preview + QStringLiteral("...");
}

std::pair<qreal, qreal> measureLineWidths(const QString &text, const QFontMetricsF &metrics)
{
    qreal widest = 0;
    qreal last = 0;
    for (const auto line : QStringView(text).split(QLatin1Char('\n'))) {
        last = metrics.horizontalAdvance(line.toString());
        widest = std::max(widest, last);
    }
    return {std::ceil(widest) + 1, std::ceil(last) + 1};
}
} // namespace

ProtocolMessageModel::ProtocolMessageModel(whatevr::proto::CollectionViewModel *source, QObject *parent)
    : QAbstractListModel(parent)
    , m_source(source)
    , m_bodyFont(QGuiApplication::font())
    , m_bodyMetrics(m_bodyFont)
{
    Q_ASSERT(m_source);

    // Every structural change invalidates the decoded-row cache: it is keyed by
    // row index, which any insert/remove/move renumbers.
    connect(m_source, &QAbstractItemModel::modelAboutToBeReset, this, [this] {
        beginResetModel();
    });
    connect(m_source, &QAbstractItemModel::modelReset, this, [this] {
        m_textById.clear();
        invalidateRowCache();
        endResetModel();
    });
    connect(m_source, &QAbstractItemModel::rowsAboutToBeInserted, this,
            [this](const QModelIndex &, int first, int last) {
                invalidateRowCache();
                beginInsertRows(QModelIndex(), first, last);
            });
    connect(m_source, &QAbstractItemModel::rowsInserted, this,
            [this](const QModelIndex &, int first, int last) {
                endInsertRows();
                // The rows either side of the run: an insert can only change
                // whether they start/end a sender run or carry a separator.
                emitNeighbourRolesChanged(first - 1, first - 1);
                emitNeighbourRolesChanged(last + 1, last + 1);
            });
    connect(m_source, &QAbstractItemModel::rowsAboutToBeRemoved, this,
            [this](const QModelIndex &, int first, int last) {
                invalidateRows(first, last);
                invalidateRowCache();
                beginRemoveRows(QModelIndex(), first, last);
            });
    connect(m_source, &QAbstractItemModel::rowsRemoved, this,
            [this](const QModelIndex &, int first, int) {
                endRemoveRows();
                emitNeighbourRolesChanged(first - 1, first);
            });
    connect(m_source, &QAbstractItemModel::rowsAboutToBeMoved, this,
            [this](const QModelIndex &, int first, int last, const QModelIndex &, int destination) {
                invalidateRowCache();
                beginMoveRows(QModelIndex(), first, last, QModelIndex(), destination);
            });
    connect(m_source, &QAbstractItemModel::rowsMoved, this,
            [this](const QModelIndex &, int first, int last, const QModelIndex &, int destination) {
                endMoveRows();
                const int landing = destination > first ? destination - (last - first + 1) : destination;
                emitNeighbourRolesChanged(std::min(first, landing) - 1, std::max(last, landing) + 1);
            });
    connect(m_source, &QAbstractItemModel::dataChanged, this,
            [this](const QModelIndex &topLeft, const QModelIndex &bottomRight) {
                invalidateRows(topLeft.row(), bottomRight.row());
                invalidateRowCache();
                // The changed rows themselves may have changed in any way; only
                // their neighbours are limited to the grouping roles.
                emitAllRolesChanged(topLeft.row(), bottomRight.row());
                emitNeighbourRolesChanged(topLeft.row() - 1, topLeft.row() - 1);
                emitNeighbourRolesChanged(bottomRight.row() + 1, bottomRight.row() + 1);
            });
}

int ProtocolMessageModel::rowCount(const QModelIndex &parent) const
{
    return parent.isValid() || !m_source ? 0 : m_source->rowCount();
}

void ProtocolMessageModel::setTransfersSource(whatevr::proto::CollectionViewModel *transfers)
{
    if (m_transfers == transfers) {
        return;
    }
    if (m_transfers) {
        disconnect(m_transfers, nullptr, this, nullptr);
    }
    m_transfers = transfers;
    if (m_transfers) {
        // `transfers` holds only what is downloading right now — usually nothing,
        // at most a handful of rows — and the daemon throttles progress to ~7/s
        // per transfer, so any change re-reads the two download roles across the
        // timeline instead of mapping transfer rows back to timeline rows. A
        // dataChanged only wakes the delegates actually on screen.
        const auto invalidate = [this] { invalidateTransferRoles(); };
        connect(m_transfers, &QAbstractItemModel::rowsInserted, this, invalidate);
        connect(m_transfers, &QAbstractItemModel::rowsRemoved, this, invalidate);
        connect(m_transfers, &QAbstractItemModel::dataChanged, this, invalidate);
        connect(m_transfers, &QAbstractItemModel::modelReset, this, invalidate);
        // The source is held by pointer, not owned: if it outlives its owner the
        // rows just stop reporting downloads.
        connect(m_transfers, &QObject::destroyed, this, [this] { m_transfers = nullptr; });
    }
    invalidateTransferRoles();
}

QVariantMap ProtocolMessageModel::transfer(const QVariantMap &item) const
{
    if (!m_transfers) {
        return {};
    }
    // The `transfers` view is keyed by message id (PROTOCOL.md), so the join is
    // a plain lookup — no frontend index to keep in sync. Only inbound rows
    // count: these roles are the bubble's *download* state, and the view's
    // `direction` is what tells the two apart.
    const QVariantMap active = m_transfers->itemById(item.value(QStringLiteral("id")).toString());
    if (active.value(QStringLiteral("direction")).toString() != QLatin1String("download")) {
        return {};
    }
    return active;
}

void ProtocolMessageModel::invalidateTransferRoles()
{
    // The transfers view holds only what is downloading right now — usually
    // nothing, at most a handful of rows — so refresh exactly those rows
    // instead of the whole timeline. Rows that were transferring last time are
    // included too, so one that just finished loses its spinner.
    QStringList current;
    if (m_transfers) {
        const int transferRows = m_transfers->rowCount();
        current.reserve(transferRows);
        for (int row = 0; row < transferRows; ++row) {
            const QString id = m_transfers->data(m_transfers->index(row),
                                                 whatevr::proto::CollectionViewModel::IdRole).toString();
            if (!id.isEmpty()) {
                current.append(id);
            }
        }
    }

    QStringList affected = current;
    for (const QString &id : std::as_const(m_transferRowIds)) {
        if (!affected.contains(id)) {
            affected.append(id);
        }
    }
    m_transferRowIds = std::move(current);

    for (const QString &id : std::as_const(affected)) {
        const int row = indexOf(id);
        if (row >= 0) {
            // Progress only. Whether a fetch is in flight comes from the message
            // row itself and arrives with its own dataChanged.
            Q_EMIT dataChanged(index(row, 0), index(row, 0), {MediaDownloadProgressRole});
        }
    }
}

QVariantMap ProtocolMessageModel::wireItem(int row) const
{
    if (!m_source || row < 0 || row >= m_source->rowCount()) {
        return {};
    }
    return m_source->data(m_source->index(row), whatevr::proto::CollectionViewModel::ItemRole).toMap();
}

void ProtocolMessageModel::invalidateRowCache() const
{
    m_rowCache = RowCache{};
}

const ProtocolMessageModel::RowCache &ProtocolMessageModel::rowCache(int row) const
{
    if (m_rowCache.row != row) {
        m_rowCache = RowCache{};
        m_rowCache.row = row;
        m_rowCache.item = wireItem(row);
    }
    return m_rowCache;
}

const QVariantMap &ProtocolMessageModel::cachedSender(const RowCache &cache) const
{
    if (!cache.senderLoaded) {
        m_rowCache.sender = sender(cache.item);
        m_rowCache.senderLoaded = true;
    }
    return m_rowCache.sender;
}

const QVariantMap &ProtocolMessageModel::cachedMedia(const RowCache &cache) const
{
    if (!cache.mediaLoaded) {
        m_rowCache.media = media(cache.item);
        m_rowCache.mediaLoaded = true;
    }
    return m_rowCache.media;
}

const QVariantMap &ProtocolMessageModel::cachedReply(const RowCache &cache) const
{
    if (!cache.replyLoaded) {
        m_rowCache.reply = reply(cache.item);
        m_rowCache.replyLoaded = true;
    }
    return m_rowCache.reply;
}

QVariant ProtocolMessageModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= rowCount()) {
        return {};
    }

    // One decode per row, shared across the ~45 role reads a delegate makes;
    // the nested maps resolve only for the roles that actually want them.
    const RowCache &cache = rowCache(index.row());
    const QVariantMap &item = cache.item;
    const auto senderData = [&]() -> const QVariantMap & { return cachedSender(cache); };
    const auto mediaData = [&]() -> const QVariantMap & { return cachedMedia(cache); };
    const auto replyData = [&]() -> const QVariantMap & { return cachedReply(cache); };
    const QString direction = item.value(QStringLiteral("direction")).toString();
    const QString status = item.value(QStringLiteral("status")).toString();
    const QString kind = mediaKind(item);
    const qint64 timestamp = item.value(QStringLiteral("timestamp")).toLongLong();
    const bool outgoing = direction == QLatin1String("outgoing");
    const bool groupChat = item.value(QStringLiteral("chat_id")).toString().endsWith(QStringLiteral("@g.us"));
    TextPresentation *presentation = nullptr;
    const auto textPresentation = [&]() -> TextPresentation & {
        if (!presentation) {
            presentation = &ensureTextPresentation(item);
        }
        return *presentation;
    };

    // Every branch below must return a *typed* QVariant, never a default-
    // constructed one. A missing key in a QVariantMap yields an invalid
    // QVariant, which reaches QML as `undefined`; binding that to a delegate's
    // `required property string` stringifies it to the literal "undefined"
    // rather than leaving it empty. The delegate used to launder every role
    // through `String(model.x || "")`, which hid it — DN9 removed that layer,
    // so the types have to be right at the source.
    switch (role) {
    case IdRole:
        return item.value(QStringLiteral("id")).toString();
    case ChatIdRole:
        return item.value(QStringLiteral("chat_id")).toString();
    case SenderIdRole:
        return senderData().value(QStringLiteral("id")).toString();
    case SenderNameRole:
        return senderDisplayName(item);
    case SenderAvatarLocalPathRole:
        return senderData().value(QStringLiteral("avatar_path")).toString();
    case SenderInitialsRole:
        return initialsForName(senderDisplayName(item));
    case TextRole:
        return displayText(item);
    case LayoutTextRole: {
        auto &text = textPresentation();
        return text.fullParsed ? text.fullMarkup.layoutText : (text.truncated ? text.sourceText : text.previewMarkup.layoutText);
    }
    case EmojiOnlyCountRole: {
        auto &text = textPresentation();
        return text.fullParsed ? text.fullMarkup.emojiOnlyCount : (text.truncated ? 0 : text.previewMarkup.emojiOnlyCount);
    }
    case HasRichTextRole: {
        auto &text = textPresentation();
        return text.fullParsed && text.fullMarkup.hasRichText;
    }
    case RichTextRole: {
        auto &text = textPresentation();
        return text.fullParsed ? text.fullMarkup.richText : QString();
    }
    case TextPreviewRole:
        return textPresentation().previewText;
    case LayoutTextPreviewRole:
        return textPresentation().previewMarkup.layoutText;
    case PreviewHasRichTextRole:
        return textPresentation().previewMarkup.hasRichText;
    case PreviewRichTextRole:
        return textPresentation().previewMarkup.richText;
    case TextTruncatedRole:
        return textPresentation().truncated;
    case TimestampUnixRole:
        return timestamp;
    case TimeTextRole:
        return formatTime(timestamp);
    case DateSeparatorTextRole:
        return startsDayGroup(index.row()) ? cachedRelativeDate(item) : QString();
    case DirectionRole:
        return directionValue(direction);
    case StatusRole:
        return statusValue(status);
    case StatusTextRole:
        return statusText(status);
    case IsOutgoingRole:
        return outgoing;
    case MediaKindRole:
        return kind;
    case MediaMimeTypeRole:
        return mediaData().value(QStringLiteral("mime")).toString();
    case MediaLocalPathRole:
        return mediaData().value(QStringLiteral("path")).toString();
    case MediaThumbnailLocalPathRole:
        return mediaData().value(QStringLiteral("thumbnail_path")).toString();
    case MediaWidthRole:
        return mediaData().value(QStringLiteral("width")).toInt();
    case MediaHeightRole:
        return mediaData().value(QStringLiteral("height")).toInt();
    case MediaAnimatedRole:
        return mediaData().value(QStringLiteral("animated")).toBool();
    case MediaSizeBytesRole:
        // Sizes exceed 2 GiB only in theory, but a document is the one kind
        // that could, so it stays a 64-bit value all the way to QML.
        return QVariant::fromValue(mediaData().value(QStringLiteral("size_bytes")).toLongLong());
    case MediaDurationSecsRole:
        return mediaData().value(QStringLiteral("duration_secs")).toInt();
    case MediaFileNameRole:
        return mediaData().value(QStringLiteral("filename")).toString();
    case MediaPageCountRole:
        return mediaData().value(QStringLiteral("page_count")).toInt();
    case MediaWaveformRole:
        return mediaData().value(QStringLiteral("waveform")).toList();
    case MediaPlayedRole:
        return mediaData().value(QStringLiteral("played")).toBool();
    case ShowSenderHeaderRole:
        return groupChat && !outgoing && startsSenderGroup(index.row());
    case ShowSenderAvatarRole:
        return groupChat && !outgoing && endsSenderGroup(index.row());
    case ShowSenderGutterRole:
        return groupChat && !outgoing;
    case GroupStartRole:
        return startsSenderGroup(index.row());
    case GroupEndRole:
        return endsSenderGroup(index.row());
    case MediaDownloadingRole:
        // Off the message row, not the `transfers` join: the two views are
        // recomputed independently, and reading this from the transfer row's
        // disappearance meant the bubble learned the download had ended before
        // it learned where the file was, and flashed its download button back
        // (PROTOCOL.md, "Messages").
        return mediaData().value(QStringLiteral("downloading")).toBool();
    case MediaDownloadErrorRole:
        return mediaData().value(QStringLiteral("download_error")).toString();
    case ReplyToMessageIdRole:
        return replyData().value(QStringLiteral("message_id")).toString();
    case ReplyToSenderNameRole:
        return replyData().value(QStringLiteral("sender_name")).toString();
    case ReplyToTextRole:
        return replyData().value(QStringLiteral("text")).toString();
    case ReplyToMediaKindRole: {
        const QString replyKind = replyData().value(QStringLiteral("kind")).toString();
        return replyKind == QLatin1String("text") ? QString() : replyKind;
    }
    case ReplyToMediaMimeTypeRole:
        return QString();
    case ReplyToIsOutgoingRole:
        return replyData().value(QStringLiteral("direction")).toString() == QLatin1String("outgoing");
    case WidestLineWidthRole: {
        auto &text = textPresentation();
        return text.fullParsed ? text.fullWidest : text.previewWidest;
    }
    case LastLineWidthRole: {
        auto &text = textPresentation();
        return text.fullParsed ? text.fullLast : text.previewLast;
    }
    case LinksRole:
        return textPresentation().links;
    case HasLinksRole:
        return !textPresentation().links.isEmpty();
    case MediaCacheKeyRole:
        return QString();
    case IsRevokedRole:
        return item.value(QStringLiteral("revoked")).toBool();
    case IsEditedRole:
        return item.value(QStringLiteral("edited")).toBool();
    case IsStarredRole:
        return item.value(QStringLiteral("starred")).toBool();
    case IsPinnedRole:
        return item.value(QStringLiteral("pinned_until")).toLongLong() > QDateTime::currentSecsSinceEpoch();
    case PinnedUntilUnixRole:
        return item.value(QStringLiteral("pinned_until")).toLongLong();
    case ReactionsRole:
        return reactions(item);
    case MediaDownloadProgressRole: {
        // -1 means "downloading, size unknown" to the bubble (it shows an
        // indeterminate spinner instead of the progress ring), which is also
        // what a message with no active transfer reports.
        const QVariantMap active = transfer(item);
        const qulonglong total = active.value(QStringLiteral("total_bytes")).toULongLong();
        if (active.isEmpty() || total == 0) {
            return -1.0;
        }
        const qulonglong received = active.value(QStringLiteral("received_bytes")).toULongLong();
        return std::min(1.0, static_cast<double>(received) / static_cast<double>(total));
    }
    default:
        return {};
    }
}

QHash<int, QByteArray> ProtocolMessageModel::roleNames() const
{
    return {
        {IdRole, "messageId"},
        {ChatIdRole, "chatId"},
        {SenderIdRole, "senderId"},
        {SenderNameRole, "senderName"},
        {SenderAvatarLocalPathRole, "senderAvatarLocalPath"},
        {SenderInitialsRole, "senderInitials"},
        {TextRole, "text"},
        {LayoutTextRole, "layoutText"},
        {EmojiOnlyCountRole, "emojiOnlyCount"},
        {HasRichTextRole, "hasRichText"},
        {RichTextRole, "richText"},
        {TextPreviewRole, "textPreview"},
        {LayoutTextPreviewRole, "layoutTextPreview"},
        {PreviewHasRichTextRole, "previewHasRichText"},
        {PreviewRichTextRole, "previewRichText"},
        {TextTruncatedRole, "textTruncated"},
        {TimestampUnixRole, "timestampUnix"},
        {TimeTextRole, "timeText"},
        {DateSeparatorTextRole, "dateSeparatorText"},
        {DirectionRole, "direction"},
        {StatusRole, "status"},
        {StatusTextRole, "statusText"},
        {IsOutgoingRole, "isOutgoing"},
        {MediaKindRole, "mediaKind"},
        {MediaMimeTypeRole, "mediaMimeType"},
        {MediaLocalPathRole, "mediaLocalPath"},
        {MediaThumbnailLocalPathRole, "mediaThumbnailLocalPath"},
        {MediaWidthRole, "mediaWidth"},
        {MediaHeightRole, "mediaHeight"},
        {MediaAnimatedRole, "mediaAnimated"},
        {MediaSizeBytesRole, "mediaSizeBytes"},
        {MediaDurationSecsRole, "mediaDurationSecs"},
        {MediaFileNameRole, "mediaFileName"},
        {MediaPageCountRole, "mediaPageCount"},
        {MediaWaveformRole, "mediaWaveform"},
        {MediaPlayedRole, "mediaPlayed"},
        {ShowSenderHeaderRole, "showSenderHeader"},
        {ShowSenderAvatarRole, "showSenderAvatar"},
        {ShowSenderGutterRole, "showSenderGutter"},
        {GroupStartRole, "groupStart"},
        {GroupEndRole, "groupEnd"},
        {MediaDownloadingRole, "mediaDownloading"},
        {MediaDownloadErrorRole, "mediaDownloadError"},
        {ReplyToMessageIdRole, "replyToMessageId"},
        {ReplyToSenderNameRole, "replyToSenderName"},
        {ReplyToTextRole, "replyToText"},
        {ReplyToMediaKindRole, "replyToMediaKind"},
        {ReplyToMediaMimeTypeRole, "replyToMediaMimeType"},
        {ReplyToIsOutgoingRole, "replyToIsOutgoing"},
        {WidestLineWidthRole, "widestLineWidth"},
        {LastLineWidthRole, "lastLineWidth"},
        {LinksRole, "links"},
        {HasLinksRole, "hasLinks"},
        {MediaCacheKeyRole, "mediaCacheKey"},
        {IsRevokedRole, "isRevoked"},
        {IsEditedRole, "isEdited"},
        {IsStarredRole, "isStarred"},
        {IsPinnedRole, "isPinned"},
        {PinnedUntilUnixRole, "pinnedUntilUnix"},
        {ReactionsRole, "reactions"},
        {MediaDownloadProgressRole, "mediaDownloadProgress"},
    };
}

QString ProtocolMessageModel::displayText(const QVariantMap &item)
{
    const QString kind = item.value(QStringLiteral("kind")).toString();
    const QString text = item.value(QStringLiteral("text")).toString();
    if (item.value(QStringLiteral("revoked")).toBool()) {
        return item.value(QStringLiteral("fallback")).toString();
    }
    // The daemon's fallback ("🎥 Video (0:11)") is a one-line summary for the
    // chat list, reply previews and notifications. A bubble that renders the
    // media itself must not also print it as a caption, so anything with a
    // media object shows only the real caption, which is usually empty.
    if (kind == QLatin1String("text") || !media(item).isEmpty()) {
        return text;
    }
    // No media to draw: unsupported kinds still need their tombstone label.
    return item.value(QStringLiteral("fallback")).toString();
}

QVariantMap ProtocolMessageModel::sender(const QVariantMap &item)
{
    return item.value(QStringLiteral("sender")).toMap();
}

QVariantMap ProtocolMessageModel::media(const QVariantMap &item)
{
    return item.value(QStringLiteral("media")).toMap();
}

QVariantMap ProtocolMessageModel::reply(const QVariantMap &item)
{
    return item.value(QStringLiteral("reply_to")).toMap();
}

QString ProtocolMessageModel::senderDisplayName(const QVariantMap &item)
{
    const QVariantMap senderData = sender(item);
    const QString name = senderData.value(QStringLiteral("name")).toString().trimmed();
    if (!name.isEmpty()) {
        return name;
    }
    const QString id = senderData.value(QStringLiteral("id")).toString().trimmed();
    if (!id.isEmpty() && id != QLatin1String("me")) {
        return id.section(QLatin1Char('@'), 0, 0);
    }
    return QStringLiteral("Unknown");
}

QString ProtocolMessageModel::initialsForName(const QString &name)
{
    QString initials;
    for (const auto &part : name.split(QChar::Space, Qt::SkipEmptyParts)) {
        initials.append(part.left(1).toUpper());
        if (initials.size() >= 2) {
            break;
        }
    }
    return initials.isEmpty() ? QStringLiteral("?") : initials;
}

int ProtocolMessageModel::directionValue(const QString &direction)
{
    if (direction == QLatin1String("incoming")) {
        return 1;
    }
    if (direction == QLatin1String("outgoing")) {
        return 2;
    }
    return 0;
}

int ProtocolMessageModel::statusValue(const QString &status)
{
    if (status == QLatin1String("pending")) return 1;
    if (status == QLatin1String("sent")) return 2;
    if (status == QLatin1String("delivered")) return 3;
    if (status == QLatin1String("read")) return 4;
    if (status == QLatin1String("failed")) return 5;
    return 0;
}

QString ProtocolMessageModel::statusText(const QString &status)
{
    if (status == QLatin1String("pending")) return i18nc("@label message delivery status", "Sending");
    if (status == QLatin1String("sent")) return i18nc("@label message delivery status", "Sent");
    if (status == QLatin1String("delivered")) return i18nc("@label message delivery status", "Delivered");
    if (status == QLatin1String("read")) return i18nc("@label message delivery status", "Read");
    if (status == QLatin1String("failed")) return i18nc("@label message delivery status", "Failed");
    return {};
}

QString ProtocolMessageModel::mediaKind(const QVariantMap &item)
{
    const QString kind = item.value(QStringLiteral("kind")).toString();
    return kind == QLatin1String("text") ? QString() : kind;
}

QVariantList ProtocolMessageModel::reactions(const QVariantMap &item)
{
    QVariantList result;
    for (const QVariant &entry : item.value(QStringLiteral("reactions")).toList()) {
        const QVariantMap reaction = entry.toMap();
        result.append(QVariantMap{
            {QStringLiteral("emoji"), reaction.value(QStringLiteral("emoji"))},
            {QStringLiteral("senderId"), reaction.value(QStringLiteral("sender_id"))},
            {QStringLiteral("senderName"), reaction.value(QStringLiteral("sender_name"))},
            {QStringLiteral("fromMe"), reaction.value(QStringLiteral("from_me"))},
        });
    }
    return result;
}

QList<whatevr::util::MessageMention> ProtocolMessageModel::mentions(const QVariantMap &item)
{
    QList<whatevr::util::MessageMention> result;
    for (const QVariant &entry : item.value(QStringLiteral("mentions")).toList()) {
        const QVariantMap mention = entry.toMap();
        result.append({mention.value(QStringLiteral("jid")).toString(), mention.value(QStringLiteral("name")).toString()});
    }
    return result;
}

int ProtocolMessageModel::dayNumber(const QVariantMap &item)
{
    const qint64 timestamp = item.value(QStringLiteral("timestamp")).toLongLong();
    return timestamp > 0
        ? static_cast<int>(QDateTime::fromSecsSinceEpoch(timestamp, QTimeZone::LocalTime).date().toJulianDay())
        : 0;
}

QString ProtocolMessageModel::formatTime(qint64 timestampUnix)
{
    if (timestampUnix <= 0) {
        return {};
    }
    return QDateTime::fromSecsSinceEpoch(timestampUnix, QTimeZone::LocalTime).time().toString(QStringLiteral("HH:mm"));
}

QString ProtocolMessageModel::formatRelativeDate(qint64 timestampUnix)
{
    if (timestampUnix <= 0) {
        return {};
    }
    const QDate date = QDateTime::fromSecsSinceEpoch(timestampUnix, QTimeZone::LocalTime).date();
    const QDate today = QDate::currentDate();
    const qint64 daysAgo = date.daysTo(today);
    if (daysAgo == 0) return i18nc("@title:row date separator for messages from today", "Today");
    if (daysAgo == 1) return i18nc("@title:row date separator for messages from yesterday", "Yesterday");
    if (daysAgo >= 2 && daysAgo <= 6) return QLocale().dayName(date.dayOfWeek(), QLocale::LongFormat);
    if (date.year() == today.year()) {
        return QLocale().toString(date, i18nc("date separator without year, e.g. 14 August", "d MMMM"));
    }
    return QLocale().toString(date, i18nc("date separator with year, e.g. 14 August 2023", "d MMMM yyyy"));
}

QString ProtocolMessageModel::cachedRelativeDate(const QVariantMap &item) const
{
    const QDate today = QDate::currentDate();
    if (m_dateTextDay != today) {
        m_dateTextByDay.clear();
        m_dateTextDay = today;
    }
    const int day = dayNumber(item);
    auto it = m_dateTextByDay.constFind(day);
    if (it != m_dateTextByDay.constEnd()) {
        return it.value();
    }
    const QString text = formatRelativeDate(item.value(QStringLiteral("timestamp")).toLongLong());
    m_dateTextByDay.insert(day, text);
    return text;
}

bool ProtocolMessageModel::startsSenderGroup(int row) const
{
    if (row <= 0 || row >= rowCount()) {
        return true;
    }
    const QVariantMap message = wireItem(row);
    const QVariantMap previous = wireItem(row - 1);
    if (directionValue(message.value(QStringLiteral("direction")).toString())
            != directionValue(previous.value(QStringLiteral("direction")).toString())
        || sender(message).value(QStringLiteral("id")) != sender(previous).value(QStringLiteral("id"))) {
        return true;
    }
    return message.value(QStringLiteral("timestamp")).toLongLong()
        - previous.value(QStringLiteral("timestamp")).toLongLong() > kSenderGroupGapSeconds;
}

bool ProtocolMessageModel::endsSenderGroup(int row) const
{
    if (row < 0 || row >= rowCount() - 1) {
        return true;
    }
    const QVariantMap message = wireItem(row);
    const QVariantMap next = wireItem(row + 1);
    if (directionValue(message.value(QStringLiteral("direction")).toString())
            != directionValue(next.value(QStringLiteral("direction")).toString())
        || sender(message).value(QStringLiteral("id")) != sender(next).value(QStringLiteral("id"))) {
        return true;
    }
    return next.value(QStringLiteral("timestamp")).toLongLong()
        - message.value(QStringLiteral("timestamp")).toLongLong() > kSenderGroupGapSeconds;
}

bool ProtocolMessageModel::startsDayGroup(int row) const
{
    return row <= 0 || dayNumber(wireItem(row)) != dayNumber(wireItem(row - 1));
}

ProtocolMessageModel::TextPresentation &ProtocolMessageModel::ensureTextPresentation(const QVariantMap &item) const
{
    const QString id = item.value(QStringLiteral("id")).toString();
    auto existing = m_textById.find(id);
    if (existing != m_textById.end()) {
        return existing.value();
    }

    TextPresentation presentation;
    presentation.sourceText = displayText(item);
    presentation.previewText = collapsedMessageText(presentation.sourceText);
    presentation.truncated = presentation.previewText != presentation.sourceText;
    presentation.previewMarkup = whatevr::util::parseWhatsAppMessageMarkup(
        presentation.previewText, mentions(item), item.value(QStringLiteral("chat_id")).toString().endsWith(QStringLiteral("@g.us")));
    presentation.links = whatevr::util::extractMessageLinks(presentation.sourceText);
    if (!presentation.truncated) {
        presentation.fullMarkup = presentation.previewMarkup;
        presentation.fullParsed = true;
    }
    remeasure(presentation);
    return m_textById.insert(id, std::move(presentation)).value();
}

void ProtocolMessageModel::ensureFullTextPresentation(TextPresentation &presentation, const QVariantMap &item) const
{
    if (presentation.fullParsed) {
        return;
    }
    presentation.fullMarkup = whatevr::util::parseWhatsAppMessageMarkup(
        presentation.sourceText, mentions(item), item.value(QStringLiteral("chat_id")).toString().endsWith(QStringLiteral("@g.us")));
    presentation.fullParsed = true;
    remeasure(presentation);
}

void ProtocolMessageModel::remeasure(TextPresentation &presentation) const
{
    const QString previewLayout = presentation.previewMarkup.layoutText.isEmpty()
        ? presentation.previewText : presentation.previewMarkup.layoutText;
    std::tie(presentation.previewWidest, presentation.previewLast) = measureLineWidths(previewLayout, m_bodyMetrics);
    if (presentation.fullParsed) {
        const QString fullLayout = presentation.fullMarkup.layoutText.isEmpty()
            ? presentation.sourceText : presentation.fullMarkup.layoutText;
        std::tie(presentation.fullWidest, presentation.fullLast) = measureLineWidths(fullLayout, m_bodyMetrics);
    }
}

int ProtocolMessageModel::indexOf(const QString &messageId) const
{
    return m_source ? m_source->indexOfId(messageId) : -1;
}

QString ProtocolMessageModel::messageIdAt(int row) const
{
    return wireItem(row).value(QStringLiteral("id")).toString();
}

bool ProtocolMessageModel::isOutgoingAt(int row) const
{
    return wireItem(row).value(QStringLiteral("direction")).toString() == QLatin1String("outgoing");
}

QString ProtocolMessageModel::dateTextForRow(int row) const
{
    return row >= 0 && row < rowCount() ? cachedRelativeDate(wireItem(row)) : QString();
}

void ProtocolMessageModel::setBodyMetricsFont(const QFont &font)
{
    if (font == m_bodyFont) {
        return;
    }
    m_bodyFont = font;
    m_bodyMetrics = QFontMetricsF(m_bodyFont);
    for (auto it = m_textById.begin(); it != m_textById.end(); ++it) {
        remeasure(it.value());
    }
    if (rowCount() > 0) {
        Q_EMIT dataChanged(index(0), index(rowCount() - 1), {WidestLineWidthRole, LastLineWidthRole});
    }
}

bool ProtocolMessageModel::expandMessageText(const QString &messageId)
{
    const int row = indexOf(messageId);
    if (row < 0) {
        return false;
    }
    const QVariantMap item = wireItem(row);
    TextPresentation &presentation = ensureTextPresentation(item);
    if (presentation.fullParsed) {
        return true;
    }
    ensureFullTextPresentation(presentation, item);
    Q_EMIT dataChanged(index(row), index(row),
                       {LayoutTextRole, EmojiOnlyCountRole, HasRichTextRole, RichTextRole,
                        WidestLineWidthRole, LastLineWidthRole});
    return true;
}

QString ProtocolMessageModel::copyTextForMessages(const QStringList &messageIds) const
{
    const QSet<QString> selected(messageIds.cbegin(), messageIds.cend());
    QList<QVariantMap> messages;
    for (int row = 0; row < rowCount(); ++row) {
        const QVariantMap item = wireItem(row);
        if (selected.contains(item.value(QStringLiteral("id")).toString())) {
            messages.append(item);
        }
    }
    if (messages.isEmpty()) {
        return {};
    }
    if (messages.size() == 1) {
        return displayText(messages.first());
    }

    QString out;
    for (const QVariantMap &item : messages) {
        if (!out.isEmpty()) {
            out += QLatin1Char('\n');
        }
        const bool outgoing = item.value(QStringLiteral("direction")).toString() == QLatin1String("outgoing");
        const qint64 timestamp = item.value(QStringLiteral("timestamp")).toLongLong();
        const QString date = QLocale().toString(
            QDateTime::fromSecsSinceEpoch(timestamp, QTimeZone::LocalTime).date(), QLocale::ShortFormat);
        out += QStringLiteral("[%1, %2] %3: %4")
                   .arg(date,
                        formatTime(timestamp),
                        outgoing ? i18nc("@label sender name for own messages in copied text", "You") : senderDisplayName(item),
                        displayText(item));
    }
    return out;
}

QVariantMap ProtocolMessageModel::messageSnapshot(const QString &messageId) const
{
    const int row = indexOf(messageId);
    if (row < 0) {
        return {};
    }
    const QVariantMap item = wireItem(row);
    TextPresentation &presentation = ensureTextPresentation(item);
    ensureFullTextPresentation(presentation, item);
    const QVariantMap mediaData = media(item);
    const qint64 pinnedUntil = item.value(QStringLiteral("pinned_until")).toLongLong();
    return {
        {QStringLiteral("messageId"), messageId},
        {QStringLiteral("text"), presentation.sourceText},
        {QStringLiteral("textPreview"), presentation.previewText},
        {QStringLiteral("hasRichText"), presentation.fullMarkup.hasRichText},
        {QStringLiteral("richText"), presentation.fullMarkup.richText},
        {QStringLiteral("links"), presentation.links},
        {QStringLiteral("senderName"), senderDisplayName(item)},
        {QStringLiteral("isOutgoing"), item.value(QStringLiteral("direction")).toString() == QLatin1String("outgoing")},
        {QStringLiteral("timestampUnix"), item.value(QStringLiteral("timestamp"))},
        {QStringLiteral("mediaKind"), mediaKind(item)},
        {QStringLiteral("mediaMimeType"), mediaData.value(QStringLiteral("mime"))},
        {QStringLiteral("mediaLocalPath"), mediaData.value(QStringLiteral("path"))},
        {QStringLiteral("mediaFileName"), mediaData.value(QStringLiteral("filename"))},
        {QStringLiteral("mediaSizeBytes"), mediaData.value(QStringLiteral("size_bytes"))},
        {QStringLiteral("mediaDurationSecs"), mediaData.value(QStringLiteral("duration_secs"))},
        {QStringLiteral("mediaCacheKey"), QString()},
        // Download state, so the context menu can offer Download, Cancel and
        // Retry rather than being blind to anything that is not on disk yet.
        {QStringLiteral("mediaDownloading"), data(index(row, 0), MediaDownloadingRole)},
        {QStringLiteral("mediaDownloadProgress"), data(index(row, 0), MediaDownloadProgressRole)},
        {QStringLiteral("mediaDownloadError"), mediaData.value(QStringLiteral("download_error"))},
        {QStringLiteral("mediaPageCount"), mediaData.value(QStringLiteral("page_count"))},
        {QStringLiteral("mediaPlayed"), mediaData.value(QStringLiteral("played"))},
        {QStringLiteral("isRevoked"), item.value(QStringLiteral("revoked"))},
        {QStringLiteral("isEdited"), item.value(QStringLiteral("edited"))},
        {QStringLiteral("isStarred"), item.value(QStringLiteral("starred"))},
        {QStringLiteral("isPinned"), pinnedUntil > QDateTime::currentSecsSinceEpoch()},
        {QStringLiteral("reactions"), reactions(item)},
    };
}

QStringList ProtocolMessageModel::allMessageIds() const
{
    QStringList ids;
    ids.reserve(rowCount());
    for (int row = 0; row < rowCount(); ++row) {
        const QString id = wireItem(row).value(QStringLiteral("id")).toString();
        if (!id.isEmpty()) {
            ids.append(id);
        }
    }
    return ids;
}

QVariantMap ProtocolMessageModel::nextVoiceMessage(const QString &messageId) const
{
    // WhatsApp plays a run of voice notes back to back, so finishing one hands
    // off to the next one below it that is already downloaded. A note that is
    // not on disk ends the run rather than stalling on a download.
    const int from = indexOf(messageId);
    if (from < 0) {
        return {};
    }
    for (int row = from + 1; row < rowCount(); ++row) {
        const QVariantMap item = wireItem(row);
        if (mediaKind(item) != QLatin1String("voice")) {
            continue;
        }
        const QVariantMap mediaData = media(item);
        const QString path = mediaData.value(QStringLiteral("path")).toString();
        if (path.isEmpty()) {
            return {};
        }
        return {
            {QStringLiteral("messageId"), item.value(QStringLiteral("id")).toString()},
            {QStringLiteral("localPath"), path},
            {QStringLiteral("durationSecs"), mediaData.value(QStringLiteral("duration_secs"))},
        };
    }
    return {};
}

QStringList ProtocolMessageModel::messageIdsForDay(const QString &messageId) const
{
    const int row = indexOf(messageId);
    if (row < 0) {
        return {};
    }
    const int selectedDay = dayNumber(wireItem(row));
    QStringList ids;
    for (int candidate = 0; candidate < rowCount(); ++candidate) {
        const QVariantMap item = wireItem(candidate);
        if (dayNumber(item) == selectedDay) {
            ids.append(item.value(QStringLiteral("id")).toString());
        }
    }
    return ids;
}

void ProtocolMessageModel::invalidateRows(int first, int last)
{
    if (!m_source) {
        return;
    }
    const int boundedFirst = std::max(0, first);
    const int boundedLast = std::min(last, m_source->rowCount() - 1);
    for (int row = boundedFirst; row <= boundedLast; ++row) {
        m_textById.remove(wireItem(row).value(QStringLiteral("id")).toString());
    }
}

void ProtocolMessageModel::emitAllRolesChanged(int first, int last)
{
    const int boundedFirst = std::max(0, first);
    const int boundedLast = std::min(last, rowCount() - 1);
    if (boundedFirst <= boundedLast) {
        Q_EMIT dataChanged(index(boundedFirst), index(boundedLast));
    }
}

void ProtocolMessageModel::emitNeighbourRolesChanged(int first, int last)
{
    const int boundedFirst = std::max(0, first);
    const int boundedLast = std::min(last, rowCount() - 1);
    if (boundedFirst > boundedLast) {
        return;
    }
    // An empty role list means "every role", which re-evaluates all ~45 model
    // bindings on every materialised delegate. A neighbour's appearance can
    // only change in these ways, so say so.
    Q_EMIT dataChanged(index(boundedFirst), index(boundedLast),
                       {ShowSenderHeaderRole, ShowSenderAvatarRole, ShowSenderGutterRole,
                        GroupStartRole, GroupEndRole, DateSeparatorTextRole});
}
