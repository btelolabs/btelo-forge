//go:build darwin

package vault

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Security -framework Foundation -framework LocalAuthentication
#import <Foundation/Foundation.h>
#import <Security/Security.h>
#import <LocalAuthentication/LocalAuthentication.h>
#include <stdlib.h>
#include <string.h>

static LAContext *forge_no_ui_context(void) {
	LAContext *ctx = [[LAContext alloc] init];
	ctx.interactionNotAllowed = YES;
	return ctx;
}

// forge_disable_ui disables legacy SecKeychain modal dialogs (SecurityAgent) for the duration of a call
// so locked keychains, custom test HOME directories, or ACL mismatches never hang headless/test runs.
static Boolean forge_disable_ui(void) {
	Boolean prev = true;
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
	SecKeychainGetUserInteractionAllowed(&prev);
	SecKeychainSetUserInteractionAllowed(false);
#pragma clang diagnostic pop
	return prev;
}

static void forge_restore_ui(Boolean prev) {
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
	SecKeychainSetUserInteractionAllowed(prev);
#pragma clang diagnostic pop
}

// forge_keychain_read reads a generic password item without ever displaying a blocking GUI prompt.
// Returns: 0 = found (*outData malloc'd, *outLen set), 1 = not found, <0 = OSStatus error.
static int forge_keychain_read(const char *service, const char *account, unsigned char **outData, int *outLen) {
	*outData = NULL;
	*outLen = 0;
	Boolean prevUI = forge_disable_ui();
	int rc = 1;
	@autoreleasepool {
		NSDictionary *query = @{
			(__bridge id)kSecClass: (__bridge id)kSecClassGenericPassword,
			(__bridge id)kSecAttrService: [NSString stringWithUTF8String:service],
			(__bridge id)kSecAttrAccount: [NSString stringWithUTF8String:account],
			(__bridge id)kSecReturnData: @YES,
			(__bridge id)kSecMatchLimit: (__bridge id)kSecMatchLimitOne,
			(__bridge id)kSecUseAuthenticationContext: forge_no_ui_context(),
		};
		CFTypeRef result = NULL;
		OSStatus status = SecItemCopyMatching((__bridge CFDictionaryRef)query, &result);
		if (status == errSecItemNotFound) {
			rc = 1;
		} else if (status != errSecSuccess || result == NULL) {
			if (result) CFRelease(result);
			rc = (int)status;
		} else {
			NSData *data = (__bridge_transfer NSData *)result;
			if (data.length == 0) {
				rc = 1;
			} else {
				unsigned char *buf = (unsigned char *)malloc(data.length);
				if (!buf) {
					rc = -1;
				} else {
					memcpy(buf, data.bytes, data.length);
					*outData = buf;
					*outLen = (int)data.length;
					rc = 0;
				}
			}
		}
	}
	forge_restore_ui(prevUI);
	return rc;
}

// forge_make_user_access creates a SecAccessRef that allows any process running as the signed-in user on this Mac
// to read the item silently without triggering SecurityAgent ACL prompts (matching Windows DPAPI scope).
static SecAccessRef forge_make_user_access(CFStringRef label) {
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
	SecAccessRef access = NULL;
	CFArrayRef empty = CFArrayCreate(kCFAllocatorDefault, NULL, 0, &kCFTypeArrayCallBacks);
	OSStatus st = SecAccessCreate(label, empty, &access);
	if (empty) CFRelease(empty);
	if (st != errSecSuccess || access == NULL) {
		return NULL;
	}
	// Copy ACLs for decrypt authorization and set trustedList = NULL ("allow any application" for this user).
	CFArrayRef acls = SecAccessCopyMatchingACLList(access, kSecACLAuthorizationDecrypt);
	if (acls != NULL) {
		CFIndex n = CFArrayGetCount(acls);
		for (CFIndex i = 0; i < n; i++) {
			SecACLRef acl = (SecACLRef)CFArrayGetValueAtIndex(acls, i);
			SecACLSetContents(acl, NULL, label, 0);
		}
		CFRelease(acls);
	}
	return access;
#pragma clang diagnostic pop
}

