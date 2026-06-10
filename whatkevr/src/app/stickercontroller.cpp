#include "stickercontroller.h"

#include <QGrpcCallReply>
#include <QTimer>
#include <QtGrpc/qgrpccalloptions.h>

#include <chrono>

#include <KLocalizedString>

#include "appcontroller.h"
#include "../models/stickermodel.h"
#include "../models/stickerpackmodel.h"
#include "whatevr/v1/whatevr_client.grpc.qpb.h"

using namespace whatevr::v1;
using StickerSource = whatevr::v1::StickerSourceGadget::StickerSource;

namespace {
constexpr int kSearchDebounceMs = 150;
constexpr int kDefaultListLimit = 200;
constexpr int kMaxDownloadRetries = 3;
constexpr int kDownloadRetryBaseMs = 400;
// How many DownloadSticker RPCs may be in flight at once. The picker fires a
// request per visible tile, but the daemon caps the shared HTTP/2 channel at a
// modest concurrent-stream budget that the long-lived event streams already
// dip into; a small pool keeps downloads flowing without starving it.
constexpr int kMaxInFlightDownloads = 6;
}

StickerController::StickerController(AppController *appController)
    : QObject(appController)
    , m_appController(appController)
    , m_stickerModel(new StickerModel(this))
    , m_stickerFilterModel(new StickerFilterModel(this))
    , m_packModel(new StickerPackModel(this, false))
    , m_installedPackModel(new StickerPackModel(this, true))
    , m_searchTimer(new QTimer(this))
{
    m_stickerFilterModel->setSourceModel(m_stickerModel);

    m_searchTimer->setSingleShot(true);
    m_searchTimer->setInterval(kSearchDebounceMs);
    connect(m_searchTimer, &QTimer::timeout, this, [this] {
        if (m_pendingSearchQuery.isEmpty()) {
            // Cleared search falls back to the previous browse view.
            if (m_view == View::Search) {
                showRecents();
            }
            return;
        }
        requestList(View::Search, QString(), m_pendingSearchQuery);
    });
}

StickerController::~StickerController() = default;

void StickerController::attachChannel(const std::shared_ptr<QAbstractGrpcChannel> &channel)
{
    if (!channel) {
        return;
    }
    if (!m_client) {
        m_client = std::make_unique<StickerService::Client>(this);
    }
    m_client->attachChannel(channel);
}

void StickerController::handleLibraryChanged(StickerSource source)
{
    if (source == StickerSource::STICKER_SOURCE_FAVORITE && m_favoriteKeysLoaded) {
        m_favoriteKeysLoaded = false;
        requestFavoriteKeys();
    }
    if (!m_activated) {
        return;
    }
    switch (source) {
    case StickerSource::STICKER_SOURCE_RECENT:
        if (m_view == View::Recents || m_view == View::All) {
            requestList(m_view, m_activePackId, m_searchQuery);
        }
        break;
    case StickerSource::STICKER_SOURCE_FAVORITE:
        if (m_view == View::Favorites || m_view == View::All) {
            requestList(m_view, m_activePackId, m_searchQuery);
        }
        break;
    default:
        // Pack set changed (install/uninstall/store refresh).
        requestPacks(false);
        break;
    }
}

void StickerController::handleDownloadChanged(const Sticker &sticker, const QString &errorText)
{
    if (!errorText.isEmpty()) {
        return;
    }
    m_stickerModel->updateSticker(sticker);
}

QAbstractItemModel *StickerController::stickerModel() const
{
    return m_stickerFilterModel;
}

QAbstractItemModel *StickerController::packModel() const
{
    return m_packModel;
}

QAbstractItemModel *StickerController::installedPackModel() const
{
    return m_installedPackModel;
}

bool StickerController::loading() const
{
    return m_loading;
}

bool StickerController::downloading() const
{
    return m_downloading;
}

bool StickerController::packsLoading() const
{
    return m_packsLoading;
}

QString StickerController::errorText() const
{
    return m_errorText;
}

