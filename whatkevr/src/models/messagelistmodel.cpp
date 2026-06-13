#include "messagelistmodel.h"

#include <QDateTime>
#include <QElapsedTimer>
#include <QFontMetricsF>
#include <QGuiApplication>
#include <QSet>
#include <QTextBoundaryFinder>
#include <QTimeZone>

#include <KLocalizedString>

#include <algorithm>
#include <cmath>
#include <tuple>
#include <utility>

#include "whatevr/v1/whatevr.qpb.h"

#include "messagemarkup.h"
#include "richtext.h"

namespace {
constexpr qint64 kSenderGroupGapSeconds = 5LL * 60;
constexpr int kCollapsedTextMaxGraphemes = 900;
constexpr int kCollapsedTextMaxLines = 12;

using whatevr::util::plainTextFromQtRichText;
using whatevr::util::parseWhatsAppMessageMarkup;

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
        if (text.at(i) != QLatin1Char('\n')) {
            continue;
        }
        ++lines;
        if (lines > maxLines) {
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

// Metrics for the message body font (the bubble renders body text with the
// application default font). Rebuilt if the application font changes; items
// parsed before such a change keep their old widths until their text changes,
// which is acceptable for a rare runtime event.
const QFontMetricsF &bodyFontMetrics()
{
    static QFont cachedFont = QGuiApplication::font();
    static QFontMetricsF metrics(cachedFont);
    if (cachedFont != QGuiApplication::font()) {
        cachedFont = QGuiApplication::font();
        metrics = QFontMetricsF(cachedFont);
    }
    return metrics;
}

// Unwrapped advance width of the widest and the last line. Mirrors the layout
// decisions ChatBubble makes (natural bubble width, inline time+ticks fit).
std::pair<qreal, qreal> measureLineWidths(const QString &text)
{
    const QFontMetricsF &metrics = bodyFontMetrics();
    qreal widest = 0;
    qreal last = 0;
    for (const auto line : QStringView(text).split(QLatin1Char('\n'))) {
        last = metrics.horizontalAdvance(line.toString());
        widest = std::max(widest, last);
    }
    // Round up with one pixel of headroom: QML lays the text out with its own
    // rasterised metrics and must never wrap a line we measured as fitting.
    return {std::ceil(widest) + 1, std::ceil(last) + 1};
}
}

MessageListModel::MessageListModel(QObject *parent)
    : QAbstractListModel(parent)
{
    // Interval 0: one slice per event-loop pass, yielding to input and render
    // work between slices.
    m_warmupTimer.setInterval(0);
    connect(&m_warmupTimer, &QTimer::timeout, this, &MessageListModel::parseWarmupTick);
}

void MessageListModel::scheduleParseWarmup(int fromRow)
{
    m_warmupCursor = std::clamp(std::min(m_warmupCursor, fromRow), 0, static_cast<int>(m_messages.size()));
    if (!m_warmupTimer.isActive() && m_warmupCursor < m_messages.size()) {
        m_warmupTimer.start();
    }
}

void MessageListModel::parseWarmupTick()
{
    // ~2 ms per slice: a full 80-row page clears within a few frames without
    // ever blocking one. Rows a delegate touched first are no-ops here, and
    // rows parsed here are no-ops later in data() — the cache fill must never
    // emit signals, so warm-up is invisible to the view.
    QElapsedTimer budget;
    budget.start();
    while (m_warmupCursor < m_messages.size()) {
        ensurePreviewParsed(m_messages.at(m_warmupCursor));
        ++m_warmupCursor;
        if (budget.nsecsElapsed() > 2'000'000) {
            return;
        }
    }
    m_warmupTimer.stop();
}

int MessageListModel::rowCount(const QModelIndex &parent) const
{
    if (parent.isValid()) {
        return 0;
    }
    return static_cast<int>(m_messages.size());
}

QVariant MessageListModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= m_messages.size()) {
        return {};
    }

    const auto &message = m_messages.at(index.row());
    switch (role) {
    // Roles backed by the lazy preview/markup cache: parse on first access.
    case LayoutTextRole:
    case EmojiOnlyCountRole:
    case HasRichTextRole:
    case RichTextRole:
    case TextPreviewRole:
    case LayoutTextPreviewRole:
    case PreviewHasRichTextRole:
    case PreviewRichTextRole:
    case TextTruncatedRole:
    case WidestLineWidthRole:
    case LastLineWidthRole:
    case LinksRole:
    case HasLinksRole:
        ensurePreviewParsed(message);
        break;
    default:
        break;
    }

    switch (role) {
    case IdRole:
        return message.id;
    case ChatIdRole:
        return message.chatId;
    case SenderIdRole:
        return message.senderId;
    case SenderNameRole:
        return message.senderDisplayName;
    case SenderAvatarLocalPathRole:
        return message.senderAvatarLocalPath;
    case SenderInitialsRole:
        return message.senderInitials;
    case TextRole:
        return message.text;
    case LayoutTextRole:
        return message.layoutText;
    case EmojiOnlyCountRole:
        return message.emojiOnlyCount;
    case HasRichTextRole:
        return message.hasRichText;
    case RichTextRole:
        return message.richText;
    case TextPreviewRole:
        return message.textPreview;
    case LayoutTextPreviewRole:
        return message.layoutTextPreview;
    case PreviewHasRichTextRole:
        return message.previewHasRichText;
    case PreviewRichTextRole:
        return message.previewRichText;
    case TextTruncatedRole:
        return message.textTruncated;
    case TimestampUnixRole:
        return message.timestampUnix;
    case TimeTextRole:
        return message.timeText;
    case DateSeparatorTextRole:
        return startsDayGroup(index.row()) ? cachedRelativeDate(message) : QString();
    case DirectionRole:
        return message.direction;
    case StatusRole:
        return message.status;
    case StatusTextRole:
        return message.statusText;
    case IsOutgoingRole:
        return message.direction == static_cast<int>(whatevr::v1::MessageDirectionGadget::MessageDirection::MESSAGE_DIRECTION_OUTGOING);
    case MediaKindRole:
        return message.mediaKind;
    case MediaMimeTypeRole:
        return message.mediaMimeType;
    case MediaLocalPathRole:
        return message.mediaLocalPath;
    case MediaThumbnailLocalPathRole:
        return message.mediaThumbnailLocalPath;
    case MediaWidthRole:
        return message.mediaWidth;
    case MediaHeightRole:
        return message.mediaHeight;
    case MediaAnimatedRole:
        return message.mediaAnimated;
    case ShowSenderHeaderRole:
        return m_groupChat && !isOutgoing(message) && startsSenderGroup(index.row());
    case ShowSenderAvatarRole:
        return m_groupChat && !isOutgoing(message) && endsSenderGroup(index.row());
    case ShowSenderGutterRole:
        return m_groupChat && !isOutgoing(message);
    case GroupStartRole:
        return startsSenderGroup(index.row());
    case GroupEndRole:
        return endsSenderGroup(index.row());
    case MediaDownloadingRole:
        return message.mediaDownloading;
    case MediaDownloadErrorRole:
        return message.mediaDownloadError;
    case ReplyToMessageIdRole:
        return message.replyToMessageId;
    case ReplyToSenderNameRole:
        return message.replyToSenderName;
    case ReplyToTextRole:
        return message.replyToText;
    case ReplyToMediaKindRole:
        return message.replyToMediaKind;
    case ReplyToMediaMimeTypeRole:
        return message.replyToMediaMimeType;
    case ReplyToIsOutgoingRole:
        return message.replyToDirection == static_cast<int>(whatevr::v1::MessageDirectionGadget::MessageDirection::MESSAGE_DIRECTION_OUTGOING);
    case WidestLineWidthRole:
        return message.widestLineWidth;
    case LastLineWidthRole:
        return message.lastLineWidth;
    case LinksRole:
        return message.links;
    case HasLinksRole:
        return !message.links.isEmpty();
    case MediaCacheKeyRole:
        return message.mediaCacheKey;
    case IsRevokedRole:
        return message.isRevoked;
    case IsEditedRole:
        return message.isEdited;
    case ReactionsRole:
        return message.reactions;
    case MediaDownloadProgressRole:
        return message.mediaDownloadTotalBytes > 0
            ? qMin<qreal>(1.0, static_cast<qreal>(message.mediaDownloadReceivedBytes) / static_cast<qreal>(message.mediaDownloadTotalBytes))
            : -1.0;
    default:
        return {};
    }
}

