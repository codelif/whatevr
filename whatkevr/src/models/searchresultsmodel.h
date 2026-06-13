#pragma once

#include <QAbstractListModel>
#include <QList>
#include <QString>

#include <cstdint>

namespace whatevr::v1 {
class Chat;
class MessageSearchResult;
}

// Flat, two-section list backing the unified search bar in the chat list. It
// holds chat-name matches first (section "chat") then message-text matches
// (section "message"), so a single ListView can render both with sectioned
// headers. Rows carry the ids needed to open a chat or jump to a message.
class SearchResultsModel final : public QAbstractListModel
{
    Q_OBJECT
    Q_PROPERTY(int chatCount READ chatCount NOTIFY countsChanged FINAL)
    Q_PROPERTY(int messageCount READ messageCount NOTIFY countsChanged FINAL)
    Q_PROPERTY(int numberCount READ numberCount NOTIFY countsChanged FINAL)

public:
    enum Role : std::uint16_t {
        // "number", "chat" or "message"; drives the ListView section header.
        KindRole = Qt::UserRole + 1,
        AvatarLocalPathRole,
        InitialsRole,
        // Chat name, or for message rows the owning chat's name.
        TitleRole,
        // Chat: last-message preview. Message: the matched text (raw; the
        // delegate windows/highlights it against the query).
        SubtitleRole,
        IsGroupRole,
        // Message-only.
        ChatIdRole,
        MessageIdRole,
        SenderNameRole,
        TimeTextRole,
        TimestampUnixRole,
        IsOutgoingRole,
        // Number-only: canonical JID and whether the number is on WhatsApp.
        JidRole,
        RegisteredRole,
    };
    Q_ENUM(Role)

    explicit SearchResultsModel(QObject *parent = nullptr);

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    [[nodiscard]] int chatCount() const;
    [[nodiscard]] int messageCount() const;
    [[nodiscard]] int numberCount() const;

    void setChats(const QList<whatevr::v1::Chat> &chats);
    void setMessages(const QList<whatevr::v1::MessageSearchResult> &results);
    // Set (or clear, when jid and phone are both empty) the single phone-number
    // result shown above chat/message matches when the query is a number.
    void setNumber(const QString &jid, const QString &phone, const QString &displayName, bool registered);
    void clearNumber();
    void clear();

Q_SIGNALS:
    void countsChanged();

private:
    struct Row {
        bool isMessage = false;
        bool isNumber = false;
        QString avatarLocalPath {};
        QString initials {};
        QString title {};
        QString subtitle {};
        bool isGroup = false;
        QString chatId {};
        QString messageId {};
        QString senderName {};
        QString timeText {};
        qint64 timestampUnix = 0;
        bool isOutgoing = false;
        QString jid {};
        bool registered = false;
    };

    QList<Row> m_number;
    QList<Row> m_chats;
    QList<Row> m_messages;
};
