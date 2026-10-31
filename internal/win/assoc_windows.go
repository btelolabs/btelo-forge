package win

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// File associations, per user, as Windows wants an application to declare them: a ProgID for each kind of file it
// opens, the extensions listing it among their "Open with" programs, and its capabilities under
// RegisteredApplications so it shows in Settings → Default apps. Windows lets only the user choose the default
// program of a common type (a protected UserChoice), so for those this offers the app and opens the Settings page
// for it; the app's own file types, which no other program claims, it makes the default outright.

// Association is a kind of file the app opens: its ProgID, the name Explorer shows for it, and its extensions;
// Own says the app's own formats, whose default it may set; Preview that its files end with a small picture
// (internal/thumb), which Explorer is to show.
type Association struct {
	ProgID  string
	Name    string
	Exts    []string
	Own     bool
	Preview bool
}

// AssocRoot is where associations are written: Software under the current user. Tests point it elsewhere.
type AssocRoot struct {
	Software string // the path standing for HKEY_CURRENT_USER\Software
	App      string // the application's key under Software, such as BteloLabs\BteloForge
	Name     string // the application's name in RegisteredApplications and Default apps
	Exe      string // the program, its full path

	Thumbnailer string // the thumbnail DLL, its full path; empty when there is none
}

// UserRoot is the real place, for the program at exe.
func UserRoot(name, exe string) AssocRoot {
	return AssocRoot{Software: "Software", App: `BteloLabs\BteloForge`, Name: name, Exe: exe}
}

func (r AssocRoot) classes() string      { return r.Software + `\Classes` }
func (r AssocRoot) capabilities() string { return r.Software + `\` + r.App + `\Capabilities` }
func (r AssocRoot) command() string      { return `"` + r.Exe + `" "%1"` }
func (r AssocRoot) application() string {
	return r.classes() + `\Applications\` + filepath.Base(r.Exe)
}

func setString(path, name, value string) error {
	return setStringIn(registry.CURRENT_USER, path, name, value)
}

func setStringIn(root registry.Key, path, name, value string) error {
	k, _, err := registry.CreateKey(root, path, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

func getString(path, name string) string { return getStringIn(registry.CURRENT_USER, path, name) }

func getStringIn(root registry.Key, path, name string) string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return v
}

func deleteValue(path, name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// deleteTree removes a key with everything under it.
func deleteTree(path string) error { return deleteTreeIn(registry.CURRENT_USER, path) }

func deleteTreeIn(root registry.Key, path string) error {
	k, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	subs, err := k.ReadSubKeyNames(-1)
	k.Close()
	if err != nil {
		return err
	}
	for _, s := range subs {
		if err := deleteTreeIn(root, path+`\`+s); err != nil {
			return err
		}
	}
	if err := registry.DeleteKey(root, path); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// Register declares the app as a program for the associations, adding to what is declared already.
func Register(r AssocRoot, list []Association, description string) error {
	icon := `"` + r.Exe + `",0`
	for _, a := range list {
		prog := r.classes() + `\` + a.ProgID
		for _, kv := range [][3]string{
			{prog, "", a.Name},
			{prog, "FriendlyTypeName", a.Name},
			{prog + `\DefaultIcon`, "", icon},
			{prog + `\shell\open\command`, "", r.command()},
		} {
			if err := setString(kv[0], kv[1], kv[2]); err != nil {
				return err
			}
		}
		for _, ext := range a.Exts {
			if err := setString(r.classes()+`\`+ext+`\OpenWithProgids`, a.ProgID, ""); err != nil {
				return err
			}
			if err := setString(r.capabilities()+`\FileAssociations`, ext, a.ProgID); err != nil {
				return err
			}
			if err := setString(r.application()+`\SupportedTypes`, ext, ""); err != nil {
				return err
			}
			if a.Own {
				if err := setString(r.classes()+`\`+ext, "", a.ProgID); err != nil {
					return err
				}
			}
		}
		if a.Preview {
			if err := registerThumbnails(r, a); err != nil {
				return err
			}
		}
	}
	for _, kv := range [][3]string{
		{r.capabilities(), "ApplicationName", r.Name},
		{r.capabilities(), "ApplicationDescription", description},
		{r.capabilities(), "ApplicationIcon", icon},
		{r.Software + `\RegisteredApplications`, r.Name, r.capabilities()[len(r.Software)+1:]},
		{r.application(), "FriendlyAppName", r.Name},
		{r.application() + `\shell\open\command`, "", r.command()},
	} {
		if err := setString(kv[0], kv[1], kv[2]); err != nil {
			return err
		}
	}
	notifyAssociations()
	return nil
}

// Unregister takes the associations back; once none is left, the app's declaration goes too.
func Unregister(r AssocRoot, list []Association) error {
	for _, a := range list {
		if a.Preview {
			if err := unregisterThumbnails(r, a); err != nil {
				return err
			}
		}
		for _, ext := range a.Exts {
			if err := deleteValue(r.classes()+`\`+ext+`\OpenWithProgids`, a.ProgID); err != nil {
				return err
			}
			if err := deleteValue(r.capabilities()+`\FileAssociations`, ext); err != nil {
				return err
			}
			if err := deleteValue(r.application()+`\SupportedTypes`, ext); err != nil {
				return err
			}
			if a.Own && getString(r.classes()+`\`+ext, "") == a.ProgID {
				if err := deleteValue(r.classes()+`\`+ext, ""); err != nil {
					return err
				}
			}
		}
		if err := deleteTree(r.classes() + `\` + a.ProgID); err != nil {
			return err
		}
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, r.capabilities()+`\FileAssociations`, registry.QUERY_VALUE); err == nil {
		names, _ := k.ReadValueNames(-1)
		k.Close()
		if len(names) == 0 {
			if err := deleteTree(r.Software + `\` + r.App); err != nil {
				return err
			}
			if err := deleteValue(r.Software+`\RegisteredApplications`, r.Name); err != nil {
				return err
			}
			if err := deleteTree(r.application()); err != nil {
				return err
			}
		}
	}
	notifyAssociations()
	return nil
}

// Registered reports whether the association is declared for this very program (a moved program declares nothing
// until it registers again).
func Registered(r AssocRoot, a Association) bool {
	return strings.EqualFold(getString(r.classes()+`\`+a.ProgID+`\shell\open\command`, ""), r.command()) &&
		(!a.Preview || thumbnailsRegistered(r))
}

// DefaultFor reports whether the user's choice for the extension is the ProgID, or, with no choice made, whether
// the extension names it.
func DefaultFor(r AssocRoot, ext, progID string) bool {
	choice := getString(r.Software+`\Microsoft\Windows\CurrentVersion\Explorer\FileExts\`+ext+`\UserChoice`, "ProgId")
	if choice != "" {
		return strings.EqualFold(choice, progID)
	}
	return strings.EqualFold(getString(r.classes()+`\`+ext, ""), progID)
}

var shChangeNotify = shell32.NewProc("SHChangeNotify")

// notifyAssociations tells Explorer the associations changed, so its icons and menus follow at once.
func notifyAssociations() {
	const shcneAssocChanged, shcnfIDList = 0x08000000, 0
	shChangeNotify.Call(shcneAssocChanged, shcnfIDList, 0, 0)
}

// OpenDefaultApps opens Settings at the app's page of Default apps, where the user chooses the defaults Windows
// keeps for itself.
func OpenDefaultApps(name string) error {
	return LaunchURL("ms-settings:defaultapps?registeredAppUser=" + strings.ReplaceAll(name, " ", "%20"))
}
