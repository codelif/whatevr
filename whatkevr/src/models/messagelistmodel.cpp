#include "messagelistmodel.h"

#include <QDateTime>
#include <QTimeZone>

#include <KLocalizedString>

#include "whatevr/v1/whatevr.qpb.h"

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
    case MediaWidthRole:
        return message.mediaWidth;
    case MediaHeightRole:
        return message.mediaHeight;
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
        {TextRole, "text"},
        {TimestampUnixRole, "timestampUnix"},
        {TimeTextRole, "timeText"},
        {DirectionRole, "direction"},
        {StatusRole, "status"},
        {StatusTextRole, "statusText"},
        {IsOutgoingRole, "isOutgoing"},
        {MediaMimeTypeRole, "mediaMimeType"},
        {MediaLocalPathRole, "mediaLocalPath"},
        {MediaWidthRole, "mediaWidth"},
        {MediaHeightRole, "mediaHeight"},
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

    beginResetModel();
    m_messages = std::move(next);
    endResetModel();
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

void MessageListModel::prependMessages(const QList<whatevr::v1::Message> &messages)
{
    if (messages.isEmpty()) {
        return;
    }

    QList<MessageItem> toInsert;
    toInsert.reserve(messages.size());
    for (const auto &msg : messages) {
        const MessageItem item = fromProto(msg);
        if (indexOf(item.id) < 0) {
            toInsert.append(item);
        }
    }
    if (toInsert.isEmpty()) {
        return;
    }

    std::sort(toInsert.begin(), toInsert.end(), [](const MessageItem &l, const MessageItem &r) {
        if (l.timestampUnix != r.timestampUnix) {
            return l.timestampUnix < r.timestampUnix;
        }
        return l.id < r.id;
    });

    beginInsertRows(QModelIndex(), 0, toInsert.size() - 1);
    for (int i = 0; i < toInsert.size(); ++i) {
        m_messages.insert(i, toInsert.at(i));
    }
    endInsertRows();
}

bool MessageListModel::isEmpty() const
{
    return m_messages.isEmpty();
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
        .text = message.text(),
        .timestampUnix = message.timestampUnix(),
        .direction = static_cast<int>(message.direction()),
        .status = static_cast<int>(message.status()),
        .mediaMimeType = message.mediaMimeType(),
        .mediaLocalPath = message.mediaLocalPath(),
        .mediaWidth = message.mediaWidth(),
        .mediaHeight = message.mediaHeight(),
    };
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
        return QString();
    }
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
