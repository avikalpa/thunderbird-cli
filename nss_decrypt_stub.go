//go:build !linux || !cgo

package main

import "fmt"

func nssDirectSendCompiled() bool {
	return false
}

func nssBuildDetail() string {
	return "built without NSS-backed secret decryption for direct send"
}

func decryptNSSSecret(profilePath, ciphertext string) (string, error) {
	return "", fmt.Errorf("direct secret-backed send is unavailable in this build; rebuild on Linux with cgo and NSS runtime libraries")
}

func encryptNSSSecret(profilePath, plaintext string) (string, error) {
	return "", fmt.Errorf("storing credentials requires a Linux cgo build with NSS runtime libraries")
}
