#include "searchresultsmodel.h"

#include <QDateTime>
#include <QLocale>

#include <KLocalizedString>

#include "whatevr/v1/whatevr.qpb.h"

namespace {

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

QString chatDisplayName(const whatevr::v1::Chat &chat)
{
    if (!chat.name().trimmed().isEmpty()) {
        return chat.name().trimmed();
    }
    if (!chat.id_proto().trimmed().isEmpty()) {
        return chat.id_proto().section(QLatin1Char('@'), 0, 0);
    }
    return i18nc("@item:intext fallback chat name", "Unknown chat");
}

QString previewForMessage(const whatevr::v1::Message &message)
{
    if (message.isRevoked()) {
        return i18nc("@item:intext message preview", "This message was deleted");
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

SearchResultsModel::SearchResultsModel(QObject *parent)
    : QAbstractListModel(parent)
{
}

int SearchResultsModel::rowCount(const QModelIndex &parent) const
{
    if (parent.isValid()) {
        return 0;
    }
    return static_cast<int>(m_number.size() + m_chats.size() + m_messages.size());
}

int SearchResultsModel::chatCount() const
{
    return static_cast<int>(m_chats.size());
}

int SearchResultsModel::messageCount() const
{
    return static_cast<int>(m_messages.size());
}

int SearchResultsModel::numberCount() const
{
    return static_cast<int>(m_number.size());
}

QVariant SearchResultsModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= rowCount()) {
        return {};
    }
    int row = index.row();
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

QHash<int, QByteArray> SearchResultsModel::roleNames() const
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

void SearchResultsModel::setChats(const QList<whatevr::v1::Chat> &chats)
{
    QList<Row> rows;
    rows.reserve(static_cast<int>(chats.size()));
    for (const auto &chat : chats) {
        const QString name = chatDisplayName(chat);
        rows.append(Row {
            .isMessage = false,
            .avatarLocalPath = chat.avatarLocalPath(),
            .initials = initialsForName(name),
            .title = name,
            .subtitle = chat.lastMessage(),
            .isGroup = chat.isGroup(),
            .chatId = chat.id_proto(),
            .messageId = QString(),
            .senderName = QString(),
            .timeText = QString(),
        });
    }
    beginResetModel();
    m_chats = std::move(rows);
    endResetModel();
    Q_EMIT countsChanged();
}

void SearchResultsModel::setMessages(const QList<whatevr::v1::MessageSearchResult> &results)
{
    QList<Row> rows;
    rows.reserve(static_cast<int>(results.size()));
    for (const auto &result : results) {
        const auto &message = result.message();
        const bool outgoing = isOutgoingDirection(message);
        QString senderName = message.senderName();
        if (outgoing) {
            senderName = i18nc("@item:intext message sender, the local user", "You");
        }
        rows.append(Row {
            .isMessage = true,
            .avatarLocalPath = QString(),
            .initials = initialsForName(result.chatName()),
            .title = result.chatName(),
            .subtitle = previewForMessage(message),
            .chatId = message.chatId(),
            .messageId = message.id_proto(),
            .senderName = senderName,
            .timeText = formatTimestamp(message.timestampUnix()),
            .timestampUnix = message.timestampUnix(),
            .isOutgoing = outgoing,
        });
    }
    beginResetModel();
    m_messages = std::move(rows);
    endResetModel();
    Q_EMIT countsChanged();
}

void SearchResultsModel::setNumber(const QString &jid, const QString &phone, const QString &displayName, bool registered)
{
    QList<Row> rows;
    const QString trimmedPhone = phone.trimmed();
    if (!trimmedPhone.isEmpty()) {
        const QString title = displayName.trimmed().isEmpty() ? trimmedPhone : displayName.trimmed();
        rows.append(Row {
            .isMessage = false,
            .isNumber = true,
            .initials = initialsForName(title),
            .title = title,
            .subtitle = trimmedPhone,
            .jid = jid,
            .registered = registered,
        });
    }
    beginResetModel();
    m_number = std::move(rows);
    endResetModel();
    Q_EMIT countsChanged();
}

void SearchResultsModel::clearNumber()
{
    if (m_number.isEmpty()) {
        return;
    }
    beginResetModel();
    m_number.clear();
    endResetModel();
    Q_EMIT countsChanged();
}

void SearchResultsModel::clear()
{
    if (m_number.isEmpty() && m_chats.isEmpty() && m_messages.isEmpty()) {
        return;
    }
    beginResetModel();
    m_number.clear();
    m_chats.clear();
    m_messages.clear();
    endResetModel();
    Q_EMIT countsChanged();
}