QString StickerController::activeSource() const
{
    switch (m_view) {
    case View::Recents:
        return QStringLiteral("recents");
    case View::Favorites:
        return QStringLiteral("favorites");
    case View::All:
        return QStringLiteral("all");
    case View::Pack:
        return QStringLiteral("pack");
    case View::Search:
        return QStringLiteral("search");
    }
    return QStringLiteral("recents");
}

QString StickerController::activePackId() const
{
    return m_activePackId;
}

QString StickerController::activePackName() const
{
    return m_activePackName;
}

bool StickerController::activePackInstalled() const
{
    return m_activePackInstalled;
}

void StickerController::activate()
{
    if (m_activated) {
        return;
    }
    m_activated = true;
    requestList(View::Recents);
    requestPacks(false);
}

void StickerController::showRecents()
{
    requestList(View::Recents);
}

void StickerController::showFavorites()
{
    requestList(View::Favorites);
}

void StickerController::showAll()
{
    requestList(View::All);
}

void StickerController::showPack(const QString &packId)
{
    if (packId.trimmed().isEmpty()) {
        return;
    }
    requestList(View::Pack, packId.trimmed());
}

void StickerController::search(const QString &query)
{
    m_pendingSearchQuery = query.trimmed();
    m_searchTimer->start();
}

void StickerController::refreshStore()
{
    requestPacks(true);
}

void StickerController::setPackInstalled(const QString &packId, bool installed)
{
    const QString id = packId.trimmed();
    if (!clientReady() || id.isEmpty() || m_installReplies.contains(id)) {
        return;
    }

    SetStickerPackInstalledRequest request;
    request.setPackId(id);
    request.setInstalled(installed);

    std::shared_ptr<QGrpcCallReply> reply = m_client->SetStickerPackInstalled(request);
    m_installReplies.insert(id, reply);
    auto *rawReply = reply.get();
    connect(rawReply, &QGrpcCallReply::finished, this, [this, rawReply, id](const QGrpcStatus &status) {
        const auto reply = m_installReplies.take(id);
        if (!reply || reply.get() != rawReply) {
            return;
        }
        if (!status.isOk()) {
            m_errorText = status.message().isEmpty()
                ? i18nc("@info", "Unable to update sticker pack")
                : status.message();
            Q_EMIT viewChanged();
            return;
        }
        requestPacks(false);
        if (m_view == View::Pack && m_activePackId == id) {
            if (const auto response = rawReply->read<SetStickerPackInstalledResponse>(); response && response->hasPack()) {
                m_activePackInstalled = response->pack().installed();
                Q_EMIT viewChanged();
            }
        }
    });
}

void StickerController::requestDownload(const QString &cacheKey)
{
    if (!clientReady() || cacheKey.isEmpty() || m_downloadReplies.contains(cacheKey)
        || m_downloadQueued.contains(cacheKey) || m_terminalDownloads.contains(cacheKey)) {
        return;
    }
    // Prepend so the most recently requested (currently visible) tiles are
    // served first as the user scrolls; stale off-screen requests sink.
    m_downloadQueue.prepend(cacheKey);
    m_downloadQueued.insert(cacheKey);
    pumpDownloadQueue();
}

void StickerController::prefetchStickers(const QList<Sticker> &stickers)
{
    if (!clientReady()) {
        return;
    }
    bool queued = false;
    // Append (not prepend) so the list keeps its display order; explicit
    // requestDownload() calls and retries still jump ahead via prepend.
    for (const auto &sticker : stickers) {
        const QString cacheKey = sticker.cacheKey();
        if (cacheKey.isEmpty() || !sticker.localPath().isEmpty()) {
            continue;
        }
        if (m_downloadReplies.contains(cacheKey) || m_downloadQueued.contains(cacheKey)
            || m_terminalDownloads.contains(cacheKey)) {
            continue;
        }
        m_downloadQueue.append(cacheKey);
        m_downloadQueued.insert(cacheKey);
        queued = true;
    }
    if (queued) {
        pumpDownloadQueue();
    }
}

