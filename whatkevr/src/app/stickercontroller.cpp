#include "stickercontroller.h"

#include <QGrpcCallReply>
#include <QTimer>

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
}

StickerController::StickerController(AppController *appController)
    : QObject(appController)
    , m_appController(appController)
    , m_stickerModel(new StickerModel(this))
    , m_packModel(new StickerPackModel(this, false))
    , m_installedPackModel(new StickerPackModel(this, true))
    , m_searchTimer(new QTimer(this))
{
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
    return m_stickerModel;
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
    if (!clientReady() || cacheKey.isEmpty() || m_downloadReplies.contains(cacheKey)) {
        return;
    }

    DownloadStickerRequest request;
    request.setCacheKey(cacheKey);

    std::shared_ptr<QGrpcCallReply> reply = m_client->DownloadSticker(request);
    m_downloadReplies.insert(cacheKey, reply);
    auto *rawReply = reply.get();
    connect(rawReply, &QGrpcCallReply::finished, this, [this, rawReply, cacheKey](const QGrpcStatus &status) {
        const auto reply = m_downloadReplies.take(cacheKey);
        if (!reply || reply.get() != rawReply) {
            return;
        }
        if (!status.isOk()) {
            return;
        }
        if (const auto response = rawReply->read<DownloadStickerResponse>(); response && response->hasSticker()) {
            // The response key may differ from the requested one when a
            // provisional favorite was canonicalized during download.
            m_stickerModel->updateSticker(response->sticker(), cacheKey);
        }
    });
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
