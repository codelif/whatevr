#include "chatlistmodel.h"

#include <QCollator>
#include <QDateTime>
#include <QFileInfo>
#include <QScopeGuard>
#include <QSettings>
#include <QVariantMap>

#include "whatevr/v1/whatevr.qpb.h"

#include "richtext.h"
#include "settings.h"

namespace {
constexpr auto kDraftsKey = "settings/drafts";
}

ChatListModel::ChatListModel(QObject *parent)
    : QAbstractListModel(parent)
{
    loadPersistedDrafts();
}

void ChatListModel::loadPersistedDrafts()
{
    const Settings *settings = Settings::instance();
    if (!settings || !settings->persistDrafts()) {
        return;
    }
    const QVariantMap stored = QSettings().value(QLatin1String(kDraftsKey)).toMap();
    const qint64 now = QDateTime::currentSecsSinceEpoch();
    for (auto it = stored.constBegin(); it != stored.constEnd(); ++it) {
        const QString text = it.value().toString();
        if (!text.isEmpty()) {
            m_drafts.insert(it.key(), Draft {text, now});
        }
    }
}

void ChatListModel::clearDrafts()
{
    m_drafts.clear();
    // Remove the stored key unconditionally: drafts may have been persisted
    // while the setting was on, even if it is off now.
    QSettings().remove(QLatin1String(kDraftsKey));
}

void ChatListModel::savePersistedDrafts() const
{
    const Settings *settings = Settings::instance();
    if (!settings || !settings->persistDrafts()) {
        return;
    }
    QVariantMap map;
    for (auto it = m_drafts.constBegin(); it != m_drafts.constEnd(); ++it) {
        map.insert(it.key(), it.value().text);
    }
    QSettings().setValue(QLatin1String(kDraftsKey), map);
}

int ChatListModel::rowCount(const QModelIndex &parent) const
{
    if (parent.isValid()) {
        return 0;
    }
    return static_cast<int>(m_chats.size());
}

QVariant ChatListModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= m_chats.size()) {
        return {};
    }

    const auto &chat = m_chats.at(index.row());
    switch (role) {
    case IdRole:
        return chat.id;
    case NameRole:
        return chat.displayName;
    case LastMessageRole:
        return chat.lastMessage;
    case LastMessageTimeUnixRole:
        return chat.lastMessageTimeUnix;
    case LastMessageDirectionRole:
        return chat.lastMessageDirection;
    case LastMessageStatusRole:
        return chat.lastMessageStatus;
    case UnreadCountRole:
        return chat.unreadCount;
    case IsGroupRole:
        return chat.isGroup;
    case IsPinnedRole:
        return chat.isPinned;
    case PinnedOrderRole:
        return chat.pinnedOrder;
    case IsArchivedRole:
        return chat.isArchived;
    case IsMutedRole:
        return chat.isMuted;
    case SectionRole:
        return chat.isArchived ? QStringLiteral("archived") : QStringLiteral("active");
    case AvatarLocalPathRole:
        return chat.avatarLocalPath;
    case InitialsRole:
        return chat.initials;
    case IsTypingRole:
        return chat.isTyping;
    case HasDraftRole:
        return chat.hasDraft;
    case DraftTextRole:
        return chat.draftText;
    default:
        return {};
    }
}

QHash<int, QByteArray> ChatListModel::roleNames() const
{
    return {
        {IdRole, "chatId"},
        {NameRole, "name"},
        {LastMessageRole, "lastMessage"},
        {LastMessageTimeUnixRole, "lastMessageTimeUnix"},
        {LastMessageDirectionRole, "lastMessageDirection"},
        {LastMessageStatusRole, "lastMessageStatus"},
        {UnreadCountRole, "unreadCount"},
        {IsGroupRole, "isGroup"},
        {IsPinnedRole, "isPinned"},
        {PinnedOrderRole, "pinnedOrder"},
        {IsArchivedRole, "isArchived"},
        {IsMutedRole, "isMuted"},
        {SectionRole, "chatSection"},
        {AvatarLocalPathRole, "avatarLocalPath"},
        {InitialsRole, "initials"},
        {IsTypingRole, "isTyping"},
        {HasDraftRole, "hasDraft"},
        {DraftTextRole, "draftText"},
    };
}