QHash<int, QByteArray> MessageListModel::roleNames() const
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
        {ReactionsRole, "reactions"},
        {MediaDownloadProgressRole, "mediaDownloadProgress"},
    };
}

void MessageListModel::replaceMessages(const QList<whatevr::v1::Message> &messages)
{
    QList<MessageItem> next;
    next.reserve(messages.size());
    for (const auto &message : messages) {
        MessageItem item = fromProto(message);
        const int existingIndex = indexOf(item.id);
        if (existingIndex >= 0) {
            const auto &existing = m_messages.at(existingIndex);
            item.mediaDownloading = existing.mediaDownloading;
            item.mediaDownloadError = existing.mediaDownloadError;
            item.mediaDownloadReceivedBytes = existing.mediaDownloadReceivedBytes;
            item.mediaDownloadTotalBytes = existing.mediaDownloadTotalBytes;
            if (item.text == existing.text) {
                transplantParsedState(item, existing);
            }
        }
        next.append(std::move(item));
    }

    // Newest first: index 0 is the most recent message. The view renders the
    // list bottom-to-top, so older history lands at higher indices. Within a
    // single timestamp second, sortSeq (the daemon's insertion order) decides;
    // the random message id is only a final stable fallback.
    std::sort(next.begin(), next.end(), [](const MessageItem &left, const MessageItem &right) {
        if (left.timestampUnix != right.timestampUnix) {
            return left.timestampUnix > right.timestampUnix;
        }
        if (left.sortSeq != right.sortSeq) {
            return left.sortSeq > right.sortSeq;
        }
        return left.id > right.id;
    });

    if (sameMessages(m_messages, next)) {
        Q_EMIT modelReplaced();
        return;
    }

    if (sameMessageOrder(m_messages, next)) {
        // Emit dataChanged once per contiguous run of changed rows instead of
        // per row; a refetch typically touches many neighbouring rows at once.
        int firstChanged = -1;
        int lastChanged = -1;
        int runStart = -1;
        for (int i = 0; i < next.size(); ++i) {
            if (sameMessageData(m_messages.at(i), next.at(i))) {
                if (runStart >= 0) {
                    Q_EMIT dataChanged(index(runStart, 0), index(i - 1, 0));
                    runStart = -1;
                }
                continue;
            }
            m_messages[i] = next.at(i);
            if (runStart < 0) {
                runStart = i;
            }
            if (firstChanged < 0) {
                firstChanged = i;
            }
            lastChanged = i;
        }
        if (runStart >= 0) {
            Q_EMIT dataChanged(index(runStart, 0), index(static_cast<int>(next.size()) - 1, 0));
        }
        if (firstChanged >= 0) {
            emitGroupingRolesChanged(firstChanged, lastChanged);
        }
        // Replaced rows lost their parsed state; re-warm after first paint has
        // had a frame to settle (a chat switch typically lands here).
        QTimer::singleShot(50, this, [this] { scheduleParseWarmup(0); });
        Q_EMIT modelReplaced();
        return;
    }

    // Insertion/removal path: merge by id so delegate state and the scroll
    // position survive a refetch that gained or lost messages (the common case
    // after restoring cached messages while new ones arrived). Survivors must
    // keep their relative order for the merge to be valid; if they reordered
    // (e.g. server timestamp corrections), fall back to a full reset.
    QSet<QString> nextIds;
    nextIds.reserve(next.size());
    for (const auto &item : next) {
        nextIds.insert(item.id);
    }

    QStringList survivorsInCurrent;
    for (const auto &item : m_messages) {
        if (nextIds.contains(item.id)) {
            survivorsInCurrent.append(item.id);
        }
    }
    QStringList survivorsInNext;
    for (const auto &item : next) {
        if (m_messageIndexById.contains(item.id)) {
            survivorsInNext.append(item.id);
        }
    }

    if (survivorsInCurrent != survivorsInNext) {
        beginResetModel();
        m_messages = std::move(next);
        rebuildIndex();
        endResetModel();
        m_warmupCursor = 0;
        QTimer::singleShot(50, this, [this] { scheduleParseWarmup(0); });
        return;
    }

    int minTouched = -1;
    int maxTouched = -1;
    const auto touch = [&minTouched, &maxTouched](int first, int last) {
        minTouched = minTouched < 0 ? first : std::min(minTouched, first);
        maxTouched = std::max(maxTouched, last);
    };

    // Remove vanished rows in contiguous runs, scanning backwards so row
    // numbers stay valid while we mutate.
    for (int row = static_cast<int>(m_messages.size()) - 1; row >= 0;) {
        if (nextIds.contains(m_messages.at(row).id)) {
            --row;
            continue;
        }
        const int last = row;
        while (row > 0 && !nextIds.contains(m_messages.at(row - 1).id)) {
            --row;
        }
        beginRemoveRows(QModelIndex(), row, last);
        m_messages.remove(row, last - row + 1);
        endRemoveRows();
        touch(row, row);
        --row;
    }

    // m_messages now holds exactly the survivors in next's order: walk both
    // lists, inserting runs of new rows and refreshing changed survivors.
    int row = 0;
    for (int i = 0; i < next.size();) {
        if (row < m_messages.size() && m_messages.at(row).id == next.at(i).id) {
            if (!sameMessageData(m_messages.at(row), next.at(i))) {
                m_messages[row] = next.at(i);
                const QModelIndex changedIndex = index(row, 0);
                Q_EMIT dataChanged(changedIndex, changedIndex);
                touch(row, row);
            }
            ++row;
            ++i;
            continue;
        }
        int runEnd = i;
        while (runEnd < next.size()
               && !(row < m_messages.size() && m_messages.at(row).id == next.at(runEnd).id)) {
            ++runEnd;
        }
        const int count = runEnd - i;
        beginInsertRows(QModelIndex(), row, row + count - 1);
        for (int k = 0; k < count; ++k) {
            m_messages.insert(row + k, next.at(i + k));
        }
        endInsertRows();
        touch(row, row + count - 1);
        row += count;
        i = runEnd;
    }

    rebuildIndex();
    if (maxTouched >= 0) {
        emitGroupingRolesChanged(minTouched, maxTouched);
    }
    QTimer::singleShot(50, this, [this] { scheduleParseWarmup(0); });
    Q_EMIT modelReplaced();
}