void StickerController::pumpDownloadQueue()
{
    while (m_downloadReplies.size() < kMaxInFlightDownloads && !m_downloadQueue.isEmpty()) {
        const QString cacheKey = m_downloadQueue.takeFirst();
        m_downloadQueued.remove(cacheKey);
        startDownload(cacheKey);
    }
    updateDownloadingState();
}

void StickerController::updateDownloadingState()
{
    const bool downloading = !m_downloadQueue.isEmpty() || !m_downloadReplies.isEmpty();
    if (downloading == m_downloading) {
        return;
    }
    m_downloading = downloading;
    Q_EMIT viewChanged();
}

void StickerController::startDownload(const QString &cacheKey)
{
    if (!clientReady() || cacheKey.isEmpty() || m_downloadReplies.contains(cacheKey)) {
        return;
    }

    DownloadStickerRequest request;
    request.setCacheKey(cacheKey);

    // Bound each call so a hung media fetch frees its pool slot (and stream)
    // instead of stalling the whole queue.
    using namespace std::chrono_literals;
    QGrpcCallOptions options;
    options.setDeadlineTimeout(30s);

    std::shared_ptr<QGrpcCallReply> reply = m_client->DownloadSticker(request, options);
    m_downloadReplies.insert(cacheKey, reply);
    auto *rawReply = reply.get();
    connect(rawReply, &QGrpcCallReply::finished, this, [this, rawReply, cacheKey](const QGrpcStatus &status) {
        const auto reply = m_downloadReplies.take(cacheKey);
        if (!reply || reply.get() != rawReply) {
            return;
        }
        if (!status.isOk()) {
            if (!scheduleDownloadRetry(cacheKey, status.code())) {
                // Terminal failure (e.g. NotFound: media path expired) or
                // retry budget spent — remember it so we never re-fetch this
                // dead sticker on a later list reload. It simply stays hidden.
                m_terminalDownloads.insert(cacheKey);
            }
        } else {
            // Succeeded: clear the retry budget for this key.
            m_downloadAttempts.remove(cacheKey);
            if (const auto response = rawReply->read<DownloadStickerResponse>(); response && response->hasSticker()) {
                // The response key may differ from the requested one when a
                // provisional favorite was canonicalized during download.
                m_stickerModel->updateSticker(response->sticker(), cacheKey);
            }
        }
        // A slot freed up; start the next queued download.
        pumpDownloadQueue();
    });
}

bool StickerController::scheduleDownloadRetry(const QString &cacheKey, QtGrpc::StatusCode code)
{
    // Only transient failures are worth retrying; a missing sticker or bad
    // request will never succeed on a retry.
    switch (code) {
    case QtGrpc::StatusCode::Unavailable:
    case QtGrpc::StatusCode::Aborted:
    case QtGrpc::StatusCode::DeadlineExceeded:
    case QtGrpc::StatusCode::Unknown:
    case QtGrpc::StatusCode::ResourceExhausted:
        break;
    default:
        m_downloadAttempts.remove(cacheKey);
        return false;
    }

    const int attempt = m_downloadAttempts.value(cacheKey, 0);
    if (attempt >= kMaxDownloadRetries) {
        m_downloadAttempts.remove(cacheKey);
        return false;
    }
    m_downloadAttempts.insert(cacheKey, attempt + 1);

    // Backoff: 400ms, 1200ms, 2400ms.
    const int delayMs = kDownloadRetryBaseMs * (attempt + 1) * (attempt + 1);
    QTimer::singleShot(delayMs, this, [this, cacheKey] {
        requestDownload(cacheKey);
    });
    return true;
}

