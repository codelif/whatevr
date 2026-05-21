#include "messagelistmodel.h"

#include <QDateTime>
#include <QSet>
#include <QTimeZone>

#include <KLocalizedString>

#include "whatevr/v1/whatevr.qpb.h"

namespace {
constexpr qint64 kSenderGroupGapSeconds = 5 * 60;
}

MessageListModel::MessageListModel(QObject *parent)
    : QAbstractListModel(parent)
{
}

int MessageListModel::rowCount(const QModelIndex &parent) const
{
    if (parent.isValid()) {
        return 0;
    }
    return m_messages.size();
}

QVariant MessageListModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= m_messages.size()) {
        return QVariant();
    }

    const auto &message = m_messages.at(index.row());
    switch (role) {
    case IdRole:
        return message.id;
    case ChatIdRole:
        return message.chatId;
    case SenderIdRole:
        return message.senderId;
    case SenderNameRole:
        return displaySenderName(message);
    case SenderAvatarLocalPathRole:
        return message.senderAvatarLocalPath;
    case SenderInitialsRole:
        return initialsForName(displaySenderName(message));
    case TextRole:
        return message.text;
    case TimestampUnixRole:
        return message.timestampUnix;
    case TimeTextRole:
        return formatTime(message.timestampUnix);
    case DirectionRole:
        return message.direction;
    case StatusRole:
        return message.status;
    case StatusTextRole:
        return statusText(message.status);
    case IsOutgoingRole:
        return message.direction == static_cast<int>(whatevr::v1::MessageDirectionGadget::MessageDirection::MESSAGE_DIRECTION_OUTGOING);
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
    default:
        return QVariant();
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
        {TimestampUnixRole, "timestampUnix"},
        {TimeTextRole, "timeText"},
        {DirectionRole, "direction"},
        {StatusRole, "status"},
        {StatusTextRole, "statusText"},
        {IsOutgoingRole, "isOutgoing"},
        {MediaMimeTypeRole, "mediaMimeType"},
        {MediaLocalPathRole, "mediaLocalPath"},
        {MediaThumbnailLocalPathRole, "mediaThumbnailLocalPath"},
        {MediaWidthRole, "mediaWidth"},
        {MediaHeightRole, "mediaHeight"},
        {ShowSenderHeaderRole, "showSenderHeader"},
        {ShowSenderAvatarRole, "showSenderAvatar"},
        {ShowSenderGutterRole, "showSenderGutter"},
        {GroupStartRole, "groupStart"},
        {GroupEndRole, "groupEnd"},
    };
}

void MessageListModel::replaceMessages(const QList<whatevr::v1::Message> &messages)
{
    QList<MessageItem> next;
    next.reserve(messages.size());
    for (const auto &message : messages) {
        next.append(fromProto(message));
    }

    std::sort(next.begin(), next.end(), [](const MessageItem &left, const MessageItem &right) {
        if (left.timestampUnix != right.timestampUnix) {
            return left.timestampUnix < right.timestampUnix;
        }
        return left.id < right.id;
    });

    if (sameMessages(m_messages, next)) {
        return;
    }

    if (sameMessageOrder(m_messages, next)) {
        for (int i = 0; i < next.size(); ++i) {
            if (sameMessageData(m_messages.at(i), next.at(i))) {
                continue;
            }
            m_messages[i] = next.at(i);
            const QModelIndex changedIndex = index(i, 0);
            Q_EMIT dataChanged(changedIndex, changedIndex);
        }
        return;
    }

    beginResetModel();
    m_messages = std::move(next);
    endResetModel();
}

void MessageListModel::prependMessages(const QList<whatevr::v1::Message> &messages)
{
    QList<MessageItem> older;
    older.reserve(messages.size());
    for (const auto &message : messages) {
        const MessageItem item = fromProto(message);
        if (indexOf(item.id) < 0) {
            older.append(item);
        }
    }

    if (older.isEmpty()) {
        return;
    }

    std::sort(older.begin(), older.end(), [](const MessageItem &left, const MessageItem &right) {
        if (left.timestampUnix != right.timestampUnix) {
            return left.timestampUnix < right.timestampUnix;
        }
        return left.id < right.id;
    });

    beginInsertRows(QModelIndex(), 0, older.size() - 1);
    for (int i = older.size() - 1; i >= 0; --i) {
        m_messages.prepend(older.at(i));
    }
    endInsertRows();
}