void MessageListModel::appendOlderMessages(const QList<whatevr::v1::Message> &messages)
{
    QList<MessageItem> older;
    older.reserve(messages.size());
    QSet<QString> seenIncoming;
    for (const auto &message : messages) {
        const MessageItem item = fromProto(message);
        if (item.id.isEmpty() || seenIncoming.contains(item.id)) {
            continue;
        }
        seenIncoming.insert(item.id);

        const int existingIndex = indexOf(item.id);
        if (existingIndex >= 0) {
            if (!sameMessageData(m_messages.at(existingIndex), item)) {
                MessageItem updated = item;
                const auto &existing = m_messages.at(existingIndex);
                updated.mediaDownloading = existing.mediaDownloading;
                updated.mediaDownloadError = existing.mediaDownloadError;
                updated.mediaDownloadReceivedBytes = existing.mediaDownloadReceivedBytes;
                updated.mediaDownloadTotalBytes = existing.mediaDownloadTotalBytes;
                if (updated.text == existing.text) {
                    transplantParsedState(updated, existing);
                }
                m_messages[existingIndex] = std::move(updated);
                const QModelIndex changedIndex = index(existingIndex, 0);
                Q_EMIT dataChanged(changedIndex, changedIndex);
                emitGroupingRolesChanged(existingIndex, existingIndex);
            }
        } else {
            older.append(item);
        }
    }

    if (older.isEmpty()) {
        return;
    }

    // Keep the batch newest-first so it slots onto the end of the (newest-first)
    // list while preserving the global ordering.
    std::sort(older.begin(), older.end(), [](const MessageItem &left, const MessageItem &right) {
        if (left.timestampUnix != right.timestampUnix) {
            return left.timestampUnix > right.timestampUnix;
        }
        if (left.sortSeq != right.sortSeq) {
            return left.sortSeq > right.sortSeq;
        }
        return left.id > right.id;
    });

    const int firstNew = static_cast<int>(m_messages.size());
    beginInsertRows(QModelIndex(), firstNew, firstNew + static_cast<int>(older.size()) - 1);
    m_messages.reserve(m_messages.size() + older.size());
    for (const auto &message : older) {
        m_messages.append(message);
    }
    rebuildIndex();
    endInsertRows();
    // Refresh grouping roles around the seam: the previously-oldest message now
    // has an older neighbour and may no longer start a sender group.
    emitGroupingRolesChanged(firstNew - 1, firstNew);
    // No delay: the user is scrolling toward these rows right now.
    scheduleParseWarmup(firstNew);
}

void MessageListModel::clear()
{
    if (m_messages.isEmpty()) {
        return;
    }

    beginResetModel();
    m_messages.clear();
    m_messageIndexById.clear();
    endResetModel();
    m_warmupTimer.stop();
    m_warmupCursor = 0;
}

