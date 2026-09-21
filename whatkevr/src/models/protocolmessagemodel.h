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
        MediaSizeBytesRole,
        MediaDurationSecsRole,
        MediaFileNameRole,
        MediaPageCountRole,
        MediaWaveformRole,
        MediaPlayedRole,
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
        // Whether the daemon attached a `media` object at all. Kinds with
        // nothing to fetch (a poll, a contact card, a system event) have a
        // kind but no media, and a bubble that inferred "downloadable" from
        // the kind alone would put a download button over nothing.
        HasMediaRole,
        // A disappearing message somebody asked to keep in the chat.
        IsKeptRole,
        // Kind-specific payloads, one role per family, each an empty map on a
        // row of another kind. One role per *field* would add sixty entries
        // here and sixty required properties to ChatBubble for payloads only
        // one bubble ever reads.
        LocationRole,
        LiveShareRole,
        ContactsRole,
        PollRole,
        GroupInviteRole,
        // Named eventInfo rather than event on the QML side: ChatBubble takes
        // `event` as the parameter of its key handlers, and a role by that
        // name would be silently shadowed inside them.
        EventRole,
        // An album's pictures, as whole message items. The daemon grouped
        // them; this model does not merge, sort or dedupe anything (rule 3).
        AlbumRole,
        // The card the sender's client built for a link in the text. Unlike
        // every other payload here it arrives on a row whose kind is `text`,
        // because the text is still the message: the card renders above it,
        // not instead of it.
        LinkPreviewRole,
        // A business message: a header, some words and a set of things the
        // reader is invited to do. WhatsApp has four wire shapes for that one
        // idea; the daemon flattens all four into this, so there is one card
        // here rather than four.
        InteractiveRole,
        // A product, an order or a payment. The money arrives as the integer
        // WhatsApp sent plus its currency code, never pre-formatted: how a sum
        // reads is a question about this machine's locale, which is the one
        // thing the daemon cannot know.
        CommerceRole,
        // A shared sticker pack, with the library's own answer about whether it
        // can be added and whether it already has been.
        StickerPackRole,
        // A call that happened. Not a message anybody wrote, which is why it
        // draws as a centered pill rather than in somebody's bubble.
        CallLogRole,
        // Something the chat did to itself: somebody joined, the subject
        // changed, a security code changed. Centered for the same reason a call
        // log is, and carrying both a finished sentence and the parts it was
        // built from, so the pill can say it in the reader's language.
        SystemRole,
        // A message that arrived but would not decrypt, and has been asked for
        // again. The row turns into the real message, in place, if it comes.
        WaitingRole,
        // Sender device id: 0 is the primary phone app, anything else a linked
        // device (Web/Desktop or another companion). Drives the footer mark.
        SenderDeviceRole,
        // WhatsApp forward marker (daemon `forwarded`).
        IsForwardedRole,
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
    /// The next downloaded voice note after messageId, for playing a run of
    /// them back to back. Empty when there is none.
    [[nodiscard]] Q_INVOKABLE QVariantMap nextVoiceMessage(const QString &messageId) const;
    [[nodiscard]] Q_INVOKABLE QStringList allMessageIds() const;
    [[nodiscard]] Q_INVOKABLE QStringList messageIdsForDay(const QString &messageId) const;

    /// The ends of the transcript in time rather than in row index. Callers
    /// that mean "the oldest message we hold" must ask for it by name: the
    /// transcript is held newest-first, so row 0 is the newest one and the
    /// literal 0 that used to mean "oldest" now means its opposite.
    [[nodiscard]] Q_INVOKABLE QString oldestMessageId() const;
    [[nodiscard]] Q_INVOKABLE QString newestMessageId() const;

    /// True when row 0 holds the newest message. Every question about which way
    /// the rows run is answered from here, and from the two neighbour helpers
    /// below, rather than from arithmetic spelled out at each site.
    [[nodiscard]] bool newestFirst() const;
    /// The row holding the message immediately older (or newer) in time than
    /// this one, or -1 at that end of the transcript.
    [[nodiscard]] Q_INVOKABLE int olderRow(int row) const;
    [[nodiscard]] Q_INVOKABLE int newerRow(int row) const;
    /// The row holding the nth-oldest message, for the handful of readers that
    /// genuinely want a conversation in the order it happened.
    [[nodiscard]] int chronologicalRow(int nth) const;

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
    [[nodiscard]] static bool rendersItsOwnPayload(const QVariantMap &item);
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
    [[nodiscard]] static bool isAuthorless(const QVariantMap &item);
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
    // Both take a wire item rather than a row, because an album's pictures are
    // real messages with real ids that occupy no row of their own.
    [[nodiscard]] QVariantMap snapshotOfItem(const QVariantMap &item, const QString &messageId) const;
    [[nodiscard]] double downloadProgress(const QVariantMap &item) const;

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
        QVariantMap location;
        bool senderLoaded = false;
        bool mediaLoaded = false;
        bool replyLoaded = false;
        bool locationLoaded = false;
    };
    [[nodiscard]] const RowCache &rowCache(int row) const;
    [[nodiscard]] const QVariantMap &cachedSender(const RowCache &cache) const;
    [[nodiscard]] const QVariantMap &cachedMedia(const RowCache &cache) const;
    [[nodiscard]] const QVariantMap &cachedReply(const RowCache &cache) const;
    [[nodiscard]] const QVariantMap &cachedLocation(const RowCache &cache) const;

    whatevr::proto::CollectionViewModel *m_source;
    whatevr::proto::CollectionViewModel *m_transfers = nullptr;
    mutable QHash<QString, TextPresentation> m_textById;
    QFont m_bodyFont;
    QFontMetricsF m_bodyMetrics;
    /// The same font at the size inline emoji are actually drawn at, so a line
    /// carrying one is measured as wide as it lands.
    QFontMetricsF m_emojiMetrics;
    mutable QHash<int, QString> m_dateTextByDay;
    mutable QDate m_dateTextDay;
    mutable RowCache m_rowCache;
    // Message ids that had an active download at the last transfers change, so
    // rows that stop transferring are refreshed alongside rows that start.
    QStringList m_transferRowIds;
};
