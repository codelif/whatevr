#pragma once

#include <QAbstractListModel>
#include <QJsonArray>
#include <QJsonObject>
#include <QList>
#include <QString>

#include <cstdint>

// Presentation model for the protocol's *query* results (`search.chats`,
// `search.messages`, `contacts.check_phone`). Unlike a view, a query is a
// one-shot request whose whole result arrives in one response and which the
// frontend renders and throws away — PROTOCOL.md's Queries section allows
// exactly this frontend-only state.
//
// Each of the three result sets keeps the daemon's own order and lives in its
// own section (phone number, chats, messages); the model never sorts, merges,
// or deduplicates across them. Roles match the ones the search delegate has
// always bound to, so it is a pure data-source swap.
class ProtocolSearchModel final : public QAbstractListModel
{
    Q_OBJECT

public:
    enum Role : std::uint16_t {
        KindRole = Qt::UserRole + 1,
        AvatarLocalPathRole,
        InitialsRole,
        TitleRole,
        SubtitleRole,
        IsGroupRole,
        ChatIdRole,
        MessageIdRole,
        SenderNameRole,
        TimeTextRole,
        TimestampUnixRole,
        IsOutgoingRole,
        JidRole,
        RegisteredRole,
    };
    Q_ENUM(Role)

    explicit ProtocolSearchModel(QObject *parent = nullptr);

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    // Each setter takes the daemon rows verbatim: `search.chats`' `chats`
    // array, `search.messages`' `messages` array (message items plus
    // `chat_name`), and the `contacts.check_phone` result object.
    void setChats(const QJsonArray &chats);
    void setMessages(const QJsonArray &messages);
    void setNumber(const QJsonObject &result);
    void clearNumber();
    void clear();

    [[nodiscard]] int chatCount() const { return static_cast<int>(m_chats.size()); }
    [[nodiscard]] int messageCount() const { return static_cast<int>(m_messages.size()); }
    [[nodiscard]] int numberCount() const { return static_cast<int>(m_number.size()); }

private:
    struct Row {
        bool isMessage = false;
        bool isNumber = false;
        QString avatarLocalPath;
        QString initials;
        QString title;
        QString subtitle;
        bool isGroup = false;
        QString chatId;
        QString messageId;
        QString senderName;
        QString timeText;
        qint64 timestampUnix = 0;
        bool isOutgoing = false;
        QString jid;
        bool registered = false;
    };

    QList<Row> m_number;
    QList<Row> m_chats;
    QList<Row> m_messages;
};