void MessageListModel::upsertMessage(const whatevr::v1::Message &message)
{
    const MessageItem item = fromProto(message);
    const int existingIndex = indexOf(item.id);

    if (existingIndex >= 0) {
        MessageItem updated = item;
        const auto &existing = m_messages.at(existingIndex);
        updated.mediaDownloading = existing.mediaDownloading;
        updated.mediaDownloadError = existing.mediaDownloadError;
        updated.mediaDownloadReceivedBytes = existing.mediaDownloadReceivedBytes;
        updated.mediaDownloadTotalBytes = existing.mediaDownloadTotalBytes;
        if (updated.text == existing.text) {
            transplantParsedState(updated, existing);
        }
        m_messages[existingIndex] = std::move(updated);
        const QModelIndex changedIndex = index(existingIndex, 0);
        Q_EMIT dataChanged(changedIndex, changedIndex);
        emitGroupingRolesChanged(existingIndex, existingIndex);
        return;
    }

    // Newest-first order: advance past every message that is newer than this
    // one. Within a single timestamp second, sortSeq (the daemon's insertion
    // order) decides; the random message id is only a final stable fallback.
    int insertAt = 0;
    while (insertAt < static_cast<int>(m_messages.size())
           && (m_messages.at(insertAt).timestampUnix > item.timestampUnix
               || (m_messages.at(insertAt).timestampUnix == item.timestampUnix && m_messages.at(insertAt).sortSeq > item.sortSeq)
               || (m_messages.at(insertAt).timestampUnix == item.timestampUnix && m_messages.at(insertAt).sortSeq == item.sortSeq && m_messages.at(insertAt).id > item.id))) {
        ++insertAt;
    }

    beginInsertRows(QModelIndex(), insertAt, insertAt);
    m_messages.insert(insertAt, item);
    rebuildIndex();
    endInsertRows();
    emitGroupingRolesChanged(insertAt - 1, insertAt + 1);
    scheduleParseWarmup(insertAt);
}

bool MessageListModel::removeMessage(const QString &messageId)
{
    const int row = indexOf(messageId);
    if (row < 0) {
        return false;
    }

    beginRemoveRows(QModelIndex(), row, row);
    m_messages.removeAt(row);
    rebuildIndex();
    endRemoveRows();
    m_warmupCursor = std::min(m_warmupCursor, static_cast<int>(m_messages.size()));
    // Neighbours may have gained/lost a group edge or the date separator.
    emitGroupingRolesChanged(row - 1, row);
    return true;
}

QString MessageListModel::copyTextForMessages(const QStringList &messageIds) const
{
    QList<const MessageItem *> selected;
    selected.reserve(messageIds.size());
    for (const auto &id : messageIds) {
        const int row = indexOf(id);
        if (row >= 0) {
            selected.append(&m_messages.at(row));
        }
    }
    if (selected.isEmpty()) {
        return {};
    }

    const auto bodyText = [](const MessageItem &message) -> QString {
        if (message.isRevoked) {
            return i18nc("@info placeholder body for a deleted message", "This message was deleted");
        }
        if (!message.text.isEmpty()) {
            return message.text;
        }
        if (message.mediaKind == QStringLiteral("sticker")) {
            return QStringLiteral("[Sticker]");
        }
        if (!message.mediaKind.isEmpty() || !message.mediaMimeType.isEmpty()) {
            return QStringLiteral("[Media]");
        }
        return {};
    };

    if (selected.size() == 1) {
        return bodyText(*selected.first());
    }

    // Chronological, oldest first (storage is newest-first).
    std::sort(selected.begin(), selected.end(), [](const MessageItem *left, const MessageItem *right) {
        if (left->timestampUnix != right->timestampUnix) {
            return left->timestampUnix < right->timestampUnix;
        }
        if (left->sortSeq != right->sortSeq) {
            return left->sortSeq < right->sortSeq;
        }
        return left->id < right->id;
    });

    QString out;
    for (const MessageItem *message : selected) {
        if (!out.isEmpty()) {
            out += QLatin1Char('\n');
        }
        const QString sender = isOutgoing(*message)
            ? i18nc("@label sender name for own messages in copied text", "You")
            : message->senderDisplayName;
        const QString date = QLocale().toString(
            QDateTime::fromSecsSinceEpoch(message->timestampUnix, QTimeZone::LocalTime).date(), QLocale::ShortFormat);
        out += QStringLiteral("[%1, %2] %3: %4").arg(date, message->timeText, sender, bodyText(*message));
    }
    return out;
}

QVariantMap MessageListModel::messageSnapshot(const QString &messageId) const
{
    const int row = indexOf(messageId);
    if (row < 0) {
        return {};
    }
    const auto &message = m_messages.at(row);
    ensurePreviewParsed(message);
    // hasRichText/richText in the cache only cover the displayed (possibly
    // collapsed) text; "Copy as Markdown" and the full-message dialog must
    // reflect the full text, so parse it on demand when it was collapsed.
    bool hasMarkup = message.hasRichText;
    QString fullRichText = message.richText;
    if (message.textTruncated && !message.fullMarkupParsed) {
        const auto markup = parseWhatsAppMessageMarkup(message.text);
        hasMarkup = markup.hasRichText;
        fullRichText = markup.richText;
    }
    return {
        {QStringLiteral("messageId"), message.id},
        {QStringLiteral("text"), message.text},
        {QStringLiteral("textPreview"), message.textPreview},
        {QStringLiteral("hasRichText"), hasMarkup},
        {QStringLiteral("richText"), fullRichText},
        {QStringLiteral("links"), message.links},
        {QStringLiteral("senderName"), message.senderDisplayName},
        {QStringLiteral("isOutgoing"), isOutgoing(message)},
        {QStringLiteral("timestampUnix"), message.timestampUnix},
        {QStringLiteral("mediaKind"), message.mediaKind},
        {QStringLiteral("mediaMimeType"), message.mediaMimeType},
        {QStringLiteral("mediaLocalPath"), message.mediaLocalPath},
        {QStringLiteral("mediaCacheKey"), message.mediaCacheKey},
        {QStringLiteral("isRevoked"), message.isRevoked},
        {QStringLiteral("isEdited"), message.isEdited},
        {QStringLiteral("reactions"), message.reactions},
    };
}

