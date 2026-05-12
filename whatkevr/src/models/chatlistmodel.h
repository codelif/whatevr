#pragma once

#include <QAbstractListModel>
#include <QList>
#include <QString>

namespace whatevr::v1 {
class Chat;
}

class ChatListModel final : public QAbstractListModel
{
    Q_OBJECT

public:
    enum Role {
        IdRole = Qt::UserRole + 1,
        NameRole,
        LastMessageRole,
        LastMessageTimeUnixRole,
        LastMessageDirectionRole,
        LastMessageStatusRole,
        UnreadCountRole,
        IsGroupRole,
        AvatarLocalPathRole,
        InitialsRole,
    };
    Q_ENUM(Role)

    explicit ChatListModel(QObject *parent = nullptr);

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    void replaceChats(const QList<whatevr::v1::Chat> &chats);
    void upsertChat(const whatevr::v1::Chat &chat, const QString &previousChatId = QString());
    [[nodiscard]] QString chatName(const QString &chatId) const;
    [[nodiscard]] QString chatAvatarLocalPath(const QString &chatId) const;
    [[nodiscard]] int indexOf(const QString &chatId) const;
    [[nodiscard]] bool isEmpty() const;

private:
    struct ChatItem {
        QString id;
        QString name;
        QString lastMessage;
        qint64 lastMessageTimeUnix = 0;
        int lastMessageDirection = 0;
        int lastMessageStatus = 0;
        int unreadCount = 0;
        bool isGroup = false;
        QString avatarLocalPath;
    };

    static ChatItem fromProto(const whatevr::v1::Chat &chat);
    static QString displayName(const ChatItem &chat);
    static QString initialsForName(const QString &name);
    static bool sameSortOrder(const QList<ChatItem> &left, const QList<ChatItem> &right);
    static bool sameChatData(const ChatItem &left, const ChatItem &right);
    static bool sortBefore(const ChatItem &left, const ChatItem &right);
    [[nodiscard]] int sortedInsertIndex(const ChatItem &item, int excludingIndex = -1) const;
    void sortChats();

    QList<ChatItem> m_chats;
};
