#pragma once

#include <QAbstractListModel>
#include <QHash>
#include <QList>
#include <QString>

#include <cstdint>

namespace whatevr::v1 {
class Chat;
}

class ChatListModel final : public QAbstractListModel
{
    Q_OBJECT

public:
    enum Role : std::uint16_t {
        IdRole = Qt::UserRole + 1,
        NameRole,
        LastMessageRole,
        LastMessageTimeUnixRole,
        LastMessageDirectionRole,
        LastMessageStatusRole,
        UnreadCountRole,
        IsGroupRole,
        IsPinnedRole,
        PinnedOrderRole,
        AvatarLocalPathRole,
        InitialsRole,
        IsTypingRole,
    };
    Q_ENUM(Role)

    explicit ChatListModel(QObject *parent = nullptr);

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    void replaceChats(const QList<whatevr::v1::Chat> &chats);
    void upsertChat(const whatevr::v1::Chat &chat, const QString &previousChatId = QString());
    bool updateAvatar(const QString &chatId, const QString &avatarLocalPath);
    bool setChatTyping(const QString &chatId, bool typing);
    [[nodiscard]] QString chatName(const QString &chatId) const;
    [[nodiscard]] QString chatAvatarLocalPath(const QString &chatId) const;
    [[nodiscard]] bool chatIsGroup(const QString &chatId) const;
    [[nodiscard]] int chatUnreadCount(const QString &chatId) const;
    [[nodiscard]] int indexOf(const QString &chatId) const;
    [[nodiscard]] bool isEmpty() const;

private:
    struct ChatItem {
        QString id;
        QString name;
        QString displayName;
        QString initials;
        QString lastMessage;
        qint64 lastMessageTimeUnix = 0;
        int lastMessageDirection = 0;
        int lastMessageStatus = 0;
        int unreadCount = 0;
        bool isGroup = false;
        bool isPinned = false;
        quint32 pinnedOrder = 0;
        qint64 updatedAtUnix = 0;
        QString avatarLocalPath;
        bool isTyping = false;
    };

    static ChatItem fromProto(const whatevr::v1::Chat &chat);
    static QString displayName(const ChatItem &chat);
    static QString initialsForName(const QString &name);
    static bool sameSortOrder(const QList<ChatItem> &left, const QList<ChatItem> &right);
    static bool sameChatData(const ChatItem &left, const ChatItem &right);
    static bool sortBefore(const ChatItem &left, const ChatItem &right);
    [[nodiscard]] int sortedInsertIndex(const ChatItem &item, int excludingIndex = -1) const;
    void sortChats();
    void rebuildIndex();
    void reindexRange(int firstRow, int lastRow);

    QList<ChatItem> m_chats;
    QHash<QString, int> m_chatIndexById;
};
