package backup

import (
	"errors"

	"github.com/godbus/dbus/v5"

	"whatevrd/internal/app"
)

const (
	secretsService  = "org.freedesktop.secrets"
	secretsPath     = "/org/freedesktop/secrets"
	secretsIface    = "org.freedesktop.Secret.Service"
	collectionPath  = "/org/freedesktop/secrets/collection/login"
	collectionIface = "org.freedesktop.Secret.Collection"
	itemIface       = "org.freedesktop.Secret.Item"

	keyringApp  = "whatevr"
	keyringType = "backup-passphrase"
)

// ErrNoKeyring means no Secret Service (GNOME Keyring, KWallet's secret
// service, KeePassXC, …) answered on the session bus, or the lookup found
// nothing. Callers fall back to an explicit passphrase.
var ErrNoKeyring = errors.New("no keyring entry: no secret service or nothing stored")

func keyringAttributes() map[string]string {
	return map[string]string{"application": keyringApp, "type": keyringType}
}

type secretValue struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// SetBackupPassphrase stores the backup passphrase in the OS keyring under
// whatevr/backup-passphrase, replacing any previous entry. It fails when no
// secret service is reachable or the default collection is locked.
func SetBackupPassphrase(passphrase []byte) error {
	if len(passphrase) == 0 {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "passphrase is required")
	}
	if len(passphrase) > maxPassphrase {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "passphrase is too long")
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return ErrNoKeyring
	}
	service := conn.Object(secretsService, secretsPath)
	var session dbus.ObjectPath
	if call := service.Call(secretsIface+".OpenSession", 0, "plain", dbus.MakeVariant("")); call.Err != nil {
		return ErrNoKeyring
	} else if err := call.Store(new(dbus.Variant), &session); err != nil {
		return ErrNoKeyring
	}
	collection := conn.Object(secretsService, collectionPath)
	secret := secretValue{Session: session, Value: append([]byte(nil), passphrase...), ContentType: "text/plain"}
	properties := map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant("whatevr backup passphrase"),
		"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(keyringAttributes()),
	}
	var item dbus.ObjectPath
	if call := collection.Call(collectionIface+".CreateItem", 0, properties, secret, true); call.Err != nil {
		return errors.New("store passphrase in keyring: " + call.Err.Error())
	} else if err := call.Store(&item, new(dbus.ObjectPath)); err != nil {
		return errors.New("store passphrase in keyring: " + err.Error())
	}
	_ = item
	return nil
}

// GetBackupPassphrase reads the backup passphrase from the OS keyring.
func GetBackupPassphrase() ([]byte, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, ErrNoKeyring
	}
	service := conn.Object(secretsService, secretsPath)
	var session dbus.ObjectPath
	if call := service.Call(secretsIface+".OpenSession", 0, "plain", dbus.MakeVariant("")); call.Err != nil {
		return nil, ErrNoKeyring
	} else if err := call.Store(new(dbus.Variant), &session); err != nil {
		return nil, ErrNoKeyring
	}
	var unlocked, locked []dbus.ObjectPath
	if call := service.Call(secretsIface+".SearchItems", 0, keyringAttributes()); call.Err != nil {
		return nil, ErrNoKeyring
	} else if err := call.Store(&unlocked, &locked); err != nil {
		return nil, ErrNoKeyring
	}
	if len(unlocked) == 0 {
		if len(locked) > 0 {
			return nil, errors.New("keyring is locked: unlock it and retry")
		}
		return nil, ErrNoKeyring
	}
	item := conn.Object(secretsService, unlocked[0])
	var secret secretValue
	if call := item.Call(itemIface+".GetSecret", 0, session); call.Err != nil {
		return nil, ErrNoKeyring
	} else if err := call.Store(&secret); err != nil {
		return nil, ErrNoKeyring
	}
	if len(secret.Value) == 0 {
		return nil, ErrNoKeyring
	}
	return secret.Value, nil
}