void StickerController::sendSticker(const QString &cacheKey, const QString &replyToMessageId)
{
    if (!clientReady() || cacheKey.isEmpty() || m_sendReplies.contains(cacheKey)) {
        return;
    }
    const QString chatId = m_appController ? m_appController->selectedChatId() : QString();
    if (chatId.isEmpty()) {
        return;
    }

    SendStickerRequest request;
    request.setChatId(chatId);
    request.setCacheKey(cacheKey);
    request.setReplyToMessageId(replyToMessageId.trimmed());

    std::shared_ptr<QGrpcCallReply> reply = m_client->SendSticker(request);
    m_sendReplies.insert(cacheKey, reply);
    auto *rawReply = reply.get();
    connect(rawReply, &QGrpcCallReply::finished, this, [this, rawReply, cacheKey, chatId](const QGrpcStatus &status) {
        const auto reply = m_sendReplies.take(cacheKey);
        if (!reply || reply.get() != rawReply) {
            return;
        }
        if (!status.isOk()) {
            Q_EMIT stickerSendFailed(cacheKey,
                                     status.message().isEmpty() ? i18nc("@info", "Unable to send sticker") : status.message());
            return;
        }
        if (const auto response = rawReply->read<SendStickerResponse>(); response && response->hasMessage()
            && response->message().chatId() == chatId) {
            Q_EMIT messageSent(response->message());
        }
        Q_EMIT stickerSent(cacheKey);
    });
}

void StickerController::setStickerFavorite(const QString &cacheKey, const QString &messageId, bool favorite)
{
    if (!clientReady() || (cacheKey.isEmpty() && messageId.isEmpty())) {
        return;
    }
    const QString replyKey = cacheKey.isEmpty() ? messageId : cacheKey;
    if (m_setFavoriteReplies.contains(replyKey)) {
        return;
    }

    SetStickerFavoriteRequest request;
    request.setCacheKey(cacheKey);
    request.setMessageId(messageId);
    request.setFavorite(favorite);

    std::shared_ptr<QGrpcCallReply> reply = m_client->SetStickerFavorite(request);
    m_setFavoriteReplies.insert(replyKey, reply);
    auto *rawReply = reply.get();
    connect(rawReply, &QGrpcCallReply::finished, this, [this, rawReply, replyKey, favorite](const QGrpcStatus &status) {
        const auto reply = m_setFavoriteReplies.take(replyKey);
        if (!reply || reply.get() != rawReply) {
            return;
        }
        if (!status.isOk()) {
            Q_EMIT stickerFavoriteFailed(status.message().isEmpty()
                                             ? (favorite ? i18nc("@info", "Unable to favorite the sticker")
                                                         : i18nc("@info", "Unable to unfavorite the sticker"))
                                             : status.message());
            return;
        }
        if (const auto response = rawReply->read<SetStickerFavoriteResponse>(); response && response->hasSticker()) {
            const QString key = response->sticker().cacheKey();
            if (favorite) {
                m_favoriteKeys.insert(key);
            } else {
                m_favoriteKeys.remove(key);
            }
            Q_EMIT favoritesChanged();
        }
    });
}

bool StickerController::isStickerFavorite(const QString &cacheKey)
{
    if (!m_favoriteKeysLoaded) {
        requestFavoriteKeys();
    }
    return m_favoriteKeys.contains(cacheKey);
}

// Fetches the favorite cache keys backing isStickerFavorite. Cheap (keys
// only), so a refresh on every favorites library change is fine.
void StickerController::requestFavoriteKeys()
{
    if (!clientReady() || m_favoriteKeysReply) {
        return;
    }

    ListStickersRequest request;
    request.setSource(StickerSource::STICKER_SOURCE_FAVORITE);

    m_favoriteKeysReply = m_client->ListStickers(request);
    auto *reply = m_favoriteKeysReply.get();
    connect(reply, &QGrpcCallReply::finished, this, [this, reply](const QGrpcStatus &status) {
        if (m_favoriteKeysReply.get() != reply) {
            return;
        }
        const auto owned = std::move(m_favoriteKeysReply);
        if (!status.isOk()) {
            return;
        }
        const auto response = owned->read<ListStickersResponse>();
        if (!response) {
            return;
        }
        QSet<QString> keys;
        for (const auto &sticker : response->stickers()) {
            keys.insert(sticker.cacheKey());
        }
        m_favoriteKeysLoaded = true;
        if (keys != m_favoriteKeys) {
            m_favoriteKeys = std::move(keys);
            Q_EMIT favoritesChanged();
        }
    });
}