void ChatListModel::replaceChats(const QList<whatevr::v1::Chat> &chats)
{
    const int previousArchived = archivedCount();
    auto archivedGuard = qScopeGuard([this, previousArchived] {
        if (previousArchived != archivedCount()) {
            Q_EMIT archivedCountChanged();
        }
    });

    QList<ChatItem> next;
    next.reserve(chats.size());
    for (const auto &chat : chats) {
        ChatItem item = fromProto(chat);
        const int existingIndex = indexOf(item.id);
        if (existingIndex >= 0) {
            item.isTyping = m_chats.at(existingIndex).isTyping;
        }
        applyDraft(item);
        next.append(std::move(item));
    }

    std::sort(next.begin(), next.end(), sortBefore);

    if (sameSortOrder(m_chats, next)) {
        for (int i = 0; i < next.size(); ++i) {
            if (sameChatData(m_chats.at(i), next.at(i))) {
                continue;
            }
            m_chats[i] = next.at(i);
            const QModelIndex changedIndex = index(i, 0);
            Q_EMIT dataChanged(changedIndex, changedIndex);
        }
        return;
    }

    beginResetModel();
    m_chats = std::move(next);
    rebuildIndex();
    endResetModel();
}

void ChatListModel::upsertChat(const whatevr::v1::Chat &chat, const QString &previousChatId)
{
    const int previousArchived = archivedCount();
    auto archivedGuard = qScopeGuard([this, previousArchived] {
        if (previousArchived != archivedCount()) {
            Q_EMIT archivedCountChanged();
        }
    });

    bool preservedTyping = false;
    bool hasPreservedTyping = false;

    if (!previousChatId.isEmpty()) {
        const int previousIndex = indexOf(previousChatId);
        if (previousIndex >= 0) {
            preservedTyping = m_chats.at(previousIndex).isTyping;
            hasPreservedTyping = true;
            beginRemoveRows(QModelIndex(), previousIndex, previousIndex);
            m_chats.removeAt(previousIndex);
            endRemoveRows();
            m_chatIndexById.remove(previousChatId);
            reindexRange(previousIndex, static_cast<int>(m_chats.size()) - 1);
        }
    }

    ChatItem item = fromProto(chat);
    const int existingIndex = indexOf(item.id);
    if (existingIndex >= 0) {
        item.isTyping = m_chats.at(existingIndex).isTyping;
    } else if (hasPreservedTyping) {
        item.isTyping = preservedTyping;
    }
    applyDraft(item);

    if (existingIndex < 0) {
        const int insertIndex = sortedInsertIndex(item);
        beginInsertRows(QModelIndex(), insertIndex, insertIndex);
        m_chats.insert(insertIndex, item);
        reindexRange(insertIndex, static_cast<int>(m_chats.size()) - 1);
        endInsertRows();
        return;
    }

    const int insertIndex = sortedInsertIndex(item, existingIndex);
    if (insertIndex == existingIndex) {
        if (sameChatData(m_chats.at(existingIndex), item)) {
            return;
        }
        m_chats[existingIndex] = item;
        const QModelIndex changedIndex = index(existingIndex, 0);
        Q_EMIT dataChanged(changedIndex, changedIndex);
        return;
    }

    const int destinationRow = insertIndex > existingIndex ? insertIndex + 1 : insertIndex;
    beginMoveRows(QModelIndex(), existingIndex, existingIndex, QModelIndex(), destinationRow);
    m_chats.move(existingIndex, insertIndex);
    endMoveRows();
    reindexRange(std::min(existingIndex, insertIndex), std::max(existingIndex, insertIndex));

    if (!sameChatData(m_chats.at(insertIndex), item)) {
        m_chats[insertIndex] = item;
        const QModelIndex changedIndex = index(insertIndex, 0);
        Q_EMIT dataChanged(changedIndex, changedIndex);
    }
}

bool ChatListModel::removeChat(const QString &chatId)
{
    const int chatIndex = indexOf(chatId);
    if (chatIndex < 0) {
        return false;
    }
    const int previousArchived = archivedCount();
    beginRemoveRows(QModelIndex(), chatIndex, chatIndex);
    m_chats.removeAt(chatIndex);
    endRemoveRows();
    m_chatIndexById.remove(chatId);
    reindexRange(chatIndex, static_cast<int>(m_chats.size()) - 1);
    if (previousArchived != archivedCount()) {
        Q_EMIT archivedCountChanged();
    }
    return true;
}

bool ChatListModel::updateAvatar(const QString &chatId, const QString &avatarLocalPath)
{
    const int chatIndex = indexOf(chatId);
    if (chatIndex < 0 || m_chats.at(chatIndex).avatarLocalPath == avatarLocalPath) {
        return false;
    }
    m_chats[chatIndex].avatarLocalPath = avatarLocalPath;
    const QModelIndex changedIndex = index(chatIndex, 0);
    Q_EMIT dataChanged(changedIndex, changedIndex, {AvatarLocalPathRole});
    return true;
}