QStringList MessageListModel::allMessageIds() const
{
    QStringList ids;
    ids.reserve(m_messages.size());
    for (const auto &message : m_messages) {
        if (!message.id.isEmpty()) {
            ids.append(message.id);
        }
    }
    return ids;
}

QStringList MessageListModel::messageIdsForDay(const QString &messageId) const
{
    const int row = indexOf(messageId);
    if (row < 0) {
        return {};
    }
    const int dayNumber = m_messages.at(row).dayNumber;
    QStringList ids;
    for (const auto &message : m_messages) {
        if (message.dayNumber == dayNumber && !message.id.isEmpty()) {
            ids.append(message.id);
        }
    }
    return ids;
}

bool MessageListModel::updateSenderAvatar(const QString &senderId, const QString &avatarLocalPath)
{
    if (senderId.isEmpty() || senderId == QStringLiteral("me")) {
        return false;
    }

    bool changed = false;
    int firstChanged = -1;
    int lastChanged = -1;
    for (int i = 0; i < m_messages.size(); ++i) {
        auto &message = m_messages[i];
        if (message.senderId != senderId || message.senderAvatarLocalPath == avatarLocalPath) {
            continue;
        }
        message.senderAvatarLocalPath = avatarLocalPath;
        changed = true;
        if (firstChanged < 0) {
            firstChanged = i;
        }
        lastChanged = i;
    }
    if (changed) {
        Q_EMIT dataChanged(index(firstChanged, 0), index(lastChanged, 0), {SenderAvatarLocalPathRole});
    }
    return changed;
}

bool MessageListModel::setMediaDownloadState(const QString &messageId, bool downloading, const QString &errorText,
                                             qint64 receivedBytes, qint64 totalBytes)
{
    const int messageIndex = indexOf(messageId);
    if (messageIndex < 0) {
        return false;
    }

    // Progress only exists while a download is running; a final (or error)
    // event resets it so a later retry starts from an empty bar.
    if (!downloading) {
        receivedBytes = 0;
        totalBytes = 0;
    }

    auto &message = m_messages[messageIndex];
    if (message.mediaDownloading == downloading && message.mediaDownloadError == errorText
        && message.mediaDownloadReceivedBytes == receivedBytes && message.mediaDownloadTotalBytes == totalBytes) {
        return false;
    }

    message.mediaDownloading = downloading;
    message.mediaDownloadError = errorText;
    message.mediaDownloadReceivedBytes = receivedBytes;
    message.mediaDownloadTotalBytes = totalBytes;
    const QModelIndex changedIndex = index(messageIndex, 0);
    Q_EMIT dataChanged(changedIndex, changedIndex, {MediaDownloadingRole, MediaDownloadErrorRole, MediaDownloadProgressRole});
    return true;
}

QString MessageListModel::unreadAnchorCandidate(int unreadCount, bool *complete) const
{
    if (complete != nullptr) {
        *complete = false;
    }
    if (unreadCount <= 0) {
        return {};
    }

    // Messages are newest-first; the unread incoming messages are by
    // definition the most recent ones. Revoked tombstones are skipped because
    // the daemon removes them from the badge count.
    QString anchor;
    int counted = 0;
    for (const auto &message : m_messages) {
        if (message.direction != static_cast<int>(whatevr::v1::MessageDirectionGadget::MessageDirection::MESSAGE_DIRECTION_INCOMING)
            || message.isRevoked) {
            continue;
        }
        anchor = message.id;
        if (++counted >= unreadCount) {
            if (complete != nullptr) {
                *complete = true;
            }
            break;
        }
    }
    return anchor;
}

QVariantList MessageListModel::applyOptimisticReaction(const QString &messageId, const QString &emoji)
{
    const int messageIndex = indexOf(messageId);
    if (messageIndex < 0) {
        return {};
    }

    auto &message = m_messages[messageIndex];
    const QVariantList previous = message.reactions;

    QVariantList updated;
    updated.reserve(previous.size() + 1);
    for (const auto &entry : previous) {
        // Drop the viewer's existing reaction; everyone else's stays put.
        if (!entry.toMap().value(QStringLiteral("fromMe")).toBool()) {
            updated.append(entry);
        }
    }
    if (!emoji.isEmpty()) {
        updated.append(QVariantMap {
            {QStringLiteral("emoji"), emoji},
            {QStringLiteral("senderId"), QString()},
            {QStringLiteral("senderName"), QString()},
            {QStringLiteral("fromMe"), true},
        });
    }

    message.reactions = updated;
    const QModelIndex changedIndex = index(messageIndex, 0);
    Q_EMIT dataChanged(changedIndex, changedIndex, {ReactionsRole});
    return previous;
}

void MessageListModel::restoreReactions(const QString &messageId, const QVariantList &reactions)
{
    const int messageIndex = indexOf(messageId);
    if (messageIndex < 0) {
        return;
    }

    m_messages[messageIndex].reactions = reactions;
    const QModelIndex changedIndex = index(messageIndex, 0);
    Q_EMIT dataChanged(changedIndex, changedIndex, {ReactionsRole});
}

QString MessageListModel::applyOptimisticEdit(const QString &messageId, const QString &newText)
{
    const int messageIndex = indexOf(messageId);
    if (messageIndex < 0) {
        return {};
    }

    auto &message = m_messages[messageIndex];
    const QString previous = message.text;
    message.text = newText;
    message.isEdited = true;
    // Drop the lazily-built preview/markup caches so the new body re-parses on
    // next access from data().
    message.previewParsed = false;
    message.fullMarkupParsed = false;

    const QModelIndex changedIndex = index(messageIndex, 0);
    // The body drives many derived roles (layout, preview, links, widths), so
    // refresh the whole row rather than a narrow role set.
    Q_EMIT dataChanged(changedIndex, changedIndex);
    return previous;
}

void MessageListModel::restoreText(const QString &messageId, const QString &oldText, bool wasEdited)
{
    const int messageIndex = indexOf(messageId);
    if (messageIndex < 0) {
        return;
    }

    auto &message = m_messages[messageIndex];
    message.text = oldText;
    message.isEdited = wasEdited;
    message.previewParsed = false;
    message.fullMarkupParsed = false;

    const QModelIndex changedIndex = index(messageIndex, 0);
    Q_EMIT dataChanged(changedIndex, changedIndex);
}