// forge_keychain_add stores a generic password item accessible to the signed-in user's processes on this Mac.
// Returns: 0 = added, 1 = duplicate item, <0 = OSStatus error.
static int forge_keychain_add(const char *service, const char *account, const unsigned char *data, int dataLen) {
	Boolean prevUI = forge_disable_ui();
	int rc = -1;
	@autoreleasepool {
		NSString *svc = [NSString stringWithUTF8String:service];
		NSString *acc = [NSString stringWithUTF8String:account];
		NSData *val = [NSData dataWithBytes:data length:(NSUInteger)dataLen];

		NSMutableDictionary *attrs = [@{
			(__bridge id)kSecClass: (__bridge id)kSecClassGenericPassword,
			(__bridge id)kSecAttrService: svc,
			(__bridge id)kSecAttrAccount: acc,
			(__bridge id)kSecAttrLabel: @"Btelo Forge Vault Key",
			(__bridge id)kSecAttrAccessible: (__bridge id)kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
			(__bridge id)kSecValueData: val,
			(__bridge id)kSecUseAuthenticationContext: forge_no_ui_context(),
		} mutableCopy];

		SecAccessRef access = forge_make_user_access((__bridge CFStringRef)@"Btelo Forge Vault Key");
		if (access != NULL) {
			attrs[(__bridge id)kSecAttrAccess] = (__bridge id)access;
		}

		OSStatus status = SecItemAdd((__bridge CFDictionaryRef)attrs, NULL);
		if (access != NULL) {
			CFRelease(access);
		}
		if (status == errSecDuplicateItem) {
			rc = 1;
		} else {
			rc = (status == errSecSuccess) ? 0 : (int)status;
		}
	}
	forge_restore_ui(prevUI);
	return rc;
}

static int forge_keychain_delete(const char *service, const char *account) {
	Boolean prevUI = forge_disable_ui();
	int rc = 0;
	@autoreleasepool {
		NSDictionary *query = @{
			(__bridge id)kSecClass: (__bridge id)kSecClassGenericPassword,
			(__bridge id)kSecAttrService: [NSString stringWithUTF8String:service],
			(__bridge id)kSecAttrAccount: [NSString stringWithUTF8String:account],
		};
		OSStatus status = SecItemDelete((__bridge CFDictionaryRef)query);
		rc = (status == errSecSuccess || status == errSecItemNotFound) ? 0 : (int)status;
	}
	forge_restore_ui(prevUI);
	return rc;
}
*/
import "C"

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"
)

const (
	vaultVersion    byte = 1
	masterKeyBytes       = 32
	keychainAccount      = "master-key"
)

var (
	keychainService = "com.btelolabs.forge.vault"

	keyMu      sync.Mutex
	cachedKey  []byte
	cachedHome string
)

func keychainRead(service, account string) ([]byte, bool, error) {
	cs := C.CString(service)
	ca := C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))

	var ptr *C.uchar
	var n C.int
	rc := int(C.forge_keychain_read(cs, ca, &ptr, &n))
	if rc == 1 {
		return nil, false, nil
	}
	if rc != 0 {
		return nil, false, fmt.Errorf("keychain read failed (OSStatus %d)", rc)
	}
	defer C.free(unsafe.Pointer(ptr))
	return C.GoBytes(unsafe.Pointer(ptr), n), true, nil
}

func keychainAdd(service, account string, data []byte) (dup bool, err error) {
	cs := C.CString(service)
	ca := C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))

	rc := int(C.forge_keychain_add(cs, ca, (*C.uchar)(unsafe.Pointer(&data[0])), C.int(len(data))))
	if rc == 1 {
		return true, nil
	}
	if rc != 0 {
		return false, fmt.Errorf("keychain add failed (OSStatus %d)", rc)
	}
	return false, nil
}