bool ChatListModel::setChatTyping(const QString &chatId, bool typing)
{
    const int chatIndex = indexOf(chatId);
    if (chatIndex < 0 || m_chats.at(chatIndex).isTyping == typing) {
        return false;
    }
    m_chats[chatIndex].isTyping = typing;
    const QModelIndex changedIndex = index(chatIndex, 0);
    Q_EMIT dataChanged(changedIndex, changedIndex, {IsTypingRole});
    return true;
}

bool ChatListModel::setChatPinnedLocal(const QString &chatId, bool pinned)
{
    const int i = indexOf(chatId);
    if (i < 0) {
        return pinned;
    }
    ChatItem updated = m_chats.at(i);
    const bool previous = updated.isPinned;
    if (previous == pinned) {
        return previous;
    }
    updated.isPinned = pinned;
    // Mirror the daemon: a fresh pin sorts above older pins, unpin clears order.
    updated.pinnedOrder = pinned ? static_cast<quint32>(QDateTime::currentSecsSinceEpoch()) : 0;
    repositionMutatedChat(i, updated);
    return previous;
}

bool ChatListModel::setChatArchivedLocal(const QString &chatId, bool archived)
{
    const int i = indexOf(chatId);
    if (i < 0) {
        return archived;
    }
    ChatItem updated = m_chats.at(i);
    const bool previous = updated.isArchived;
    if (previous == archived) {
        return previous;
    }
    updated.isArchived = archived;
    // WhatsApp auto-unpins an archived chat; mirror that locally.
    if (archived && updated.isPinned) {
        updated.isPinned = false;
        updated.pinnedOrder = 0;
    }
    repositionMutatedChat(i, updated);
    return previous;
}

bool ChatListModel::setChatMutedLocal(const QString &chatId, bool muted)
{
    const int i = indexOf(chatId);
    if (i < 0) {
        return muted;
    }
    ChatItem updated = m_chats.at(i);
    const bool previous = updated.isMuted;
    if (previous == muted) {
        return previous;
    }
    updated.isMuted = muted;
    // Muting does not affect sort order, so this just rewrites the row in place.
    repositionMutatedChat(i, updated);
    return previous;
}

void ChatListModel::repositionMutatedChat(int existingIndex, const ChatItem &updated)
{
    const int previousArchived = archivedCount();
    auto archivedGuard = qScopeGuard([this, previousArchived] {
        if (previousArchived != archivedCount()) {
            Q_EMIT archivedCountChanged();
        }
    });

    const int insertIndex = sortedInsertIndex(updated, existingIndex);
    if (insertIndex == existingIndex) {
        m_chats[existingIndex] = updated;
        const QModelIndex changedIndex = index(existingIndex, 0);
        Q_EMIT dataChanged(changedIndex, changedIndex);
        return;
    }

    const int destinationRow = insertIndex > existingIndex ? insertIndex + 1 : insertIndex;
    beginMoveRows(QModelIndex(), existingIndex, existingIndex, QModelIndex(), destinationRow);
    m_chats.move(existingIndex, insertIndex);
    endMoveRows();
    reindexRange(std::min(existingIndex, insertIndex), std::max(existingIndex, insertIndex));

    m_chats[insertIndex] = updated;
    const QModelIndex changedIndex = index(insertIndex, 0);
    Q_EMIT dataChanged(changedIndex, changedIndex);
}

