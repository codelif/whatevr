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

// The body font at the size the rich-text path draws inline emoji.
QFont scaledEmojiFont(const QFont &body)
{
    QFont scaled = body;
    const double factor = whatevr::util::inlineEmojiScale();
    if (scaled.pointSizeF() > 0) {
        scaled.setPointSizeF(scaled.pointSizeF() * factor);
    } else if (scaled.pixelSize() > 0) {
        scaled.setPixelSize(std::max(1, qRound(scaled.pixelSize() * factor)));
    }
    return scaled;
}

// Whether a line contains anything the renderer would enlarge. Cheap enough to
// run on every line so the common case never pays for the grapheme walk below.
bool mayContainEmoji(QStringView line)
{
    for (QChar ch : line) {
        if (ch.isHighSurrogate() || ch.unicode() >= 0x2000) {
            return true;
        }
    }
    return false;
}

// Advance of one line, with emoji measured at the size they are drawn.
//
// The rich-text path enlarges emoji, and this used to measure the whole line in
// the body font. The width it reported was therefore short by the difference on
// every message carrying one, and that width is what the bubble is built from:
// the plate came out narrower than its own last line, so the time and ticks no
// longer fitted beside the words and dropped onto a line of their own. Two
// messages of the same length would disagree about where their timestamp went
// purely because one of them ended in an emoji.
qreal measureLine(QStringView line, const QFontMetricsF &metrics, const QFontMetricsF &emojiMetrics)
{
    if (!mayContainEmoji(line)) {
        return metrics.horizontalAdvance(line.toString());
    }

    const QString text = line.toString();
    QTextBoundaryFinder finder(QTextBoundaryFinder::Grapheme, text);
    qreal advance = 0;
    int clusterStart = 0;
    // Runs of like-measured clusters are batched: shaping a whole run at once is
    // both faster and truer than summing per-cluster advances, which throws away
    // kerning between neighbouring letters.
    int runStart = 0;
    bool runIsEmoji = false;
    bool runOpen = false;
    const auto flushRun = [&](int runEnd) {
        if (!runOpen || runEnd <= runStart) {
            return;
        }
        const QString run = text.mid(runStart, runEnd - runStart);
        advance += runIsEmoji ? emojiMetrics.horizontalAdvance(run)
                              : metrics.horizontalAdvance(run);
    };

    finder.toStart();
    while (true) {
        const int clusterEnd = finder.toNextBoundary();
        if (clusterEnd < 0) {
            break;
        }
        const QString cluster = text.mid(clusterStart, clusterEnd - clusterStart);
        const bool isEmoji = whatevr::util::isEmojiGraphemeCluster(cluster);
        if (!runOpen) {
            runOpen = true;
            runIsEmoji = isEmoji;
            runStart = clusterStart;
        } else if (isEmoji != runIsEmoji) {
            flushRun(clusterStart);
            runIsEmoji = isEmoji;
            runStart = clusterStart;
        }
        clusterStart = clusterEnd;
    }
    flushRun(text.size());
    return advance;
}

