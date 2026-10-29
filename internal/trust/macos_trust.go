//go:build darwin && cgo

package trust

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>

static OSStatus add_trust(const UInt8 *bytes, CFIndex length) {
    CFDataRef data = CFDataCreate(NULL, bytes, length);
    if (!data) return errSecAllocate;
    SecCertificateRef cert = SecCertificateCreateWithData(NULL, data);
    CFRelease(data);
    if (!cert) return errSecDecode;
    const void *keys[] = { kSecClass, kSecValueRef };
    const void *values[] = { kSecClassCertificate, cert };
    CFDictionaryRef query = CFDictionaryCreate(NULL, keys, values, 2, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    OSStatus status = SecItemAdd(query, NULL);
    if (status == errSecDuplicateItem) status = errSecSuccess;
    CFRelease(query);
    if (status == errSecSuccess) {
        SecPolicyRef policy = SecPolicyCreateSSL(true, NULL);
        const void *tkeys[] = { kSecTrustSettingsPolicy, kSecTrustSettingsResult };
        const void *tvalues[] = { policy, kSecTrustSettingsResultTrustRoot };
        CFDictionaryRef setting = CFDictionaryCreate(NULL, tkeys, tvalues, 2, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
        const void *settings[] = { setting };
        CFArrayRef array = CFArrayCreate(NULL, settings, 1, &kCFTypeArrayCallBacks);
        status = SecTrustSettingsSetTrustSettings(cert, kSecTrustSettingsDomainUser, array);
        CFRelease(array); CFRelease(setting); CFRelease(policy);
    }
    CFRelease(cert);
    return status;
}

static OSStatus remove_trust(const UInt8 *bytes, CFIndex length) {
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
	if status := C.add_trust((*C.UInt8)(unsafe.Pointer(&b[0])), C.CFIndex(len(b))); status != 0 {
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
	if status := C.remove_trust((*C.UInt8)(unsafe.Pointer(&b[0])), C.CFIndex(len(b))); status != 0 {
		return fmt.Errorf("remove macOS user Keychain trust: %d", status)
	}
	return nil
}
