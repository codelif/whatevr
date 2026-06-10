#pragma once

#include <QAbstractItemModel>
#include <QHash>
#include <QList>
#include <QObject>
#include <QSet>
#include <QString>
#include <QtGrpc/qtgrpcnamespace.h>

#include <memory>

#include "whatevr/v1/whatevr.qpb.h"

QT_BEGIN_NAMESPACE
class QGrpcCallReply;
class QAbstractGrpcChannel;
class QTimer;
QT_END_NAMESPACE

namespace whatevr::v1::StickerService {
class Client;
}

class AppController;
class StickerModel;
class StickerFilterModel;
class StickerPackModel;

// Drives the sticker side of the expression picker: library views (recents /
// favorites / packs / search), the store browser, lazy file downloads and
// sending. Owned by AppController, which shares its gRPC channel and forwards
// the sticker daemon events here.
class StickerController final : public QObject
{
    Q_OBJECT
    Q_PROPERTY(QAbstractItemModel *stickerModel READ stickerModel CONSTANT FINAL)
    Q_PROPERTY(QAbstractItemModel *packModel READ packModel CONSTANT FINAL)
    Q_PROPERTY(QAbstractItemModel *installedPackModel READ installedPackModel CONSTANT FINAL)
    Q_PROPERTY(bool loading READ loading NOTIFY viewChanged FINAL)
    // True while the view's stickers are still being fetched in the background.
    // The grid hides not-yet-downloaded tiles, so it can read empty (count 0)
    // even though stickers are on their way; the picker keeps its spinner up
    // (rather than the "no stickers" placeholder) while this is set.
    Q_PROPERTY(bool downloading READ downloading NOTIFY viewChanged FINAL)
    Q_PROPERTY(bool packsLoading READ packsLoading NOTIFY viewChanged FINAL)
    Q_PROPERTY(QString errorText READ errorText NOTIFY viewChanged FINAL)
    // "recents" | "favorites" | "all" | "pack" | "search"
    Q_PROPERTY(QString activeSource READ activeSource NOTIFY viewChanged FINAL)
    Q_PROPERTY(QString activePackId READ activePackId NOTIFY viewChanged FINAL)
    Q_PROPERTY(QString activePackName READ activePackName NOTIFY viewChanged FINAL)
    Q_PROPERTY(bool activePackInstalled READ activePackInstalled NOTIFY viewChanged FINAL)

public:
    explicit StickerController(AppController *appController);
    ~StickerController() override;

    void attachChannel(const std::shared_ptr<QAbstractGrpcChannel> &channel);
    void handleLibraryChanged(whatevr::v1::StickerSourceGadget::StickerSource source);
    void handleDownloadChanged(const whatevr::v1::Sticker &sticker, const QString &errorText);

    [[nodiscard]] QAbstractItemModel *stickerModel() const;
    [[nodiscard]] QAbstractItemModel *packModel() const;
    [[nodiscard]] QAbstractItemModel *installedPackModel() const;
    [[nodiscard]] bool loading() const;
    [[nodiscard]] bool downloading() const;
    [[nodiscard]] bool packsLoading() const;
    [[nodiscard]] QString errorText() const;
    [[nodiscard]] QString activeSource() const;
    [[nodiscard]] QString activePackId() const;
    [[nodiscard]] QString activePackName() const;
    [[nodiscard]] bool activePackInstalled() const;

    // First-open hook: loads recents + the pack list once, lazily, so users
    // who never open the sticker tab pay nothing.
    Q_INVOKABLE void activate();
    Q_INVOKABLE void showRecents();
    Q_INVOKABLE void showFavorites();
    Q_INVOKABLE void showAll();
    Q_INVOKABLE void showPack(const QString &packId);
    Q_INVOKABLE void search(const QString &query);
    Q_INVOKABLE void refreshStore();
    Q_INVOKABLE void setPackInstalled(const QString &packId, bool installed);
    Q_INVOKABLE void requestDownload(const QString &cacheKey);
    Q_INVOKABLE void sendSticker(const QString &cacheKey, const QString &replyToMessageId = QString());

Q_SIGNALS:
    void viewChanged();
    // Relayed to AppController so the optimistic bubble lands in the message
    // list exactly like text/image sends.
    void messageSent(const whatevr::v1::Message &message);
    // Lets the picker close (or flash an error) after a click-to-send.
    void stickerSent(const QString &cacheKey);
    void stickerSendFailed(const QString &cacheKey, const QString &errorText);

private:
    enum class View { Recents, Favorites, All, Pack, Search };

