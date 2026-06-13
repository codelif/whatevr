#include "starredmessagesmodel.h"

#include <QDateTime>
#include <QLocale>

#include <KLocalizedString>

#include "whatevr/v1/whatevr.qpb.h"

namespace {

QString previewForMessage(const whatevr::v1::Message &message)
{
    if (message.isRevoked()) {
        return i18nc("@item:intext starred-message preview", "This message was deleted");
    }
    const QString text = message.text();
    if (!text.isEmpty()) {
        return text;
    }
    const QString kind = message.mediaKind();
    if (kind == QLatin1String("image")) {
        return i18nc("@item:intext media placeholder", "Photo");
    }
    if (kind == QLatin1String("sticker")) {
        return i18nc("@item:intext media placeholder", "Sticker");
    }
    if (!kind.isEmpty()) {
        return i18nc("@item:intext media placeholder", "Media");
    }
    return QString();
}

QString formatTimestamp(qint64 timestampUnix)
{
    if (timestampUnix <= 0) {
        return QString();
    }
    return QLocale().toString(QDateTime::fromSecsSinceEpoch(timestampUnix), QLocale::ShortFormat);
}

bool isOutgoingDirection(const whatevr::v1::Message &message)
{
    return message.direction() == whatevr::v1::MessageDirectionGadget::MessageDirection::MESSAGE_DIRECTION_OUTGOING;
}

} // namespace

StarredMessagesModel::StarredMessagesModel(QObject *parent)
    : QAbstractListModel(parent)
{
}

int StarredMessagesModel::rowCount(const QModelIndex &parent) const
{
    if (parent.isValid()) {
        return 0;
    }
    return static_cast<int>(m_items.size());
}

QVariant StarredMessagesModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= m_items.size()) {
        return {};
    }
    const Item &item = m_items.at(index.row());
    switch (role) {
    case MessageIdRole:
        return item.messageId;
    case ChatIdRole:
        return item.chatId;
    case ChatNameRole:
        return item.chatName;
    case SenderNameRole:
        return item.senderName;
    case PreviewRole:
        return item.preview;
    case MediaKindRole:
        return item.mediaKind;
    case TimeTextRole:
        return item.timeText;
    case TimestampUnixRole:
        return item.timestampUnix;
    case IsOutgoingRole:
        return item.isOutgoing;
    default:
        return {};
    }
}

QHash<int, QByteArray> StarredMessagesModel::roleNames() const
{
    return {
        {MessageIdRole, "messageId"},
        {ChatIdRole, "chatId"},
        {ChatNameRole, "chatName"},
        {SenderNameRole, "senderName"},
        {PreviewRole, "preview"},
        {MediaKindRole, "mediaKind"},
        {TimeTextRole, "timeText"},
        {TimestampUnixRole, "timestampUnix"},
        {IsOutgoingRole, "isOutgoing"},
    };
}

void StarredMessagesModel::replace(const QList<whatevr::v1::StarredMessageItem> &items)
{
    beginResetModel();
    m_items.clear();
    m_items.reserve(static_cast<int>(items.size()));
    for (const auto &item : items) {
        m_items.append(fromProto(item.message(), item.chatName()));
    }
    endResetModel();
}

void StarredMessagesModel::clear()
{
    if (m_items.isEmpty()) {
        return;
    }
    beginResetModel();
    m_items.clear();
    endResetModel();
}

void StarredMessagesModel::applyMessage(const whatevr::v1::Message &message)
{
    // The view only ever shows starred messages; the moment one stops being
    // starred (unstarred or deleted) it should leave the list.
    if (!message.isStarred() || message.isRevoked()) {
        removeMessage(message.id_proto());
        return;
    }
    const int existing = indexOfMessage(message.id_proto());
    if (existing < 0) {
        return; // A newly-starred message surfaces on the next full reload.
    }
    m_items[existing] = fromProto(message, m_items.at(existing).chatName);
    const QModelIndex changed = index(existing, 0);
    Q_EMIT dataChanged(changed, changed);
}

bool StarredMessagesModel::removeMessage(const QString &messageId)
{
    const int existing = indexOfMessage(messageId);
    if (existing < 0) {
        return false;
    }
    beginRemoveRows(QModelIndex(), existing, existing);
    m_items.removeAt(existing);
    endRemoveRows();
    return true;
}

int StarredMessagesModel::indexOfMessage(const QString &messageId) const
{
    for (int i = 0; i < m_items.size(); ++i) {
        if (m_items.at(i).messageId == messageId) {
            return i;
        }
    }
    return -1;
}

StarredMessagesModel::Item StarredMessagesModel::fromProto(const whatevr::v1::Message &message, const QString &chatName)
{
    const bool outgoing = isOutgoingDirection(message);
    QString senderName = message.senderName();
    if (outgoing) {
        senderName = i18nc("@item:intext message sender, the local user", "You");
    }
    return Item {
        .messageId = message.id_proto(),
        .chatId = message.chatId(),
        .chatName = chatName,
        .senderName = senderName,
        .preview = previewForMessage(message),
        .mediaKind = message.mediaKind(),
        .timeText = formatTimestamp(message.timestampUnix()),
        .timestampUnix = message.timestampUnix(),
        .isOutgoing = outgoing,
    };
}