std::pair<qreal, qreal> measureLineWidths(const QString &text, const QFontMetricsF &metrics,
                                          const QFontMetricsF &emojiMetrics)
{
    qreal widest = 0;
    qreal last = 0;
    for (const auto line : QStringView(text).split(QLatin1Char('\n'))) {
        last = measureLine(line, metrics, emojiMetrics);
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
    , m_emojiMetrics(scaledEmojiFont(m_bodyFont))
{
    Q_ASSERT(m_source);

    // Every structural change invalidates the decoded-row cache: it is keyed by
    // row index, which any insert/remove/move renumbers.
    connect(m_source, &QAbstractItemModel::modelAboutToBeReset, this, [this] {
        beginResetModel();
    });
    connect(m_source, &QAbstractItemModel::modelReset, this, [this] {
        // m_textById deliberately survives this. It is keyed by message id and
        // verifies its own contents on read (see ensureTextPresentation), so a
        // chat switch no longer throws away the shaping for a conversation the
        // reader is very likely to come back to. Only the row cache goes, and
        // that one has to: it is keyed by row index.
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

const QVariantMap &ProtocolMessageModel::cachedLocation(const RowCache &cache) const
{
    if (!cache.locationLoaded) {
        m_rowCache.location = cache.item.value(QStringLiteral("location")).toMap();
        m_rowCache.locationLoaded = true;
    }
    return m_rowCache.location;
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
    case SenderDeviceRole:
        return senderData().value(QStringLiteral("device")).toInt();
    case IsForwardedRole:
        return item.value(QStringLiteral("forwarded")).toBool();
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
    case HasMediaRole:
        return !mediaData().isEmpty();
    case IsKeptRole:
        return item.value(QStringLiteral("kept")).toBool();
    case LocationRole:
        return cachedLocation(cache);
    case LiveShareRole:
        return item.value(QStringLiteral("live")).toMap();
    case ContactsRole:
        return item.value(QStringLiteral("contacts")).toMap();
    case PollRole:
        return item.value(QStringLiteral("poll")).toMap();
    case GroupInviteRole:
        return item.value(QStringLiteral("invite")).toMap();
    case EventRole:
        return item.value(QStringLiteral("event")).toMap();
    case AlbumRole:
        return item.value(QStringLiteral("album")).toMap();
    case LinkPreviewRole:
        return item.value(QStringLiteral("link_preview")).toMap();
    case InteractiveRole:
        return item.value(QStringLiteral("interactive")).toMap();
    case CommerceRole:
        return item.value(QStringLiteral("commerce")).toMap();
    case StickerPackRole:
        return item.value(QStringLiteral("sticker_pack")).toMap();
    case CallLogRole:
        return item.value(QStringLiteral("call_log")).toMap();
    case SystemRole:
        return item.value(QStringLiteral("system")).toMap();
    case WaitingRole:
        return item.value(QStringLiteral("waiting")).toMap();
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
    case MediaDownloadProgressRole:
        // -1 means "downloading, size unknown" to the bubble (it shows an
        // indeterminate spinner instead of the progress ring), which is also
        // what a message with no active transfer reports.
        return downloadProgress(item);
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
        {HasMediaRole, "hasMedia"},
        {IsKeptRole, "isKept"},
        {LocationRole, "location"},
        {LiveShareRole, "liveShare"},
        {ContactsRole, "contacts"},
        {PollRole, "poll"},
        {GroupInviteRole, "invite"},
        {EventRole, "eventInfo"},
        {AlbumRole, "album"},
        {LinkPreviewRole, "linkPreview"},
        {InteractiveRole, "interactive"},
        {CommerceRole, "commerce"},
        {StickerPackRole, "stickerPack"},
        {CallLogRole, "callLog"},
        {SystemRole, "system"},
        {WaitingRole, "waiting"},
        {SenderDeviceRole, "senderDevice"},
        {IsForwardedRole, "isForwarded"},
    };
}

QString ProtocolMessageModel::displayText(const QVariantMap &item)
{
    const QString kind = item.value(QStringLiteral("kind")).toString();
    const QString text = item.value(QStringLiteral("text")).toString();
    if (item.value(QStringLiteral("revoked")).toBool()) {
        return item.value(QStringLiteral("fallback")).toString();
    }
    // The daemon's fallback ("🎥 Video (0:11)", "👤 Contact: Aditi Rao") is a
    // one-line summary for the chat list, reply previews and notifications. It
    // is also what PROTOCOL.md rule 5 hands a frontend for a kind it cannot
    // draw. A bubble that draws the kind itself must therefore not also print
    // it, or every contact card grows a caption repeating its own name.
    if (kind == QLatin1String("text") || rendersItsOwnPayload(item)) {
        return text;
    }
    return item.value(QStringLiteral("fallback")).toString();
}

// rendersItsOwnPayload reports whether this build has a bubble for the item's
// content. It asks by looking for the payload objects whatkevr knows how to
// draw rather than by listing kinds, so a kind arriving from a newer daemon
// with a payload this build has never heard of correctly falls back to the
// daemon's one-line rendering (rule 5) instead of drawing nothing.
bool ProtocolMessageModel::rendersItsOwnPayload(const QVariantMap &item)
{
    static const QStringList known{
        QStringLiteral("media"),
        QStringLiteral("location"),
        QStringLiteral("contacts"),
        QStringLiteral("poll"),
        QStringLiteral("invite"),
        QStringLiteral("event"),
        QStringLiteral("album"),
        QStringLiteral("interactive"),
        QStringLiteral("commerce"),
        QStringLiteral("sticker_pack"),
        QStringLiteral("call_log"),
        QStringLiteral("system"),
        QStringLiteral("waiting"),
    };
    for (const QString &key : known) {
        if (!item.value(key).toMap().isEmpty()) {
            return true;
        }
    }
    return false;
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

// A row nobody said: a call that happened, something the chat did to itself.
// It has a sender on the wire (the person who made the change) but it is not
// that person talking, so it belongs to no run of their messages: it neither
// starts one nor continues one, and it breaks the run it lands in.
bool ProtocolMessageModel::isAuthorless(const QVariantMap &item)
{
    const QString kind = item.value(QStringLiteral("kind")).toString();
    return kind == QLatin1String("system") || kind == QLatin1String("call_log");
}

bool ProtocolMessageModel::newestFirst() const
{
    return m_source && m_source->reverseOrder();
}

int ProtocolMessageModel::olderRow(int row) const
{
    if (row < 0 || row >= rowCount()) {
        return -1;
    }
    const int neighbour = newestFirst() ? row + 1 : row - 1;
    return (neighbour < 0 || neighbour >= rowCount()) ? -1 : neighbour;
}

int ProtocolMessageModel::newerRow(int row) const
{
    if (row < 0 || row >= rowCount()) {
        return -1;
    }
    const int neighbour = newestFirst() ? row - 1 : row + 1;
    return (neighbour < 0 || neighbour >= rowCount()) ? -1 : neighbour;
}

int ProtocolMessageModel::chronologicalRow(int nth) const
{
    if (nth < 0 || nth >= rowCount()) {
        return -1;
    }
    return newestFirst() ? rowCount() - 1 - nth : nth;
}

QString ProtocolMessageModel::oldestMessageId() const
{
    return messageIdAt(chronologicalRow(0));
}

QString ProtocolMessageModel::newestMessageId() const
{
    return messageIdAt(chronologicalRow(rowCount() - 1));
}

bool ProtocolMessageModel::startsSenderGroup(int row) const
{
    if (row < 0 || row >= rowCount()) {
        return true;
    }
    const QVariantMap message = wireItem(row);
    if (isAuthorless(message)) {
        return false;
    }
    const int before = olderRow(row);
    if (before < 0) {
        return true;
    }
    const QVariantMap previous = wireItem(before);
    if (isAuthorless(previous)) {
        return true;
    }
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
    if (row < 0 || row >= rowCount()) {
        return true;
    }
    const QVariantMap message = wireItem(row);
    if (isAuthorless(message)) {
        return false;
    }
    const int after = newerRow(row);
    if (after < 0) {
        return true;
    }
    const QVariantMap next = wireItem(after);
    if (isAuthorless(next)) {
        return true;
    }
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
    const int before = olderRow(row);
    return before < 0 || dayNumber(wireItem(row)) != dayNumber(wireItem(before));
}

// What a message's body costs to prepare: the WhatsApp markup parse, a walk of
// the link extractor's TLD table, and a font-metric measurement of every line.
// All three are pure functions of the body text and the body font, so the
// answer keeps until one of those changes.
//
// This cache used to be emptied whenever the source model reset, which is to
// say on every chat switch, so returning to a chat re-shaped every line of it
// from scratch. Message ids are unique across chats, so there was never
// anything unsafe about keeping them; what the clear was really guarding
// against is a message coming back under the same id with different words,
// which a resync can do. That is now checked directly, by comparing the text
// the row actually carries against the text the entry was built from. One
// string compare against re-parsing and re-shaping a paragraph is not a close
// call, and unlike the clear it is also correct for an edit that arrives while
// the chat is closed.
ProtocolMessageModel::TextPresentation &ProtocolMessageModel::ensureTextPresentation(const QVariantMap &item) const
{
    const QString id = item.value(QStringLiteral("id")).toString();
    const QString source = displayText(item);
    auto existing = m_textById.find(id);
    if (existing != m_textById.end() && existing->sourceText == source) {
        return existing.value();
    }

    // Bounded so a long session cannot grow it without limit. Dropping the lot
    // is crude next to evicting the coldest entries, but it happens roughly
    // never (the cap is many screens' worth of conversation), and paying for
    // recency bookkeeping on every row read to avoid it would cost more than it
    // saves.
    constexpr int kMaxCachedPresentations = 4000;
    if (m_textById.size() >= kMaxCachedPresentations) {
        m_textById.clear();
    }

    TextPresentation presentation;
    presentation.sourceText = source;
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
    std::tie(presentation.previewWidest, presentation.previewLast) =
        measureLineWidths(previewLayout, m_bodyMetrics, m_emojiMetrics);
    if (presentation.fullParsed) {
        const QString fullLayout = presentation.fullMarkup.layoutText.isEmpty()
            ? presentation.sourceText : presentation.fullMarkup.layoutText;
        std::tie(presentation.fullWidest, presentation.fullLast) =
            measureLineWidths(fullLayout, m_bodyMetrics, m_emojiMetrics);
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
    m_emojiMetrics = QFontMetricsF(scaledEmojiFont(m_bodyFont));
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
    // Gathered oldest first, not row first: what comes out of here is a
    // conversation somebody is about to paste somewhere.
    for (int nth = 0; nth < rowCount(); ++nth) {
        const QVariantMap item = wireItem(chronologicalRow(nth));
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
    if (row >= 0) {
        return snapshotOfItem(wireItem(row), messageId);
    }
    // A picture inside an album is a real message with a real id, and it is
    // the only kind of message that is not a row: the album is the row. Every
    // path that takes a message id (the full-screen viewer, Save As, Forward,
    // the context menu) still has to be able to find it, so the miss falls
    // through to the albums in the window rather than returning nothing.
    for (int i = 0; i < rowCount(); ++i) {
        const QVariantList tiles = wireItem(i)
                                       .value(QStringLiteral("album"))
                                       .toMap()
                                       .value(QStringLiteral("items"))
                                       .toList();
        for (const QVariant &tile : tiles) {
            const QVariantMap map = tile.toMap();
            if (map.value(QStringLiteral("id")).toString() == messageId) {
                return snapshotOfItem(map, messageId);
            }
        }
    }
    return {};
}

QVariantMap ProtocolMessageModel::snapshotOfItem(const QVariantMap &item, const QString &messageId) const
{
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
        // The clip's own pixel size, which is what lets the full-screen viewer
        // tell the picture apart from the letterbox around it.
        {QStringLiteral("mediaWidth"), mediaData.value(QStringLiteral("width"))},
        {QStringLiteral("mediaHeight"), mediaData.value(QStringLiteral("height"))},
        {QStringLiteral("mediaCacheKey"), QString()},
        // Download state, so the context menu can offer Download, Cancel and
        // Retry rather than being blind to anything that is not on disk yet.
        // Both are derived from the item rather than from the row, because an
        // album's pictures have no row and still download like anything else.
        {QStringLiteral("mediaDownloading"), mediaData.value(QStringLiteral("downloading")).toBool()},
        {QStringLiteral("mediaDownloadProgress"), downloadProgress(item)},
        {QStringLiteral("mediaDownloadError"), mediaData.value(QStringLiteral("download_error"))},
        {QStringLiteral("mediaPageCount"), mediaData.value(QStringLiteral("page_count"))},
        {QStringLiteral("mediaPlayed"), mediaData.value(QStringLiteral("played"))},
        {QStringLiteral("isRevoked"), item.value(QStringLiteral("revoked"))},
        {QStringLiteral("isEdited"), item.value(QStringLiteral("edited"))},
        {QStringLiteral("isForwarded"), item.value(QStringLiteral("forwarded")).toBool()},
        {QStringLiteral("isStarred"), item.value(QStringLiteral("starred"))},
        {QStringLiteral("isPinned"), pinnedUntil > QDateTime::currentSecsSinceEpoch()},
        {QStringLiteral("reactions"), reactions(item)},
        // An album's pictures, so opening one can open the set it belongs to
        // rather than a lone photo with no way back to its siblings.
        {QStringLiteral("album"), item.value(QStringLiteral("album"))},
    };
}

// downloadProgress is the transfer's completion 0..1, or -1 for "no active
// transfer, or one whose total size is unknown", which is the same thing to a
// spinner. It reads the item rather than a row so an album's pictures, which
// have no row of their own, still report their own progress.
double ProtocolMessageModel::downloadProgress(const QVariantMap &item) const
{
    const QVariantMap active = transfer(item);
    const qulonglong total = active.value(QStringLiteral("total_bytes")).toULongLong();
    if (active.isEmpty() || total == 0) {
        return -1.0;
    }
    const qulonglong received = active.value(QStringLiteral("received_bytes")).toULongLong();
    return std::min(1.0, static_cast<double>(received) / static_cast<double>(total));
}

// Oldest to newest, whichever way the rows happen to be held. Everything that
// consumes this reads it as a transcript (select all, then copy it), and a
// transcript that ran backwards would be a strange thing to put on a clipboard.
QStringList ProtocolMessageModel::allMessageIds() const
{
    QStringList ids;
    ids.reserve(rowCount());
    for (int nth = 0; nth < rowCount(); ++nth) {
        const QString id = wireItem(chronologicalRow(nth)).value(QStringLiteral("id")).toString();
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
    for (int row = newerRow(from); row >= 0; row = newerRow(row)) {
        const QVariantMap item = wireItem(row);
        if (mediaKind(item) != QLatin1String("voice")) {
            continue;
        }
        const QVariantMap mediaData = media(item);
        const QString path = mediaData.value(QStringLiteral("path")).toString();
        if (path.isEmpty()) {
            return {};
        }
        // The sender fields ride along so the handoff can refresh the player's
        // now-playing snapshot; a chained note is nobody's bubble's doing.
        return {
            {QStringLiteral("messageId"), item.value(QStringLiteral("id")).toString()},
            {QStringLiteral("localPath"), path},
            {QStringLiteral("durationSecs"), mediaData.value(QStringLiteral("duration_secs"))},
        {QStringLiteral("senderName"), senderDisplayName(item)},
        {QStringLiteral("senderDevice"), sender(item).value(QStringLiteral("device")).toInt()},
            {QStringLiteral("avatarPath"), sender(item).value(QStringLiteral("avatar_path"))},
            {QStringLiteral("waveform"), mediaData.value(QStringLiteral("waveform"))},
            {QStringLiteral("isOutgoing"), item.value(QStringLiteral("direction")).toString() == QLatin1String("outgoing")},
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
    for (int nth = 0; nth < rowCount(); ++nth) {
        const QVariantMap item = wireItem(chronologicalRow(nth));
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
