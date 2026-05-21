#include "chatlistmodel.h"

#include <QCollator>
#include <QFileInfo>

#include "whatevr/v1/whatevr.qpb.h"

ChatListModel::ChatListModel(QObject *parent)
    : QAbstractListModel(parent)
{
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
        return displayName(chat);
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
    case AvatarLocalPathRole:
        return chat.avatarLocalPath;
    case InitialsRole:
        return initialsForName(displayName(chat));
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
        {AvatarLocalPathRole, "avatarLocalPath"},
        {InitialsRole, "initials"},
    };
}

void ChatListModel::replaceChats(const QList<whatevr::v1::Chat> &chats)
{
    QList<ChatItem> next;
    next.reserve(chats.size());
    for (const auto &chat : chats) {
        next.append(fromProto(chat));
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
    endResetModel();
}

void ChatListModel::upsertChat(const whatevr::v1::Chat &chat, const QString &previousChatId)
{
    if (!previousChatId.isEmpty()) {
        const int previousIndex = indexOf(previousChatId);
        if (previousIndex >= 0) {
            beginRemoveRows(QModelIndex(), previousIndex, previousIndex);
            m_chats.removeAt(previousIndex);
            endRemoveRows();
        }
    }

    const ChatItem item = fromProto(chat);
    const int existingIndex = indexOf(item.id);

    if (existingIndex < 0) {
        const int insertIndex = sortedInsertIndex(item);
        beginInsertRows(QModelIndex(), insertIndex, insertIndex);
        m_chats.insert(insertIndex, item);
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

    if (!sameChatData(m_chats.at(insertIndex), item)) {
        m_chats[insertIndex] = item;
        const QModelIndex changedIndex = index(insertIndex, 0);
        Q_EMIT dataChanged(changedIndex, changedIndex);
    }
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

int ChatListModel::indexOf(const QString &chatId) const
{
    for (int i = 0; i < m_chats.size(); ++i) {
        if (m_chats.at(i).id == chatId) {
            return i;
        }
    }
    return -1;
}

bool ChatListModel::isEmpty() const
{
    return m_chats.isEmpty();
}

ChatListModel::ChatItem ChatListModel::fromProto(const whatevr::v1::Chat &chat)
{
    return ChatItem {
        .id = chat.id_proto(),
        .name = chat.name(),
        .lastMessage = chat.lastMessage(),
        .lastMessageTimeUnix = chat.lastMessageTimeUnix(),
        .lastMessageDirection = static_cast<int>(chat.lastMessageDirection()),
        .lastMessageStatus = static_cast<int>(chat.lastMessageStatus()),
        .unreadCount = chat.unreadCount(),
        .isGroup = chat.isGroup(),
        .avatarLocalPath = chat.avatarLocalPath(),
    };
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
        && left.name == right.name
        && left.lastMessage == right.lastMessage
        && left.lastMessageTimeUnix == right.lastMessageTimeUnix
        && left.lastMessageDirection == right.lastMessageDirection
        && left.lastMessageStatus == right.lastMessageStatus
        && left.unreadCount == right.unreadCount
        && left.isGroup == right.isGroup
        && left.avatarLocalPath == right.avatarLocalPath;
}

bool ChatListModel::sortBefore(const ChatItem &left, const ChatItem &right)
{
    if (left.lastMessageTimeUnix != right.lastMessageTimeUnix) {
        return left.lastMessageTimeUnix > right.lastMessageTimeUnix;
    }
    return displayName(left).localeAwareCompare(displayName(right)) < 0;
}

int ChatListModel::sortedInsertIndex(const ChatItem &item, int excludingIndex) const
{
    int insertIndex = 0;
    for (int i = 0; i < m_chats.size(); ++i) {
        if (i == excludingIndex) {
            continue;
        }
        if (sortBefore(item, m_chats.at(i))) {
            break;
        }
        ++insertIndex;
    }
    return insertIndex;
}

void ChatListModel::sortChats()
{
    std::sort(m_chats.begin(), m_chats.end(), sortBefore);
}
