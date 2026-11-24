//go:build darwin && cgo

package vault

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>
static CFMutableDictionaryRef query(const char *account) {
 CFMutableDictionaryRef q=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 CFStringRef a=CFStringCreateWithCString(NULL,account,kCFStringEncodingUTF8);
 CFDictionarySetValue(q,kSecClass,kSecClassGenericPassword);
 CFDictionarySetValue(q,kSecAttrService,CFSTR("org.netgo.device"));
 CFDictionarySetValue(q,kSecAttrAccount,a);CFRelease(a);return q;
}
static int readKey(const char *account,void **bytes,size_t *size) {
 CFMutableDictionaryRef q=query(account);CFDictionarySetValue(q,kSecReturnData,kCFBooleanTrue);
 CFTypeRef value=NULL;OSStatus status=SecItemCopyMatching(q,&value);CFRelease(q);
 if(status!=errSecSuccess)return (int)status;
 CFDataRef data=(CFDataRef)value;*size=(size_t)CFDataGetLength(data);*bytes=malloc(*size);
 if(!*bytes){CFRelease(value);return -1;}memcpy(*bytes,CFDataGetBytePtr(data),*size);CFRelease(value);return 0;
}
static int writeKey(const char *account,const void *bytes,size_t size) {
 CFMutableDictionaryRef q=query(account);CFDataRef data=CFDataCreate(NULL,bytes,(CFIndex)size);
 CFMutableDictionaryRef update=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 CFDictionarySetValue(update,kSecValueData,data);OSStatus status=SecItemUpdate(q,update);
 if(status==errSecItemNotFound){CFDictionarySetValue(q,kSecValueData,data);status=SecItemAdd(q,NULL);}
 CFRelease(update);CFRelease(data);CFRelease(q);return (int)status;
}
static void erase(void *bytes,size_t size){if(bytes){memset(bytes,0,size);free(bytes);}}
*/
import "C"

import (
	"errors"
	"netgo/internal/site"
	"os"
	"unsafe"
)

type Vault struct{ Root string }

func (v Vault) Read(id string) ([]byte, error) {
	if !site.ValidID(id) {
		return nil, errors.New("invalid vault ID")
	}
	a := C.CString(id)
	defer C.free(unsafe.Pointer(a))
	var bytes unsafe.Pointer
	var size C.size_t
	status := C.readKey(a, &bytes, &size)
	if status == C.errSecItemNotFound {
		return nil, os.ErrNotExist
	}
	if status != 0 {
		return nil, errors.New("keychain_read_failed")
	}
	defer C.erase(bytes, size)
	if size > 4096 {
		return nil, errors.New("invalid device key")
	}
	return C.GoBytes(bytes, C.int(size)), nil
}
func (v Vault) Write(id string, b []byte) error {
	if !site.ValidID(id) || len(b) == 0 || len(b) > 4096 {
		return errors.New("invalid device key")
	}
	a := C.CString(id)
	defer C.free(unsafe.Pointer(a))
	if C.writeKey(a, unsafe.Pointer(&b[0]), C.size_t(len(b))) != 0 {
		return errors.New("keychain_write_failed")
	}
	return nil
}
