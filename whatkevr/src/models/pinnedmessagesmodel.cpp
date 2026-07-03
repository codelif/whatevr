#include "pinnedmessagesmodel.h"

#include <QDateTime>
#include <QLocale>

#include <KLocalizedString>

#include <utility>

#include "whatevr/v1/whatevr.qpb.h"

namespace {

QString previewForMessage(const whatevr::v1::Message &message)
{
    if (message.isRevoked()) {
        return i18nc("@item:intext pinned-message preview", "This message was deleted");
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

bool isActivePin(const whatevr::v1::Message &message)
{
    return !message.isRevoked() && message.pinnedUntilUnix() > QDateTime::currentSecsSinceEpoch();
}

} // namespace

PinnedMessagesModel::PinnedMessagesModel(QObject *parent)
    : QAbstractListModel(parent)
{
}

int PinnedMessagesModel::rowCount(const QModelIndex &parent) const
{
    if (parent.isValid()) {
        return 0;
    }
    return static_cast<int>(m_items.size());
}

int PinnedMessagesModel::count() const
{
    return static_cast<int>(m_items.size());
}

QVariantMap PinnedMessagesModel::get(int row) const
{
    if (row < 0 || row >= m_items.size()) {
        return {};
    }
    const Item &item = m_items.at(row);
    return {
        {QStringLiteral("messageId"), item.messageId},
        {QStringLiteral("chatId"), item.chatId},
        {QStringLiteral("senderName"), item.senderName},
        {QStringLiteral("preview"), item.preview},
        {QStringLiteral("mediaKind"), item.mediaKind},
        {QStringLiteral("timeText"), item.timeText},
        {QStringLiteral("pinnedUntilUnix"), item.pinnedUntilUnix},
        {QStringLiteral("isOutgoing"), item.isOutgoing},
    };
}

QVariant PinnedMessagesModel::data(const QModelIndex &index, int role) const
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
    case SenderNameRole:
        return item.senderName;
    case PreviewRole:
        return item.preview;
    case MediaKindRole:
        return item.mediaKind;
    case TimeTextRole:
        return item.timeText;
    case PinnedUntilUnixRole:
        return item.pinnedUntilUnix;
    case IsOutgoingRole:
        return item.isOutgoing;
    default:
        return {};
    }
}

QHash<int, QByteArray> PinnedMessagesModel::roleNames() const
{
    return {
        {MessageIdRole, "messageId"},
        {ChatIdRole, "chatId"},
        {SenderNameRole, "senderName"},
        {PreviewRole, "preview"},
        {MediaKindRole, "mediaKind"},
        {TimeTextRole, "timeText"},
        {PinnedUntilUnixRole, "pinnedUntilUnix"},
        {IsOutgoingRole, "isOutgoing"},
    };
}

void PinnedMessagesModel::replace(const QList<whatevr::v1::Message> &messages)
{
    QList<Item> next;
    next.reserve(static_cast<int>(messages.size()));
    for (const auto &message : messages) {
        if (isActivePin(message)) {
            next.append(fromProto(message));
        }
    }

    if (sameItems(m_items, next)) {
        return;
    }

    const int oldCount = static_cast<int>(m_items.size());
    beginResetModel();
    m_items = std::move(next);
    endResetModel();
    if (oldCount != m_items.size()) {
        Q_EMIT countChanged();
    }
}

void PinnedMessagesModel::clear()
{
    if (m_items.isEmpty()) {
        return;
    }
    beginResetModel();
    m_items.clear();
    endResetModel();
    Q_EMIT countChanged();
}

void PinnedMessagesModel::applyMessage(const whatevr::v1::Message &message)
{
    if (!isActivePin(message)) {
        removeMessage(message.id_proto());
        return;
    }

    const int existing = indexOfMessage(message.id_proto());
    const Item item = fromProto(message);
    if (existing >= 0) {
        // Re-pin with a new expiry can change ordering; reseat the row.
        if (m_items.at(existing).pinnedUntilUnix == item.pinnedUntilUnix) {
            m_items[existing] = item;
            const QModelIndex changed = index(existing, 0);
            Q_EMIT dataChanged(changed, changed);
            return;
        }
        beginRemoveRows(QModelIndex(), existing, existing);
        m_items.removeAt(existing);
        endRemoveRows();
    }

    const int pos = insertionPos(item.pinnedUntilUnix);
    beginInsertRows(QModelIndex(), pos, pos);
    m_items.insert(pos, item);
    endInsertRows();
    Q_EMIT countChanged();
}

bool PinnedMessagesModel::removeMessage(const QString &messageId)
{
    const int existing = indexOfMessage(messageId);
    if (existing < 0) {
        return false;
    }
    beginRemoveRows(QModelIndex(), existing, existing);
    m_items.removeAt(existing);
    endRemoveRows();
    Q_EMIT countChanged();
    return true;
}

int PinnedMessagesModel::indexOfMessage(const QString &messageId) const
{
    for (int i = 0; i < m_items.size(); ++i) {
        if (m_items.at(i).messageId == messageId) {
            return i;
        }
    }
    return -1;
}

int PinnedMessagesModel::insertionPos(qint64 pinnedUntilUnix) const
{
    for (int i = 0; i < m_items.size(); ++i) {
        if (pinnedUntilUnix < m_items.at(i).pinnedUntilUnix) {
            return i;
        }
    }
    return static_cast<int>(m_items.size());
}

bool PinnedMessagesModel::sameItems(const QList<Item> &left, const QList<Item> &right)
{
    if (left.size() != right.size()) {
        return false;
    }
    for (int i = 0; i < left.size(); ++i) {
        const Item &a = left.at(i);
        const Item &b = right.at(i);
        if (a.messageId != b.messageId
            || a.chatId != b.chatId
            || a.senderName != b.senderName
            || a.preview != b.preview
            || a.mediaKind != b.mediaKind
            || a.timeText != b.timeText
            || a.pinnedUntilUnix != b.pinnedUntilUnix
            || a.isOutgoing != b.isOutgoing) {
            return false;
        }
    }
    return true;
}

PinnedMessagesModel::Item PinnedMessagesModel::fromProto(const whatevr::v1::Message &message)
{
    const bool outgoing = isOutgoingDirection(message);
    QString senderName = message.senderName();
    if (outgoing) {
        senderName = i18nc("@item:intext message sender, the local user", "You");
    }
    return Item {
        .messageId = message.id_proto(),
        .chatId = message.chatId(),
        .senderName = senderName,
        .preview = previewForMessage(message),
        .mediaKind = message.mediaKind(),
        .timeText = formatTimestamp(message.timestampUnix()),
        .pinnedUntilUnix = message.pinnedUntilUnix(),
        .isOutgoing = outgoing,
    };
}
