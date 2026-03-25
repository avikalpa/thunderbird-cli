//go:build !linux || !cgo

package main

import "fmt"

func nssDirectSendCompiled() bool {
	return false
}

func nssBuildDetail() string {
	return "built without NSS-backed direct OAuth send"
}

func decryptNSSSecret(profilePath, ciphertext string) (string, error) {
	return "", fmt.Errorf("direct OAuth send is unavailable in this build; rebuild on Linux with cgo and NSS runtime libraries")
}
