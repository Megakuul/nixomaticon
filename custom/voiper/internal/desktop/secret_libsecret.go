//go:build secretservice && cgo

package desktop

/*
#cgo pkg-config: libsecret-1
#include <libsecret/secret.h>
#include <stdlib.h>
static const SecretSchema voiper_schema = {
  "com.megakuul.voiper.account", SECRET_SCHEMA_NONE,
  {{"account", SECRET_SCHEMA_ATTRIBUTE_STRING}, {NULL, 0}}
};
static gboolean voiper_store(const char *account, const char *password, GCancellable *cancel, GError **err) {
  return secret_password_store_sync(&voiper_schema, SECRET_COLLECTION_DEFAULT,
    "Voiper SIP account", password, cancel, err, "account", account, NULL);
}
static gchar *voiper_lookup(const char *account, GCancellable *cancel, GError **err) {
  return secret_password_lookup_sync(&voiper_schema, cancel, err, "account", account, NULL);
}
static gboolean voiper_clear(const char *account, GCancellable *cancel, GError **err) {
  return secret_password_clear_sync(&voiper_schema, cancel, err, "account", account, NULL);
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"
)

const secretServiceBuilt = true

func secretOperation(ctx context.Context, operation, account, password string) (string, error) {
	if account == "" || len(account) > 512 || len(password) > 16384 || strings.ContainsRune(account, 0) || strings.ContainsRune(password, 0) || !utf8.ValidString(account) || !utf8.ValidString(password) {
		return "", errors.New("invalid Secret Service account or password")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cancellable := C.g_cancellable_new()
	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { C.g_cancellable_cancel(cancellable); close(cancelled) })
	defer func() {
		if !stop() {
			<-cancelled
		}
		C.g_object_unref(C.gpointer(cancellable))
	}()
	cAccount := C.CString(account)
	defer C.free(unsafe.Pointer(cAccount))
	var failure *C.GError
	var result string
	switch operation {
	case "set":
		cPassword := C.CString(password)
		ok := C.voiper_store(cAccount, cPassword, cancellable, &failure)
		C.secret_password_free(cPassword)
		if ok == 0 && failure == nil {
			return "", errors.New("Secret Service did not save password")
		}
	case "get":
		value := C.voiper_lookup(cAccount, cancellable, &failure)
		if value != nil {
			result = C.GoString(value)
			C.secret_password_free(value)
		} else if failure == nil {
			return "", ErrSecretNotFound
		}
	case "delete":
		C.voiper_clear(cAccount, cancellable, &failure)
	default:
		return "", errors.New("invalid Secret Service operation")
	}
	if failure != nil {
		defer C.g_error_free(failure)
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("Secret Service: %s", C.GoString(failure.message))
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return result, nil
}
