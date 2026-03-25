//go:build linux && cgo

package main

/*
#cgo LDFLAGS: -lnss3 -lnspr4 -lplc4 -lplds4
#include <stdlib.h>

typedef struct SECItemCompatStr {
	unsigned int type;
	unsigned char *data;
	unsigned int len;
} SECItemCompat;

extern int NSS_Init(const char *configdir);
extern int NSS_Shutdown(void);
extern void *PK11_GetInternalKeySlot(void);
extern void PK11_FreeSlot(void *slot);
extern int PK11_Authenticate(void *slot, int loadCerts, void *wincx);
extern int PK11SDR_Decrypt(SECItemCompat *data, SECItemCompat *result, void *cx);
extern void SECITEM_FreeItem(SECItemCompat *zap, int freeit);
*/
import "C"

import (
	"encoding/base64"
	"fmt"
	"sync"
	"unsafe"
)

var nssMu sync.Mutex

func nssDirectSendCompiled() bool {
	return true
}

func nssBuildDetail() string {
	return "compiled with NSS-backed secret decryption for direct send"
}

func decryptNSSSecret(profilePath, ciphertext string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("empty ciphertext")
	}
	cipherBuf := C.CBytes(raw)
	defer C.free(cipherBuf)

	nssMu.Lock()
	defer nssMu.Unlock()

	config := C.CString("sql:" + profilePath)
	defer C.free(unsafe.Pointer(config))

	if rv := C.NSS_Init(config); rv != 0 {
		return "", fmt.Errorf("NSS_Init failed")
	}
	defer C.NSS_Shutdown()

	slot := C.PK11_GetInternalKeySlot()
	if slot == nil {
		return "", fmt.Errorf("PK11_GetInternalKeySlot failed")
	}
	defer C.PK11_FreeSlot(slot)

	if rv := C.PK11_Authenticate(slot, 1, nil); rv != 0 {
		return "", fmt.Errorf("PK11_Authenticate failed")
	}

	in := C.SECItemCompat{
		data: (*C.uchar)(cipherBuf),
		len:  C.uint(len(raw)),
	}
	var out C.SECItemCompat
	if rv := C.PK11SDR_Decrypt(&in, &out, nil); rv != 0 {
		return "", fmt.Errorf("PK11SDR_Decrypt failed")
	}
	defer C.SECITEM_FreeItem(&out, 0)

	plain := C.GoBytes(unsafe.Pointer(out.data), C.int(out.len))
	return string(plain), nil
}
