//go:build windows

package fsx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// create makes the temporary file with its access list from the
// start, so nobody else can open it even for a moment: private, the
// user and SYSTEM only; replacing a file whose list is protected,
// that file's list (a rename keeps the moved file's own list, so this
// is what the file has after); else the folder's, as any new file.
// Nobody else may open it while it is being written (share mode 0).
func create(name string, private bool, target string,
	like fs.FileInfo) (*os.File, error) {
	sd, err := birthSD(private, target, like)
	if err != nil {
		return nil, err
	}
	var sa *windows.SecurityAttributes
	if sd != nil {
		sa = &windows.SecurityAttributes{
			Length: uint32(unsafe.Sizeof(
				windows.SecurityAttributes{})),
			SecurityDescriptor: sd,
		}
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	h, err := windows.CreateFile(p,
		windows.GENERIC_READ|windows.GENERIC_WRITE, 0, sa,
		windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_EXISTS) {
			err = fs.ErrExist
		}
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(h), name), nil
}

func birthSD(private bool, target string,
	like fs.FileInfo) (*windows.SECURITY_DESCRIPTOR, error) {
	if private {
		return privateSD(false)
	}
	if like == nil {
		return nil, nil
	}
	sd, err := windows.GetNamedSecurityInfo(target,
		windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	control, _, err := sd.Control()
	if err != nil {
		return nil, err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return nil, nil // it takes the folder's: so will the new one
	}
	return sd, nil
}

// privateSD is D:P(A;;FA;;;<user>)(A;;FA;;;SY); for a folder each
// entry is handed on to what is made in it (OICI).
func privateSD(folder bool) (*windows.SECURITY_DESCRIPTOR, error) {
	sid, err := currentSID()
	if err != nil {
		return nil, err
	}
	inherit := ""
	if folder {
		inherit = "OICI"
	}
	return windows.SecurityDescriptorFromString(fmt.Sprintf(
		"D:P(A;%s;FA;;;%s)(A;%s;FA;;;SY)", inherit, sid.String(),
		inherit))
}

// currentSID is the user this program runs as.
func currentSID() (*windows.SID, error) {
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid, nil
}

func private(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	// D:P protects the list from the folder's inherited entries.
	sd, err := privateSD(st.IsDir())
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|
			windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}

// The kinds of access list entry that let someone in. Only the plain
// kind (and its callback form, laid out the same) is read for whom;
// an entry of the others reads as letting someone else in.
const (
	aceAllowed               = 0x0
	aceAllowedCompound       = 0x4
	aceAllowedObject         = 0x5
	aceAllowedCallback       = 0x9
	aceAllowedCallbackObject = 0xb
)

func isPrivate(path string) (bool, string, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|
			windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false, "", err
	}
	me, err := currentSID()
	if err != nil {
		return false, "", err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false, "", err
	}
	admins, err := windows.CreateWellKnownSid(
		windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false, "", err
	}
	if owner, _, err := sd.Owner(); err == nil && owner != nil &&
		!owner.Equals(me) && !owner.Equals(system) &&
		!owner.Equals(admins) {
		return false, "it belongs to " + account(owner) + ", who may " +
			"change who may read it", nil
	}
	control, _, err := sd.Control()
	if err != nil {
		return false, "", err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return false, "it takes its access list from its folder, " +
			"so others who may open the folder may read it", nil
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false, "", err
	}
	if dacl == nil {
		return false, "it has no access list, so anyone may read it",
			nil
	}
	var others []string
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return false, "", err
		}
		switch ace.Header.AceType {
		case aceAllowed, aceAllowedCallback:
		case aceAllowedCompound, aceAllowedObject,
			aceAllowedCallbackObject:
			others = append(others, "an entry of a kind lvp "+
				"does not read")
			continue
		default:
			continue // a deny entry, or not about access
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(me) || sid.Equals(system) {
			continue
		}
		others = append(others, account(sid))
	}
	if len(others) > 0 {
		return false, "its access list also lets in " +
			strings.Join(others, ", "), nil
	}
	return true, "", nil
}

// account is a SID's DOMAIN\name, or the SID itself.
func account(sid *windows.SID) string {
	name := sid.String()
	if acct, dom, _, err := sid.LookupAccount(""); err == nil {
		name = acct
		if dom != "" {
			name = dom + `\` + acct
		}
	}
	return name
}

// inUse is a sharing or lock violation, or access denied, which is
// what Windows says to a rename or remove while the file is open
// without FILE_SHARE_DELETE.
func inUse(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