func keychainDelete(service, account string) error {
	cs := C.CString(service)
	ca := C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))

	if rc := int(C.forge_keychain_delete(cs, ca)); rc != 0 {
		return fmt.Errorf("keychain delete failed (OSStatus %d)", rc)
	}
	return nil
}

// fallbackKeyPath returns a user-only (0600) key file path inside the current HOME when the macOS Login Keychain
// is unavailable (such as headless CI or tests with an isolated temporary HOME).
func fallbackKeyPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Btelo Forge", ".vault-key"), nil
}

func loadFallbackKey(create bool) ([]byte, error) {
	path, err := fallbackKeyPath()
	if err != nil {
		return nil, err
	}
	if data, err := os.ReadFile(path); err == nil && len(data) == masterKeyBytes {
		return data, nil
	}
	if !create {
		return nil, errors.New("no fallback vault key")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	key := make([]byte, masterKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			if data, rerr := os.ReadFile(path); rerr == nil && len(data) == masterKeyBytes {
				return data, nil
			}
		}
		return nil, err
	}
	_, werr := f.Write(key)
	cerr := f.Close()
	if werr != nil {
		_ = os.Remove(path)
		return nil, werr
	}
	if cerr != nil {
		_ = os.Remove(path)
		return nil, cerr
	}
	return key, nil
}

// loadMasterKey retrieves the per-user 32-byte AES-256 master key from the macOS Login Keychain,
// creating it if create is true and no key exists yet. When the Login Keychain is unavailable (e.g. locked headless
// session or isolated test HOME), it falls back to a 0600 key file under the user's config directory so secrets
// remain encrypted on disk without ever blocking on a modal prompt.
func loadMasterKey(create bool) ([]byte, error) {
	keyMu.Lock()
	defer keyMu.Unlock()
	home := os.Getenv("HOME")
	if len(cachedKey) == masterKeyBytes && cachedHome == home {
		return cachedKey, nil
	}
	if key, ok, err := keychainRead(keychainService, keychainAccount); err == nil && ok && len(key) == masterKeyBytes {
		cachedKey, cachedHome = key, home
		return cachedKey, nil
	} else if err == nil && !ok && create {
		key := make([]byte, masterKeyBytes)
		if _, rerr := rand.Read(key); rerr != nil {
			return nil, rerr
		}
		dup, aerr := keychainAdd(keychainService, keychainAccount, key)
		if aerr == nil {
			if dup {
				if existing, found, rerr := keychainRead(keychainService, keychainAccount); rerr == nil && found && len(existing) == masterKeyBytes {
					cachedKey, cachedHome = existing, home
					return cachedKey, nil
				}
			} else {
				cachedKey, cachedHome = key, home
				return cachedKey, nil
			}
		}
	}
	key, err := loadFallbackKey(create)
	if err != nil {
		return nil, err
	}
	cachedKey, cachedHome = key, home
	return cachedKey, nil
}

func protect(plain []byte) ([]byte, bool, error) {
	if len(plain) == 0 {
		return nil, false, errors.New("nothing to protect")
	}
	key, err := loadMasterKey(true)
	if err != nil {
		// If the keychain is unavailable (e.g. locked headless environment or isolated test HOME),
		// fall back to unsealed storage rather than failing writes or hanging on a modal prompt.
		return nil, false, nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, false, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, false, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, false, err
	}
	out := make([]byte, 0, 1+len(nonce)+len(plain)+gcm.Overhead())
	out = append(out, vaultVersion)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plain, nil)
	return out, true, nil
}

func unprotect(sealed []byte) ([]byte, error) {
	if len(sealed) < 1 {
		return nil, errors.New("empty sealed data")
	}
	if sealed[0] != vaultVersion {
		return nil, errors.New("unknown sealed format")
	}
	key, err := loadMasterKey(false)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(sealed) < 1+ns+gcm.Overhead() {
		return nil, errors.New("sealed data too short")
	}
	nonce := sealed[1 : 1+ns]
	ciphertext := sealed[1+ns:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