QStringList MessageListModel::uniqueIncomingSenderIds() const
{
    QSet<QString> seen;
    QStringList ids;
    for (const auto &message : m_messages) {
        if (isOutgoing(message) || message.senderId.isEmpty() || message.senderId == QStringLiteral("me") || seen.contains(message.senderId)) {
            continue;
        }
        seen.insert(message.senderId);
        ids.append(message.senderId);
    }
    return ids;
}

void MessageListModel::setGroupChat(bool groupChat)
{
    if (m_groupChat == groupChat) {
        return;
    }
    m_groupChat = groupChat;
    if (!m_messages.isEmpty()) {
        Q_EMIT dataChanged(index(0, 0), index(static_cast<int>(m_messages.size()) - 1, 0));
    }
}

bool MessageListModel::isEmpty() const
{
    return m_messages.isEmpty();
}

int MessageListModel::messageCount() const
{
    return static_cast<int>(m_messages.size());
}

QString MessageListModel::oldestMessageId() const
{
    return m_messages.isEmpty() ? QString() : m_messages.last().id;
}

bool MessageListModel::expandMessageText(const QString &messageId)
{
    const int messageIndex = indexOf(messageId);
    if (messageIndex < 0) {
        return false;
    }

    auto &message = m_messages[messageIndex];
    // textTruncated lives in the lazy cache; make sure it is populated before
    // deciding whether there is anything to expand.
    ensurePreviewParsed(message);
    if (!message.textTruncated || message.fullMarkupParsed) {
        return true;
    }

    const auto markup = parseWhatsAppMessageMarkup(message.text);
    message.layoutText = markup.layoutText;
    message.emojiOnlyCount = markup.emojiOnlyCount;
    message.hasRichText = markup.hasRichText;
    message.richText = markup.richText;
    message.fullMarkupParsed = true;
    const QString &measureText = markup.layoutText.isEmpty() ? message.text : markup.layoutText;
    std::tie(message.widestLineWidth, message.lastLineWidth) = measureLineWidths(measureText);

    const QModelIndex changedIndex = index(messageIndex, 0);
    Q_EMIT dataChanged(changedIndex,
                       changedIndex,
                       {LayoutTextRole, EmojiOnlyCountRole, HasRichTextRole, RichTextRole, WidestLineWidthRole, LastLineWidthRole});
    return true;
}

MessageListModel::MessageItem MessageListModel::fromProto(const whatevr::v1::Message &message)
{
    // Only cheap, source-derived fields are computed here. The preview collapse
    // and markup/emoji parse are deferred to ensurePreviewParsed(): fromProto
    // runs for every cached message on every chat switch, and eager parsing
    // made selectChat() stall the GUI thread.
    const QString text = plainTextFromQtRichText(message.text());
    MessageItem item {
        .id = message.id_proto(),
        .chatId = message.chatId(),
        .senderId = message.senderId(),
        .senderName = message.senderName(),
        .senderDisplayName = {},
        .senderInitials = {},
        .senderAvatarLocalPath = message.senderAvatarLocalPath(),
        .text = text,
        .layoutText = {},
        .emojiOnlyCount = 0,
        .hasRichText = false,
        .richText = {},
        .fullMarkupParsed = false,
        .previewParsed = false,
        .textPreview = {},
        .layoutTextPreview = {},
        .previewHasRichText = false,
        .previewRichText = {},
        .textTruncated = false,
        .timestampUnix = message.timestampUnix(),
        .sortSeq = message.sortSeq(),
        .timeText = {},
        .direction = static_cast<int>(message.direction()),
        .status = static_cast<int>(message.status()),
        .statusText = {},
        .mediaKind = message.mediaKind(),
        .mediaMimeType = message.mediaMimeType(),
        .mediaLocalPath = message.mediaLocalPath(),
        .mediaThumbnailLocalPath = message.mediaThumbnailLocalPath(),
        .mediaCacheKey = message.mediaCacheKey(),
        .mediaWidth = message.mediaWidth(),
        .mediaHeight = message.mediaHeight(),
        .mediaAnimated = message.mediaAnimated(),
        .isRevoked = message.isRevoked(),
        .isEdited = message.isEdited(),
        .mediaDownloading = false,
        .mediaDownloadError = {},
        .replyToMessageId = {},
        .replyToSenderId = {},
        .replyToSenderName = {},
        .replyToText = {},
        .replyToMediaKind = {},
        .replyToMediaMimeType = {},
        .replyToDirection = 0,
        .reactions = {},
    };
    const auto &replyTo = message.replyTo();
    item.replyToMessageId = replyTo.messageId();
    item.replyToSenderId = replyTo.senderId();
    item.replyToSenderName = replyTo.senderName();
    item.replyToText = plainTextFromQtRichText(replyTo.text());
    item.replyToMediaKind = replyTo.mediaKind();
    item.replyToMediaMimeType = replyTo.mediaMimeType();
    item.replyToDirection = static_cast<int>(replyTo.direction());
    for (const auto &reaction : message.reactions()) {
        item.reactions.append(QVariantMap {
            {QStringLiteral("emoji"), reaction.emoji()},
            {QStringLiteral("senderId"), reaction.senderId()},
            {QStringLiteral("senderName"), reaction.senderName()},
            {QStringLiteral("fromMe"), reaction.fromMe()},
        });
    }
    // Revoked rows arrive with cleared content; substitute the tombstone here
    // so layout measurement, previews and copy all see the displayed string.
    if (item.isRevoked) {
        item.text = i18nc("@info placeholder body for a deleted message", "This message was deleted");
    }
    item.senderDisplayName = displaySenderName(item);
    item.senderInitials = initialsForName(item.senderDisplayName);
    item.timeText = formatTime(item.timestampUnix);
    item.dayNumber = item.timestampUnix > 0
        ? static_cast<int>(QDateTime::fromSecsSinceEpoch(item.timestampUnix, QTimeZone::LocalTime).date().toJulianDay())
        : 0;
    item.statusText = statusText(item.status);
    return item;
}

