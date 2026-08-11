#pragma once

#include <QAbstractListModel>
#include <QDate>
#include <QFont>
#include <QFontMetricsF>
#include <QHash>
#include <QStringList>
#include <QVariantMap>

#include <cstdint>

#include "collectionviewmodel.h"
#include "messagemarkup.h"

// Presentation adapter for protocol `messages` items. The source collection
// remains the sole owner of keys and ordering; this model mirrors its rows
// exactly and only derives strings, grouping, markup, and nested-field roles
// consumed by the conversation UI.
class ProtocolMessageModel final : public QAbstractListModel
{
    Q_OBJECT

public:
    enum Role : std::uint16_t {
        IdRole = Qt::UserRole + 1,
        ChatIdRole,
        SenderIdRole,
        SenderNameRole,
        SenderAvatarLocalPathRole,
        SenderInitialsRole,
        TextRole,
        LayoutTextRole,
        EmojiOnlyCountRole,
        HasRichTextRole,
        RichTextRole,
        TextPreviewRole,
        LayoutTextPreviewRole,
        PreviewHasRichTextRole,
        PreviewRichTextRole,
        TextTruncatedRole,
        TimestampUnixRole,
        TimeTextRole,
        DateSeparatorTextRole,
        DirectionRole,
        StatusRole,
        StatusTextRole,
        IsOutgoingRole,
        MediaKindRole,
        MediaMimeTypeRole,
        MediaLocalPathRole,
        MediaThumbnailLocalPathRole,
        MediaWidthRole,
        MediaHeightRole,
        MediaAnimatedRole,
        ShowSenderHeaderRole,
        ShowSenderAvatarRole,
        ShowSenderGutterRole,
        GroupStartRole,
        GroupEndRole,
        MediaDownloadingRole,
        MediaDownloadErrorRole,
        ReplyToMessageIdRole,
        ReplyToSenderNameRole,
        ReplyToTextRole,
        ReplyToMediaKindRole,
        ReplyToMediaMimeTypeRole,
        ReplyToIsOutgoingRole,
        WidestLineWidthRole,
        LastLineWidthRole,
        LinksRole,
        HasLinksRole,
        MediaCacheKeyRole,
        IsRevokedRole,
        IsEditedRole,
        IsStarredRole,
        IsPinnedRole,
        PinnedUntilUnixRole,
        ReactionsRole,
        MediaDownloadProgressRole,
    };
    Q_ENUM(Role)

    explicit ProtocolMessageModel(whatevr::proto::CollectionViewModel *source, QObject *parent = nullptr);

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    // Compose the global `transfers` view (D4c) into the two download roles.
    // The two views stay separate on the wire — `messages` owns the durable
    // media state (`path`, `download_error`), `transfers` owns the in-progress
    // bytes — and this adapter reads the second one through by message id at
    // render time; it copies and caches nothing. Optional: with no transfers
    // source the rows simply report "not downloading".
    void setTransfersSource(whatevr::proto::CollectionViewModel *transfers);

    [[nodiscard]] Q_INVOKABLE int indexOf(const QString &messageId) const;
    [[nodiscard]] Q_INVOKABLE QString messageIdAt(int row) const;
    [[nodiscard]] Q_INVOKABLE bool isOutgoingAt(int row) const;
    [[nodiscard]] Q_INVOKABLE QString dateTextForRow(int row) const;
    Q_INVOKABLE void setBodyMetricsFont(const QFont &font);
    Q_INVOKABLE bool expandMessageText(const QString &messageId);
    [[nodiscard]] Q_INVOKABLE QString copyTextForMessages(const QStringList &messageIds) const;
    [[nodiscard]] Q_INVOKABLE QVariantMap messageSnapshot(const QString &messageId) const;
    [[nodiscard]] Q_INVOKABLE QStringList allMessageIds() const;
    [[nodiscard]] Q_INVOKABLE QStringList messageIdsForDay(const QString &messageId) const;

private:
    struct TextPresentation {
        QString sourceText;
        QString previewText;
        whatevr::util::MessageMarkup previewMarkup;
        whatevr::util::MessageMarkup fullMarkup;
        QStringList links;
        qreal previewWidest = 0;
        qreal previewLast = 0;
        qreal fullWidest = 0;
        qreal fullLast = 0;
        bool truncated = false;
        bool fullParsed = false;
    };

