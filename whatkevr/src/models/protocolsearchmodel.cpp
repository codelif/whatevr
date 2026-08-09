#include "protocolsearchmodel.h"

#include <QDateTime>
#include <QLocale>
#include <QStringList>

#include <KLocalizedString>

#include "messagerow.h"

namespace
{
QString initialsForName(const QString &name)
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

QString chatDisplayName(const QJsonObject &chat)
{
    const QString name = chat.value(QStringLiteral("name")).toString().trimmed();
    if (!name.isEmpty()) {
        return name;
    }
    const QString id = chat.value(QStringLiteral("id")).toString().trimmed();
    if (!id.isEmpty()) {
        return id.section(QLatin1Char('@'), 0, 0);
    }
    return i18nc("@item:intext fallback chat name", "Unknown chat");
}

QString formatTimestamp(qint64 timestampUnix)
{
    if (timestampUnix <= 0) {
        return {};
    }
    return QLocale().toString(QDateTime::fromSecsSinceEpoch(timestampUnix), QLocale::ShortFormat);
}
} // namespace

ProtocolSearchModel::ProtocolSearchModel(QObject *parent)
    : QAbstractListModel(parent)
{
}

int ProtocolSearchModel::rowCount(const QModelIndex &parent) const
{
    if (parent.isValid()) {
        return 0;
    }
    return static_cast<int>(m_number.size() + m_chats.size() + m_messages.size());
}

QVariant ProtocolSearchModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= rowCount()) {
        return {};
    }
    const int row = index.row();
    const Row *itemPtr = nullptr;
    if (row < m_number.size()) {
        itemPtr = &m_number.at(row);
    } else if (row - m_number.size() < m_chats.size()) {
        itemPtr = &m_chats.at(row - m_number.size());
    } else {
        itemPtr = &m_messages.at(row - m_number.size() - m_chats.size());
    }
    const Row &item = *itemPtr;
    switch (role) {
    case KindRole:
        if (item.isNumber) {
            return QStringLiteral("number");
        }
        return item.isMessage ? QStringLiteral("message") : QStringLiteral("chat");
    case AvatarLocalPathRole:
        return item.avatarLocalPath;
    case InitialsRole:
        return item.initials;
    case TitleRole:
        return item.title;
    case SubtitleRole:
        return item.subtitle;
    case IsGroupRole:
        return item.isGroup;
    case ChatIdRole:
        return item.chatId;
    case MessageIdRole:
        return item.messageId;
    case SenderNameRole:
        return item.senderName;
    case TimeTextRole:
        return item.timeText;
    case TimestampUnixRole:
        return item.timestampUnix;
    case IsOutgoingRole:
        return item.isOutgoing;
    case JidRole:
        return item.jid;
    case RegisteredRole:
        return item.registered;
    default:
        return {};
    }
}

QHash<int, QByteArray> ProtocolSearchModel::roleNames() const
{
    return {
        {KindRole, "kind"},
        {AvatarLocalPathRole, "avatarLocalPath"},
        {InitialsRole, "initials"},
        {TitleRole, "title"},
        {SubtitleRole, "subtitle"},
        {IsGroupRole, "isGroup"},
        {ChatIdRole, "chatId"},
        {MessageIdRole, "messageId"},
        {SenderNameRole, "senderName"},
        {TimeTextRole, "timeText"},
        {TimestampUnixRole, "timestampUnix"},
        {IsOutgoingRole, "isOutgoing"},
        {JidRole, "jid"},
        {RegisteredRole, "registered"},
    };
}

void ProtocolSearchModel::setChats(const QJsonArray &chats)
{
    QList<Row> rows;
    rows.reserve(static_cast<int>(chats.size()));
    for (const auto &value : chats) {
        const QJsonObject chat = value.toObject();
        const QString name = chatDisplayName(chat);
        rows.append(Row{
            .avatarLocalPath = chat.value(QStringLiteral("avatar_path")).toString(),
            .initials = initialsForName(name),
            .title = name,
            .subtitle = chat.value(QStringLiteral("preview")).toString(),
            .isGroup = chat.value(QStringLiteral("is_group")).toBool(),
            .chatId = chat.value(QStringLiteral("id")).toString(),
        });
    }
    beginResetModel();
    m_chats = std::move(rows);
    endResetModel();
}

void ProtocolSearchModel::setMessages(const QJsonArray &messages)
{
    QList<Row> rows;
    rows.reserve(static_cast<int>(messages.size()));
    for (const auto &value : messages) {
        const QJsonObject message = value.toObject();
        const bool outgoing =
            message.value(QStringLiteral("direction")).toString() == QLatin1String("outgoing");
        const QString senderName = outgoing
            ? i18nc("@item:intext message sender, the local user", "You")
            : message.value(QStringLiteral("sender")).toObject().value(QStringLiteral("name")).toString();
        const QString chatName = message.value(QStringLiteral("chat_name")).toString();
        const qint64 timestamp = static_cast<qint64>(message.value(QStringLiteral("timestamp")).toDouble());
        rows.append(Row{
            .isMessage = true,
            .initials = initialsForName(chatName),
            .title = chatName,
            .subtitle = whatevr::util::messageRowPreview(message.toVariantMap()),
            .chatId = message.value(QStringLiteral("chat_id")).toString(),
            .messageId = message.value(QStringLiteral("id")).toString(),
            .senderName = senderName,
            .timeText = formatTimestamp(timestamp),
            .timestampUnix = timestamp,
            .isOutgoing = outgoing,
        });
    }
    beginResetModel();
    m_messages = std::move(rows);
    endResetModel();
}

void ProtocolSearchModel::setNumber(const QJsonObject &result)
{
    QList<Row> rows;
    const QString phone = result.value(QStringLiteral("phone")).toString().trimmed();
    if (!phone.isEmpty()) {
        const QString displayName = result.value(QStringLiteral("display_name")).toString().trimmed();
        const QString title = displayName.isEmpty() ? phone : displayName;
        rows.append(Row{
            .isNumber = true,
            .initials = initialsForName(title),
            .title = title,
            .subtitle = phone,
            .jid = result.value(QStringLiteral("jid")).toString(),
            .registered = result.value(QStringLiteral("registered")).toBool(),
        });
    }
    beginResetModel();
    m_number = std::move(rows);
    endResetModel();
}

void ProtocolSearchModel::clearNumber()
{
    if (m_number.isEmpty()) {
        return;
    }
    beginResetModel();
    m_number.clear();
    endResetModel();
}

void ProtocolSearchModel::clear()
{
    if (m_number.isEmpty() && m_chats.isEmpty() && m_messages.isEmpty()) {
        return;
    }
    beginResetModel();
    m_number.clear();
    m_chats.clear();
    m_messages.clear();
    endResetModel();
}