void StickerController::requestList(View view, const QString &packId, const QString &query)
{
    if (!clientReady()) {
        return;
    }

    m_view = view;
    m_activePackId = view == View::Pack ? packId : QString();
    m_searchQuery = view == View::Search ? query : QString();
    if (view == View::Pack) {
        // Show whatever pack metadata the pack model already has while the
        // contents load.
        m_activePackName.clear();
        m_activePackInstalled = false;
    }
    m_loading = true;
    m_errorText.clear();
    const quint64 generation = ++m_viewGeneration;
    Q_EMIT viewChanged();

    auto finish = [this, generation](const QGrpcStatus &status, const QList<Sticker> &stickers) {
        if (generation != m_viewGeneration) {
            return;
        }
        m_loading = false;
        if (!status.isOk()) {
            m_errorText = status.message().isEmpty()
                ? i18nc("@info", "Unable to load stickers")
                : status.message();
            Q_EMIT viewChanged();
            return;
        }
        m_stickerModel->resetWith(stickers);
        // The grid hides not-yet-downloaded tiles, so fetch the misses in the
        // background; each row pops into the grid once its file is ready.
        prefetchStickers(stickers);
        Q_EMIT viewChanged();
    };

    if (view == View::Pack) {
        GetStickerPackRequest request;
        request.setPackId(packId);
        m_listReply = m_client->GetStickerPack(request);
        auto *reply = m_listReply.get();
        connect(reply, &QGrpcCallReply::finished, this, [this, reply, generation, finish](const QGrpcStatus &status) {
            if (m_listReply.get() != reply) {
                return;
            }
            QList<Sticker> stickers;
            if (status.isOk()) {
                if (const auto response = reply->read<GetStickerPackResponse>()) {
                    stickers = response->stickers();
                    if (generation == m_viewGeneration && response->hasPack()) {
                        m_activePackName = response->pack().name();
                        m_activePackInstalled = response->pack().installed();
                    }
                }
            }
            m_listReply.reset();
            finish(status, stickers);
        });
        return;
    }

    ListStickersRequest request;
    request.setLimit(kDefaultListLimit);
    switch (view) {
    case View::Recents:
        request.setSource(StickerSource::STICKER_SOURCE_RECENT);
        break;
    case View::Favorites:
        request.setSource(StickerSource::STICKER_SOURCE_FAVORITE);
        break;
    case View::All:
        request.setSource(StickerSource::STICKER_SOURCE_ALL);
        break;
    case View::Search:
        request.setQuery(query);
        break;
    case View::Pack:
        break;
    }

    m_listReply = m_client->ListStickers(request);
    auto *reply = m_listReply.get();
    connect(reply, &QGrpcCallReply::finished, this, [this, reply, finish](const QGrpcStatus &status) {
        if (m_listReply.get() != reply) {
            return;
        }
        QList<Sticker> stickers;
        if (status.isOk()) {
            if (const auto response = reply->read<ListStickersResponse>()) {
                stickers = response->stickers();
            }
        }
        m_listReply.reset();
        finish(status, stickers);
    });
}

void StickerController::requestPacks(bool forceRefresh)
{
    if (!clientReady() || m_packsReply) {
        return;
    }

    ListStickerPacksRequest request;
    request.setForceRefresh(forceRefresh);

    m_packsLoading = true;
    Q_EMIT viewChanged();

    m_packsReply = m_client->ListStickerPacks(request);
    auto *reply = m_packsReply.get();
    connect(reply, &QGrpcCallReply::finished, this, [this, reply](const QGrpcStatus &status) {
        if (m_packsReply.get() != reply) {
            return;
        }
        m_packsLoading = false;
        if (status.isOk()) {
            if (const auto response = reply->read<ListStickerPacksResponse>()) {
                applyPackList(response->packs());
            }
        }
        m_packsReply.reset();
        Q_EMIT viewChanged();
    });
}

void StickerController::applyPackList(const QList<StickerPack> &packs)
{
    m_packModel->resetWith(packs);
    m_installedPackModel->resetWith(packs);
}

bool StickerController::clientReady() const
{
    return m_client != nullptr;
}