void MessageListModel::ensurePreviewParsed(const MessageItem &item)
{
    if (item.previewParsed) {
        return;
    }

    const QString previewText = collapsedMessageText(item.text);
    const bool truncated = previewText != item.text;
    const auto previewMarkup = parseWhatsAppMessageMarkup(previewText);
    item.textPreview = previewText;
    item.layoutTextPreview = previewMarkup.layoutText;
    item.previewHasRichText = previewMarkup.hasRichText;
    item.previewRichText = previewMarkup.richText;
    item.textTruncated = truncated;
    if (truncated) {
        // Full markup is parsed on demand by expandMessageText().
        item.layoutText = item.text;
        item.emojiOnlyCount = 0;
        item.hasRichText = false;
        item.richText.clear();
        item.fullMarkupParsed = false;
    } else {
        item.layoutText = previewMarkup.layoutText;
        item.emojiOnlyCount = previewMarkup.emojiOnlyCount;
        item.hasRichText = previewMarkup.hasRichText;
        item.richText = previewMarkup.richText;
        item.fullMarkupParsed = true;
    }
    // Measure the collapsed (displayed) variant; expandMessageText re-measures
    // when the full text becomes the displayed one.
    const QString &measureText = item.layoutTextPreview.isEmpty() ? item.textPreview : item.layoutTextPreview;
    std::tie(item.widestLineWidth, item.lastLineWidth) = measureLineWidths(measureText);
    // Links come from the full text: the context menu must offer links the
    // collapse cut off.
    item.links = whatevr::util::extractMessageLinks(item.text);
    item.previewParsed = true;
}

void MessageListModel::transplantParsedState(const MessageItem &target, const MessageItem &source)
{
    if (!source.previewParsed && !source.fullMarkupParsed) {
        return;
    }

    target.layoutText = source.layoutText;
    target.emojiOnlyCount = source.emojiOnlyCount;
    target.hasRichText = source.hasRichText;
    target.richText = source.richText;
    target.fullMarkupParsed = source.fullMarkupParsed;
    target.previewParsed = source.previewParsed;
    target.textPreview = source.textPreview;
    target.layoutTextPreview = source.layoutTextPreview;
    target.previewHasRichText = source.previewHasRichText;
    target.previewRichText = source.previewRichText;
    target.textTruncated = source.textTruncated;
    target.widestLineWidth = source.widestLineWidth;
    target.lastLineWidth = source.lastLineWidth;
    target.links = source.links;
}

QString MessageListModel::displaySenderName(const MessageItem &message)
{
    QString name = message.senderName.trimmed();
    if (!name.isEmpty()) {
        return name;
    }
    if (!message.senderId.trimmed().isEmpty() && message.senderId != QStringLiteral("me")) {
        return message.senderId.section(QLatin1Char('@'), 0, 0);
    }
    return QStringLiteral("Unknown");
}

QString MessageListModel::initialsForName(const QString &name)
{
    const QStringList parts = name.split(QChar::Space, Qt::SkipEmptyParts);
    QString initials;
    for (const auto &part : parts) {
        initials.append(part.left(1).toUpper());
        if (initials.size() >= 2) {
            break;
        }
    }
    return initials.isEmpty() ? QStringLiteral("?") : initials;
}

QString MessageListModel::formatTime(qint64 timestampUnix)
{
    if (timestampUnix <= 0) {
        return {};
    }

    return QDateTime::fromSecsSinceEpoch(timestampUnix, QTimeZone::LocalTime).time().toString(QStringLiteral("HH:mm"));
}

QString MessageListModel::formatRelativeDate(qint64 timestampUnix)
{
    if (timestampUnix <= 0) {
        return {};
    }

    const QDate date = QDateTime::fromSecsSinceEpoch(timestampUnix, QTimeZone::LocalTime).date();
    const QDate today = QDate::currentDate();
    const qint64 daysAgo = date.daysTo(today);

    if (daysAgo == 0) {
        return i18nc("@title:row date separator for messages from today", "Today");
    }
    if (daysAgo == 1) {
        return i18nc("@title:row date separator for messages from yesterday", "Yesterday");
    }
    if (daysAgo >= 2 && daysAgo <= 6) {
        return QLocale().dayName(date.dayOfWeek(), QLocale::LongFormat);
    }
    if (date.year() == today.year()) {
        return QLocale().toString(date, i18nc("date separator without year, e.g. 14 August", "d MMMM"));
    }
    return QLocale().toString(date, i18nc("date separator with year, e.g. 14 August 2023", "d MMMM yyyy"));
}

QString MessageListModel::dateTextForRow(int row) const
{
    if (row < 0 || row >= static_cast<int>(m_messages.size())) {
        return {};
    }
    return cachedRelativeDate(m_messages.at(row));
}

QString MessageListModel::cachedRelativeDate(const MessageItem &message) const
{
    const QDate today = QDate::currentDate();
    if (m_dateTextDay != today) {
        m_dateTextByDay.clear();
        m_dateTextDay = today;
    }
    const auto it = m_dateTextByDay.constFind(message.dayNumber);
    if (it != m_dateTextByDay.constEnd()) {
        return it.value();
    }
    const QString text = formatRelativeDate(message.timestampUnix);
    m_dateTextByDay.insert(message.dayNumber, text);
    return text;
}

QString MessageListModel::statusText(int status)
{
    using whatevr::v1::MessageStatusGadget::MessageStatus;
    switch (static_cast<MessageStatus>(status)) {
    case MessageStatus::MESSAGE_STATUS_PENDING:
        return i18nc("@label message delivery status", "Sending");
    case MessageStatus::MESSAGE_STATUS_SENT:
        return i18nc("@label message delivery status", "Sent");
    case MessageStatus::MESSAGE_STATUS_DELIVERED:
        return i18nc("@label message delivery status", "Delivered");
    case MessageStatus::MESSAGE_STATUS_READ:
        return i18nc("@label message delivery status", "Read");
    case MessageStatus::MESSAGE_STATUS_FAILED:
        return i18nc("@label message delivery status", "Failed");
    case MessageStatus::MESSAGE_STATUS_UNSPECIFIED:
    default:
        break;
    }

    return {};
}