    [[nodiscard]] QVariantMap wireItem(int row) const;
    // The active `transfers` row for this message, or an empty map when nothing
    // is downloading it.
    [[nodiscard]] QVariantMap transfer(const QVariantMap &item) const;
    [[nodiscard]] static QString displayText(const QVariantMap &item);
    [[nodiscard]] static QVariantMap sender(const QVariantMap &item);
    [[nodiscard]] static QVariantMap media(const QVariantMap &item);
    [[nodiscard]] static QVariantMap reply(const QVariantMap &item);
    [[nodiscard]] static QString senderDisplayName(const QVariantMap &item);
    [[nodiscard]] static QString initialsForName(const QString &name);
    [[nodiscard]] static int directionValue(const QString &direction);
    [[nodiscard]] static int statusValue(const QString &status);
    [[nodiscard]] static QString statusText(const QString &status);
    [[nodiscard]] static QString mediaKind(const QVariantMap &item);
    [[nodiscard]] static QVariantList reactions(const QVariantMap &item);
    [[nodiscard]] static QList<whatevr::util::MessageMention> mentions(const QVariantMap &item);
    [[nodiscard]] static int dayNumber(const QVariantMap &item);
    [[nodiscard]] static QString formatTime(qint64 timestampUnix);
    [[nodiscard]] static QString formatRelativeDate(qint64 timestampUnix);
    [[nodiscard]] QString cachedRelativeDate(const QVariantMap &item) const;
    [[nodiscard]] bool startsSenderGroup(int row) const;
    [[nodiscard]] bool endsSenderGroup(int row) const;
    [[nodiscard]] bool startsDayGroup(int row) const;
    [[nodiscard]] TextPresentation &ensureTextPresentation(const QVariantMap &item) const;
    void ensureFullTextPresentation(TextPresentation &presentation, const QVariantMap &item) const;
    void remeasure(TextPresentation &presentation) const;
    void invalidateRows(int first, int last);
    void emitAllRolesChanged(int first, int last);
    // Only the roles a row's *neighbours* can change: whether it opens or
    // closes a sender run, and whether it carries a date separator. Inserting
    // or removing a row cannot affect anything else about the rows beside it.
    void emitNeighbourRolesChanged(int first, int last);
    void invalidateTransferRoles();
    void invalidateRowCache() const;

    // Decoded view of one row. QML reads a row's ~45 roles back to back, and
    // each read used to re-decode the item map plus its nested sender, media
    // and reply maps — so materialising one delegate cost ~180 map decodes.
    // Access is row-major, so caching the last row alone captures nearly all
    // of it. Nested maps stay lazy: roles that never touch them do not pay.
    struct RowCache {
        int row = -1;
        QVariantMap item;
        QVariantMap sender;
        QVariantMap media;
        QVariantMap reply;
        bool senderLoaded = false;
        bool mediaLoaded = false;
        bool replyLoaded = false;
    };
    [[nodiscard]] const RowCache &rowCache(int row) const;
    [[nodiscard]] const QVariantMap &cachedSender(const RowCache &cache) const;
    [[nodiscard]] const QVariantMap &cachedMedia(const RowCache &cache) const;
    [[nodiscard]] const QVariantMap &cachedReply(const RowCache &cache) const;

    whatevr::proto::CollectionViewModel *m_source;
    whatevr::proto::CollectionViewModel *m_transfers = nullptr;
    mutable QHash<QString, TextPresentation> m_textById;
    QFont m_bodyFont;
    QFontMetricsF m_bodyMetrics;
    mutable QHash<int, QString> m_dateTextByDay;
    mutable QDate m_dateTextDay;
    mutable RowCache m_rowCache;
    // Message ids that had an active download at the last transfers change, so
    // rows that stop transferring are refreshed alongside rows that start.
    QStringList m_transferRowIds;
};