void ChatListModel::setChatDraft(const QString &chatId, const QString &text)
{
    if (chatId.isEmpty()) {
        return;
    }

    const QString trimmed = text.trimmed();
    const bool had = m_drafts.contains(chatId);
    if (trimmed.isEmpty()) {
        if (!had) {
            return;
        }
        m_drafts.remove(chatId);
    } else {
        if (had && m_drafts.value(chatId).text == text) {
            return;
        }
        // Stamp "now" so a (re)created draft floats to the top of its section.
        m_drafts.insert(chatId, Draft {text, QDateTime::currentSecsSinceEpoch()});
    }

    savePersistedDrafts();

    const int chatIndex = indexOf(chatId);
    if (chatIndex < 0) {
        // The chat is not in the list yet; applyDraft() will pick it up when it
        // is inserted via upsertChat/replaceChats.
        return;
    }

    // m_chats[chatIndex] still holds its old (in-order) draft state, so the
    // binary search in sortedInsertIndex finds the new position correctly —
    // mirrors the move pattern in upsertChat().
    ChatItem updated = m_chats.at(chatIndex);
    applyDraft(updated);
    const QList<int> roles {HasDraftRole, DraftTextRole, LastMessageRole};

    const int insertIndex = sortedInsertIndex(updated, chatIndex);
    if (insertIndex == chatIndex) {
        m_chats[chatIndex] = std::move(updated);
        const QModelIndex changedIndex = index(chatIndex, 0);
        Q_EMIT dataChanged(changedIndex, changedIndex, roles);
        return;
    }

    const int destinationRow = insertIndex > chatIndex ? insertIndex + 1 : insertIndex;
    beginMoveRows(QModelIndex(), chatIndex, chatIndex, QModelIndex(), destinationRow);
    m_chats.move(chatIndex, insertIndex);
    endMoveRows();
    reindexRange(std::min(chatIndex, insertIndex), std::max(chatIndex, insertIndex));
    m_chats[insertIndex] = std::move(updated);
    const QModelIndex changedIndex = index(insertIndex, 0);
    Q_EMIT dataChanged(changedIndex, changedIndex, roles);
}

QString ChatListModel::chatDraft(const QString &chatId) const
{
    return m_drafts.value(chatId).text;
}

void ChatListModel::applyDraft(ChatItem &item) const
{
    const auto it = m_drafts.constFind(item.id);
    if (it != m_drafts.constEnd()) {
        item.hasDraft = true;
        item.draftText = it->text;
        item.draftTimeUnix = it->timeUnix;
    } else {
        item.hasDraft = false;
        item.draftText.clear();
        item.draftTimeUnix = 0;
    }
}

QString ChatListModel::chatName(const QString &chatId) const
{
    const int index = indexOf(chatId);
    if (index < 0) {
        return {};
    }
    return displayName(m_chats.at(index));
}

QString ChatListModel::chatAvatarLocalPath(const QString &chatId) const
{
    const int index = indexOf(chatId);
    if (index < 0) {
        return {};
    }
    return m_chats.at(index).avatarLocalPath;
}

bool ChatListModel::chatIsGroup(const QString &chatId) const
{
    const int index = indexOf(chatId);
    if (index < 0) {
        return false;
    }
    return m_chats.at(index).isGroup;
}

int ChatListModel::chatUnreadCount(const QString &chatId) const
{
    const int index = indexOf(chatId);
    if (index < 0) {
        return 0;
    }
    return m_chats.at(index).unreadCount;
}

bool ChatListModel::chatHistoryExhausted(const QString &chatId) const
{
    const int index = indexOf(chatId);
    if (index < 0) {
        return false;
    }
    return m_chats.at(index).historyExhausted;
}

int ChatListModel::indexOf(const QString &chatId) const
{
    return m_chatIndexById.value(chatId, -1);
}

bool ChatListModel::isEmpty() const
{
    return m_chats.isEmpty();
}

int ChatListModel::archivedCount() const
{
    int count = 0;
    for (const auto &chat : m_chats) {
        if (chat.isArchived) {
            ++count;
        }
    }
    return count;
}

ChatListModel::ChatItem ChatListModel::fromProto(const whatevr::v1::Chat &chat)
{
    // Resolve mute expiry once here so the UI never re-evaluates it. mute_end is
    // unix millis; -1 = forever, 0 = not muted.
    const qint64 muteEnd = chat.muteEndTimestamp();
    const bool muted = chat.isMuted()
        && (muteEnd == -1 || muteEnd > QDateTime::currentMSecsSinceEpoch());

    ChatItem item {
        .id = chat.id_proto(),
        .name = chat.name(),
        .displayName = {},
        .initials = {},
        .lastMessage = whatevr::util::plainTextFromQtRichText(chat.lastMessage()),
        .lastMessageTimeUnix = chat.lastMessageTimeUnix(),
        .lastMessageDirection = static_cast<int>(chat.lastMessageDirection()),
        .lastMessageStatus = static_cast<int>(chat.lastMessageStatus()),
        .unreadCount = chat.unreadCount(),
        .isGroup = chat.isGroup(),
        .isPinned = chat.isPinned(),
        .pinnedOrder = chat.pinnedOrder(),
        .isArchived = chat.isArchived(),
        .isMuted = muted,
        .historyExhausted = chat.historyExhausted(),
        .updatedAtUnix = chat.updatedAtUnix(),
        .avatarLocalPath = chat.avatarLocalPath(),
        .isTyping = false,
        .hasDraft = false,
        .draftText = {},
        .draftTimeUnix = 0,
    };
    item.displayName = displayName(item);
    item.initials = initialsForName(item.displayName);
    return item;
}

