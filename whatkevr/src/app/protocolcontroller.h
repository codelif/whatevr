#pragma once

#include <QObject>
#include <QString>
#include <QVariantMap>
#include <qqmlintegration.h>

#include <cstdint>

QT_BEGIN_NAMESPACE
class QQmlEngine;
class QJSEngine;
class QTimer;
QT_END_NAMESPACE

namespace whatevr::proto
{
class ProtocolClient;
class ObjectViewModel;
class Subscription;
} // namespace whatevr::proto

// The whatevr-protocol counterpart of AppController's connection lifecycle: it
// owns the ProtocolClient (the single socket to the daemon's PROTOCOL.md
// surface) and subscribes to the `connection` and `login` object views,
// deriving from them every string the status/login/splash pages bind to. During
// the D-phase migration it runs *alongside* the still-gRPC AppController — this
// singleton drives the pre-shell screens and the shell-visibility gate, while
// AppController keeps serving the not-yet-ported chat shell over gRPC until D7.
//
// Ported pages bind `Whatevr.ProtocolController.<prop>` exactly as they used to
// bind AppController; each later D-step moves one more page's bindings across
// and subscribes its views through client() on this same connection.
class ProtocolController final : public QObject
{
    Q_OBJECT
    QML_NAMED_ELEMENT(ProtocolController)
    QML_SINGLETON

    // Shell routing (mirrors AppController's gate, now protocol-driven).
    Q_PROPERTY(bool starting READ starting NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool loginRequired READ loginRequired NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool shellVisible READ shellVisible NOTIFY stateChanged FINAL)

    // Status page.
    Q_PROPERTY(QString connectionPhase READ connectionPhase NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool daemonRunning READ daemonRunning NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool loading READ loading NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString statusTitle READ statusTitle NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString statusText READ statusText NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString detailText READ detailText NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString bannerText READ bannerText NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString actionError READ actionError NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString primaryActionText READ primaryActionText NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool primaryActionEnabled READ primaryActionEnabled NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString daemonServiceCommand READ daemonServiceCommand CONSTANT FINAL)
    Q_PROPERTY(QString daemonBinaryCommand READ daemonBinaryCommand CONSTANT FINAL)
    Q_PROPERTY(QString daemonInstructions READ daemonInstructions CONSTANT FINAL)

    // Login page.
    Q_PROPERTY(bool qrAvailable READ qrAvailable NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString qrCode READ qrCode NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString qrExpiryText READ qrExpiryText NOTIFY stateChanged FINAL)

public:
    static void setInstance(ProtocolController *instance);
    static ProtocolController *create(QQmlEngine *qmlEngine, QJSEngine *jsEngine);

    explicit ProtocolController(QObject *parent = nullptr);
    // Test seam: connect to an explicit socket path instead of the XDG default.
    ProtocolController(QString socketPath, QObject *parent);
    ~ProtocolController() override;

    // Begin connecting to the daemon and subscribe the connection/login views.
    // Idempotent; called once from main() after the event loop is up.
    void start();

    // The shared socket to the daemon, so later D-steps subscribe more views on
    // the same connection. Never null after construction.
    [[nodiscard]] whatevr::proto::ProtocolClient *client() const { return m_client; }

    [[nodiscard]] bool starting() const;
    [[nodiscard]] bool loginRequired() const;
    [[nodiscard]] bool shellVisible() const;
    [[nodiscard]] QString connectionPhase() const;
    [[nodiscard]] bool daemonRunning() const;
    [[nodiscard]] bool loading() const;
    [[nodiscard]] QString statusTitle() const;
    [[nodiscard]] QString statusText() const;
    [[nodiscard]] QString detailText() const;
    [[nodiscard]] QString bannerText() const;
    [[nodiscard]] QString actionError() const;
    [[nodiscard]] QString primaryActionText() const;
    [[nodiscard]] bool primaryActionEnabled() const;
    [[nodiscard]] QString daemonServiceCommand() const;
    [[nodiscard]] QString daemonBinaryCommand() const;
    [[nodiscard]] QString daemonInstructions() const;
    [[nodiscard]] bool qrAvailable() const;
    [[nodiscard]] QString qrCode() const;
    [[nodiscard]] QString qrExpiryText() const;

    Q_INVOKABLE void startDaemon();
    Q_INVOKABLE void triggerPrimaryAction();
    Q_INVOKABLE void copyToClipboard(const QString &text);

    // The daemon's protocol socket, `$XDG_RUNTIME_DIR/whatevr/whatevrd.sock`
    // (distinct from the gRPC socket under whatevrd/). Empty if XDG_RUNTIME_DIR
    // is unset.
    [[nodiscard]] static QString daemonSocketPath();

Q_SIGNALS:
    void stateChanged();

private:
    // Transport reachability, independent of the daemon-reported WhatsApp state.
    enum class Phase : std::uint8_t {
        Connecting, // socket connecting, or within the cold-start grace
        Connected,  // hello acknowledged; the connection view is authoritative
        NotRunning, // no socket and grace elapsed — the daemon isn't up
    };
    [[nodiscard]] Phase phase() const;
    [[nodiscard]] bool daemonSocketExists() const;

    // The daemon connection state token from the `connection` view
    // (need_login/connecting/online/reconnecting/offline/starting), or empty
    // when the view has not filled.
    [[nodiscard]] QString daemonState() const;
    [[nodiscard]] bool canReconnect() const;

    void onClientReady();
    void onClientDisconnected();
    void onConnectionValueChanged();
    void onLoginValueChanged();
    void refreshQrExpiry();
    void requestReconnect();
    void launchDaemonBinary();

    QString m_socketPath;
    whatevr::proto::ProtocolClient *m_client = nullptr;
    whatevr::proto::ObjectViewModel *m_connectionModel = nullptr;
    whatevr::proto::ObjectViewModel *m_loginModel = nullptr;
    whatevr::proto::Subscription *m_connectionSub = nullptr;
    whatevr::proto::Subscription *m_loginSub = nullptr;

    bool m_clientReady = false;
    bool m_startupGrace = true;
    bool m_reconnectInFlight = false;
    QString m_bannerText;
    QString m_actionError;

    QTimer *m_startupGraceTimer = nullptr;
    QTimer *m_qrTimer = nullptr;
};
