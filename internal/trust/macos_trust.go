//go:build darwin && cgo

package trust

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>

static OSStatus sim_add_trust(const UInt8 *bytes, CFIndex length) {
    CFDataRef data = CFDataCreate(NULL, bytes, length);
    if (!data) return errSecDecode;
    SecCertificateRef cert = SecCertificateCreateWithData(NULL, data);
    CFRelease(data);
    if (!cert) return errSecDecode;
    OSStatus status;
    SecTrustSettingsDomain domains[] = { kSecTrustSettingsDomainUser };
    CFArrayRef settings;
    status = SecTrustSettingsCopyTrustSettings(cert, kSecTrustSettingsDomainUser, &settings);
    if (status == errSecItemNotFound || status == errSecSuccess) {
        if (status == errSecSuccess) CFRelease(settings);
        SecPolicyRef policy = SecPolicyCreateSSL(true, NULL);
        SecTrustSettingsResult setting = kSecTrustSettingsResultTrustRoot;
        const void *values[] = { CFNumberCreate(NULL, kCFNumberIntType, &setting) };
        CFArrayRef array = CFArrayCreate(NULL, values, 1, &kCFTypeArrayCallBacks);
        CFRelease(values[0]);
        status = SecTrustSettingsSetTrustSettings(cert, kSecTrustSettingsDomainUser, array);
        CFRelease(array); CFRelease(policy);
    }
    CFRelease(cert);
    return status;
}

static OSStatus sim_remove_trust(const UInt8 *bytes, CFIndex length) {
    CFDataRef data = CFDataCreate(NULL, bytes, length);
    SecCertificateRef cert = data ? SecCertificateCreateWithData(NULL, data) : NULL;
    if (data) CFRelease(data);
    if (!cert) return errSecDecode;
    SecTrustSettingsRemoveTrustSettings(cert, kSecTrustSettingsDomainUser);
    const void *keys[] = { kSecClass, kSecValueRef };
    const void *values[] = { kSecClassCertificate, cert };
    CFDictionaryRef query = CFDictionaryCreate(NULL, keys, values, 2, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    OSStatus status = SecItemDelete(query);
    if (status == errSecItemNotFound) status = errSecSuccess;
    CFRelease(query); CFRelease(cert);
    return status;
}
*/
import "C"

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"unsafe"
)

func macOSCertificate(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if block, _ := pem.Decode(b); block != nil {
		b = block.Bytes
	}
	if _, err := x509.ParseCertificate(b); err != nil {
		return nil, fmt.Errorf("parse root certificate: %w", err)
	}
	return b, nil
}

func installMacOSTrust(path string) error {
	b, err := macOSCertificate(path)
	if err != nil {
		return err
	}
	if status := C.sim_add_trust((*C.UInt8)(unsafe.Pointer(&b[0])), C.CFIndex(len(b))); status != 0 {
		return fmt.Errorf("install macOS user Keychain trust: %d", status)
	}
	return nil
}

func removeMacOSTrust(path string) error {
	b, err := macOSCertificate(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if status := C.sim_remove_trust((*C.UInt8)(unsafe.Pointer(&b[0])), C.CFIndex(len(b))); status != 0 {
		return fmt.Errorf("remove macOS user Keychain trust: %d", status)
	}
	return nil
}