void MessageListModel::clear()
{
    if (m_messages.isEmpty()) {
        return;
    }

    beginResetModel();
    m_messages.clear();
    endResetModel();
}

void MessageListModel::upsertMessage(const whatevr::v1::Message &message)
{
    const MessageItem item = fromProto(message);
    const int existingIndex = indexOf(item.id);

    if (existingIndex >= 0) {
        m_messages[existingIndex] = item;
        const QModelIndex changedIndex = index(existingIndex, 0);
        Q_EMIT dataChanged(changedIndex, changedIndex);
        return;
    }

    int insertAt = m_messages.size();
    while (insertAt > 0 && m_messages.at(insertAt - 1).timestampUnix > item.timestampUnix) {
        --insertAt;
    }

    beginInsertRows(QModelIndex(), insertAt, insertAt);
    m_messages.insert(insertAt, item);
    endInsertRows();
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
        Q_EMIT dataChanged(index(0, 0), index(m_messages.size() - 1, 0));
    }
}

bool MessageListModel::isEmpty() const
{
    return m_messages.isEmpty();
}

int MessageListModel::messageCount() const
{
    return m_messages.size();
}

QString MessageListModel::oldestMessageId() const
{
    return m_messages.isEmpty() ? QString() : m_messages.first().id;
}

MessageListModel::MessageItem MessageListModel::fromProto(const whatevr::v1::Message &message)
{
    return MessageItem {
        .id = message.id_proto(),
        .chatId = message.chatId(),
        .senderId = message.senderId(),
        .senderName = message.senderName(),
        .senderAvatarLocalPath = message.senderAvatarLocalPath(),
        .text = message.text(),
        .timestampUnix = message.timestampUnix(),
        .direction = static_cast<int>(message.direction()),
        .status = static_cast<int>(message.status()),
        .mediaMimeType = message.mediaMimeType(),
        .mediaLocalPath = message.mediaLocalPath(),
        .mediaThumbnailLocalPath = message.mediaThumbnailLocalPath(),
        .mediaWidth = message.mediaWidth(),
        .mediaHeight = message.mediaHeight(),
    };
}

QString MessageListModel::displaySenderName(const MessageItem &message)
{
    const QString name = message.senderName.trimmed();
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
        return QString();
    }

    return QDateTime::fromSecsSinceEpoch(timestampUnix, QTimeZone::LocalTime).time().toString(QStringLiteral("HH:mm"));
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

    return QString();
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
    return left.id == right.id
        && left.chatId == right.chatId
        && left.senderId == right.senderId
        && left.senderName == right.senderName
        && left.senderAvatarLocalPath == right.senderAvatarLocalPath
        && left.text == right.text
        && left.timestampUnix == right.timestampUnix
        && left.direction == right.direction
        && left.status == right.status
        && left.mediaMimeType == right.mediaMimeType
        && left.mediaLocalPath == right.mediaLocalPath
        && left.mediaThumbnailLocalPath == right.mediaThumbnailLocalPath
        && left.mediaWidth == right.mediaWidth
        && left.mediaHeight == right.mediaHeight;
}

bool MessageListModel::isOutgoing(const MessageItem &message) const
{
    return message.direction == static_cast<int>(whatevr::v1::MessageDirectionGadget::MessageDirection::MESSAGE_DIRECTION_OUTGOING);
}

bool MessageListModel::startsSenderGroup(int row) const
{
    if (row <= 0 || row >= m_messages.size()) {
        return true;
    }
    const auto &message = m_messages.at(row);
    const auto &previous = m_messages.at(row - 1);
    if (isOutgoing(message) != isOutgoing(previous) || message.senderId != previous.senderId) {
        return true;
    }
    return message.timestampUnix - previous.timestampUnix > kSenderGroupGapSeconds;
}

bool MessageListModel::endsSenderGroup(int row) const
{
    if (row < 0 || row >= m_messages.size() - 1) {
        return true;
    }
    const auto &message = m_messages.at(row);
    const auto &next = m_messages.at(row + 1);
    if (isOutgoing(message) != isOutgoing(next) || message.senderId != next.senderId) {
        return true;
    }
    return next.timestampUnix - message.timestampUnix > kSenderGroupGapSeconds;
}

int MessageListModel::indexOf(const QString &messageId) const
{
    if (messageId.isEmpty()) {
        return -1;
    }

    for (int i = 0; i < m_messages.size(); ++i) {
        if (m_messages.at(i).id == messageId) {
            return i;
        }
    }
    return -1;
}