    void requestList(View view, const QString &packId = QString(), const QString &query = QString());
    void requestPacks(bool forceRefresh);
    // Enqueues background downloads for every not-yet-cached sticker in a freshly
    // loaded list. The grid only shows downloaded tiles, so nothing in the view
    // drives these fetches — the controller must prefetch them itself. The queue
    // caps in-flight downloads and dedups, so a 200-sticker list is safe.
    void prefetchStickers(const QList<whatevr::v1::Sticker> &stickers);
    // Starts queued downloads up to the in-flight cap, so the shared gRPC
    // channel is never flooded with one stream per visible tile.
    void pumpDownloadQueue();
    // Dispatches the actual DownloadSticker RPC for one key (assumes a free
    // in-flight slot). Called only via pumpDownloadQueue().
    void startDownload(const QString &cacheKey);
    // Recomputes the downloading flag from the queue/in-flight sets and emits
    // viewChanged only when it flips, so the grid's spinner-vs-placeholder
    // state tracks background prefetch without a signal per finished download.
    void updateDownloadingState();
    // Retries a transient download failure with backoff, up to a small budget.
    // Returns true if a retry was scheduled; false if the failure is terminal
    // or the budget is exhausted, in which case the caller marks the tile
    // unavailable so the picker stops re-arming the download.
    [[nodiscard]] bool scheduleDownloadRetry(const QString &cacheKey, QtGrpc::StatusCode code);
    void applyPackList(const QList<whatevr::v1::StickerPack> &packs);
    [[nodiscard]] bool clientReady() const;

    AppController *m_appController = nullptr;
    StickerModel *m_stickerModel = nullptr;
    // Exposed to QML as stickerModel; hides terminally-failed tiles.
    StickerFilterModel *m_stickerFilterModel = nullptr;
    StickerPackModel *m_packModel = nullptr;
    StickerPackModel *m_installedPackModel = nullptr;

    std::unique_ptr<whatevr::v1::StickerService::Client> m_client;
    std::unique_ptr<QGrpcCallReply> m_listReply;
    std::unique_ptr<QGrpcCallReply> m_packsReply;
    QHash<QString, std::shared_ptr<QGrpcCallReply>> m_downloadReplies;
    // Keys awaiting an in-flight slot. Used as a LIFO stack (prepend on
    // request, take from front) so the tiles the user is currently looking at
    // download first; m_downloadQueued mirrors it for O(1) dedup.
    QList<QString> m_downloadQueue;
    QSet<QString> m_downloadQueued;
    // Per-key download attempt counts, so a transient failure is retried a few
    // times with backoff instead of leaving the tile spinning forever.
    QHash<QString, int> m_downloadAttempts;
    // Keys whose media is permanently unavailable (terminal failure / retry
    // budget spent). Skipped by requestDownload/prefetch so a dead sticker is
    // never re-fetched on every list reload (e.g. during the favorites trickle).
    QSet<QString> m_terminalDownloads;
    QHash<QString, std::shared_ptr<QGrpcCallReply>> m_installReplies;
    QHash<QString, std::shared_ptr<QGrpcCallReply>> m_sendReplies;

    QTimer *m_searchTimer = nullptr;
    QString m_pendingSearchQuery;

    View m_view = View::Recents;
    QString m_activePackId;
    QString m_activePackName;
    bool m_activePackInstalled = false;
    QString m_searchQuery;
    QString m_errorText;
    bool m_loading = false;
    bool m_downloading = false;
    bool m_packsLoading = false;
    bool m_activated = false;
    // Bumped on every view switch so stale list replies are dropped.
    quint64 m_viewGeneration = 0;
};