QString ChatListModel::displayName(const ChatItem &chat)
{
    if (!chat.name.trimmed().isEmpty()) {
        return chat.name.trimmed();
    }
    if (!chat.id.trimmed().isEmpty()) {
        return chat.id.section(QLatin1Char('@'), 0, 0);
    }
    return QStringLiteral("Unknown chat");
}

QString ChatListModel::initialsForName(const QString &name)
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

bool ChatListModel::sameSortOrder(const QList<ChatItem> &left, const QList<ChatItem> &right)
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

bool ChatListModel::sameChatData(const ChatItem &left, const ChatItem &right)
{
    return left.id == right.id
        && left.updatedAtUnix == right.updatedAtUnix
        && left.name == right.name
        && left.displayName == right.displayName
        && left.initials == right.initials
        && left.lastMessage == right.lastMessage
        && left.lastMessageTimeUnix == right.lastMessageTimeUnix
        && left.lastMessageDirection == right.lastMessageDirection
        && left.lastMessageStatus == right.lastMessageStatus
        && left.unreadCount == right.unreadCount
        && left.isGroup == right.isGroup
        && left.isPinned == right.isPinned
        && left.pinnedOrder == right.pinnedOrder
        && left.isArchived == right.isArchived
        && left.isMuted == right.isMuted
        && left.historyExhausted == right.historyExhausted
        && left.avatarLocalPath == right.avatarLocalPath
        && left.isTyping == right.isTyping
        && left.hasDraft == right.hasDraft
        && left.draftText == right.draftText
        && left.draftTimeUnix == right.draftTimeUnix;
}

bool ChatListModel::sortBefore(const ChatItem &left, const ChatItem &right)
{
    // Archived chats float above every non-archived chat (the collapsible
    // "Archived" section lives at the top of the list).
    if (left.isArchived != right.isArchived) {
        return left.isArchived;
    }
    if (left.isPinned != right.isPinned) {
        return left.isPinned;
    }
    if (left.isPinned && left.pinnedOrder != right.pinnedOrder) {
        return left.pinnedOrder > right.pinnedOrder;
    }
    // A draft acts like a brand-new message: order by the draft time when set
    // (always >= last-message time), so an unsent draft floats to the top of its
    // section without disturbing the real last-message timestamp underneath.
    const qint64 leftTime = left.hasDraft ? qMax(left.draftTimeUnix, left.lastMessageTimeUnix)
                                          : left.lastMessageTimeUnix;
    const qint64 rightTime = right.hasDraft ? qMax(right.draftTimeUnix, right.lastMessageTimeUnix)
                                            : right.lastMessageTimeUnix;
    if (leftTime != rightTime) {
        return leftTime > rightTime;
    }
    return left.displayName.localeAwareCompare(right.displayName) < 0;
}

int ChatListModel::sortedInsertIndex(const ChatItem &item, int excludingIndex) const
{
    // m_chats stays sorted (the row being upserted still holds its old,
    // in-order data), so a binary search finds the insertion point. The result
    // is in the coordinate space of a list without the excluded row, matching
    // the linear scan this replaces.
    const auto it = std::upper_bound(m_chats.cbegin(), m_chats.cend(), item, sortBefore);
    int insertIndex = static_cast<int>(it - m_chats.cbegin());
    if (excludingIndex >= 0 && excludingIndex < insertIndex) {
        --insertIndex;
    }
    return insertIndex;
}

void ChatListModel::sortChats()
{
    std::sort(m_chats.begin(), m_chats.end(), sortBefore);
    rebuildIndex();
}

void ChatListModel::rebuildIndex()
{
    m_chatIndexById.clear();
    m_chatIndexById.reserve(m_chats.size());
    reindexRange(0, static_cast<int>(m_chats.size()) - 1);
}

// reindexRange refreshes the id→row map for rows whose positions shifted,
// avoiding a full rebuild on every single-row insert/remove/move.
void ChatListModel::reindexRange(int firstRow, int lastRow)
{
    firstRow = std::max(firstRow, 0);
    lastRow = std::min(lastRow, static_cast<int>(m_chats.size()) - 1);
    for (int i = firstRow; i <= lastRow; ++i) {
        if (!m_chats.at(i).id.isEmpty()) {
            m_chatIndexById.insert(m_chats.at(i).id, i);
        }
    }
}
