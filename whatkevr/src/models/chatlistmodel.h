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
        HasDraftRole,
        DraftTextRole,
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
    // Per-contact composer draft. A non-empty draft floats the chat to the top of
    // its (unpinned) section as if a new message arrived — without touching the
    // real last-message timestamp, so clearing the draft restores its position.
    // Drafts never pin. Stored client-side and re-applied across daemon refreshes.
    Q_INVOKABLE void setChatDraft(const QString &chatId, const QString &text);
    [[nodiscard]] Q_INVOKABLE QString chatDraft(const QString &chatId) const;
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
        // Transient client-only draft state, filled from m_drafts via applyDraft().
        bool hasDraft = false;
        QString draftText;
        qint64 draftTimeUnix = 0;
    };

    static ChatItem fromProto(const whatevr::v1::Chat &chat);
    void applyDraft(ChatItem &item) const;
    static QString displayName(const ChatItem &chat);
    static QString initialsForName(const QString &name);
    static bool sameSortOrder(const QList<ChatItem> &left, const QList<ChatItem> &right);
    static bool sameChatData(const ChatItem &left, const ChatItem &right);
    static bool sortBefore(const ChatItem &left, const ChatItem &right);
    [[nodiscard]] int sortedInsertIndex(const ChatItem &item, int excludingIndex = -1) const;
    void sortChats();
    void rebuildIndex();
    void reindexRange(int firstRow, int lastRow);

    struct Draft {
        QString text;
        qint64 timeUnix = 0;
    };

    QList<ChatItem> m_chats;
    QHash<QString, int> m_chatIndexById;
    QHash<QString, Draft> m_drafts;
};