bool MessageListModel::sameMessages(const QList<MessageItem> &left, const QList<MessageItem> &right)
{
    if (left.size() != right.size()) {
        return false;
    }

    for (int i = 0; i < left.size(); ++i) {
        if (!sameMessageData(left.at(i), right.at(i))) {
            return false;
        }
    }

    return true;
}

bool MessageListModel::sameMessageOrder(const QList<MessageItem> &left, const QList<MessageItem> &right)
{
    if (left.size() != right.size()) {
        return false;
    }

    for (int i = 0; i < left.size(); ++i) {
        if (left.at(i).id != right.at(i).id) {
            return false;
        }
    }

    return true;
}

bool MessageListModel::sameMessageData(const MessageItem &left, const MessageItem &right)
{
    // Compares source fields only. Derived text state (preview/markup cache) is
    // deterministic from `text`, and comparing it would make a parsed item
    // differ from an unparsed twin, defeating the cheap dataChanged paths.
    return left.id == right.id
        && left.chatId == right.chatId
        && left.senderId == right.senderId
        && left.senderName == right.senderName
        && left.senderDisplayName == right.senderDisplayName
        && left.senderInitials == right.senderInitials
        && left.senderAvatarLocalPath == right.senderAvatarLocalPath
        && left.text == right.text
        && left.timestampUnix == right.timestampUnix
        && left.timeText == right.timeText
        && left.direction == right.direction
        && left.status == right.status
        && left.statusText == right.statusText
        && left.mediaKind == right.mediaKind
        && left.mediaMimeType == right.mediaMimeType
        && left.mediaLocalPath == right.mediaLocalPath
        && left.mediaThumbnailLocalPath == right.mediaThumbnailLocalPath
        && left.mediaCacheKey == right.mediaCacheKey
        && left.mediaWidth == right.mediaWidth
        && left.mediaHeight == right.mediaHeight
        && left.mediaAnimated == right.mediaAnimated
        && left.isRevoked == right.isRevoked
        && left.mediaDownloading == right.mediaDownloading
        && left.mediaDownloadError == right.mediaDownloadError
        && left.replyToMessageId == right.replyToMessageId
        && left.replyToSenderId == right.replyToSenderId
        && left.replyToSenderName == right.replyToSenderName
        && left.replyToText == right.replyToText
        && left.replyToMediaKind == right.replyToMediaKind
        && left.replyToMediaMimeType == right.replyToMediaMimeType
        && left.replyToDirection == right.replyToDirection
        && left.reactions == right.reactions;
}

bool MessageListModel::isOutgoing(const MessageItem &message) const
{
    return message.direction == static_cast<int>(whatevr::v1::MessageDirectionGadget::MessageDirection::MESSAGE_DIRECTION_OUTGOING);
}

bool MessageListModel::startsSenderGroup(int row) const
{
    // Newest-first storage: the chronologically previous (older) message is at
    // row + 1, and the oldest message (last index) always starts a group.
    if (row < 0 || row >= static_cast<int>(m_messages.size()) - 1) {
        return true;
    }
    const auto &message = m_messages.at(row);
    const auto &previous = m_messages.at(row + 1);
    if (isOutgoing(message) != isOutgoing(previous) || message.senderId != previous.senderId) {
        return true;
    }
    return message.timestampUnix - previous.timestampUnix > kSenderGroupGapSeconds;
}

bool MessageListModel::endsSenderGroup(int row) const
{
    // Newest-first storage: the chronologically next (newer) message is at
    // row - 1, and the newest message (index 0) always ends a group.
    if (row <= 0 || row >= static_cast<int>(m_messages.size())) {
        return true;
    }
    const auto &message = m_messages.at(row);
    const auto &next = m_messages.at(row - 1);
    if (isOutgoing(message) != isOutgoing(next) || message.senderId != next.senderId) {
        return true;
    }
    return next.timestampUnix - message.timestampUnix > kSenderGroupGapSeconds;
}

bool MessageListModel::startsDayGroup(int row) const
{
    // Newest-first storage: the chronologically previous (older) message is at
    // row + 1, and the oldest message (last index) always starts a day. A row
    // begins a new day when its local calendar day differs from the older
    // neighbour's. Comparison is a plain int (precomputed Julian day number).
    if (row < 0 || row >= static_cast<int>(m_messages.size()) - 1) {
        return true;
    }
    return m_messages.at(row).dayNumber != m_messages.at(row + 1).dayNumber;
}

int MessageListModel::indexOf(const QString &messageId) const
{
    if (messageId.isEmpty()) {
        return -1;
    }

    return m_messageIndexById.value(messageId, -1);
}

void MessageListModel::rebuildIndex()
{
    m_messageIndexById.clear();
    m_messageIndexById.reserve(m_messages.size());
    for (int i = 0; i < m_messages.size(); ++i) {
        if (!m_messages.at(i).id.isEmpty()) {
            m_messageIndexById.insert(m_messages.at(i).id, i);
        }
    }
}

void MessageListModel::emitGroupingRolesChanged(int firstRow, int lastRow)
{
    if (m_messages.isEmpty()) {
        return;
    }

    const int first = std::max(0, firstRow - 1);
    const int last = std::min(static_cast<int>(m_messages.size()) - 1, lastRow + 1);
    if (first > last) {
        return;
    }

    Q_EMIT dataChanged(index(first, 0),
                       index(last, 0),
                       {ShowSenderHeaderRole, ShowSenderAvatarRole, ShowSenderGutterRole, GroupStartRole, GroupEndRole, DateSeparatorTextRole});
}
