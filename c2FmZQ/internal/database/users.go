package database

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"

	"c2FmZQ/internal/log"
	"c2FmZQ/internal/stingle"
	"c2FmZQ/internal/stingle/token"
)

const (
	userListFile    = "users.dat"
	userFile        = "user.dat"
	contactListFile = "contact-list.dat"
)

// This is used internally for the list of all users in the system.
type userList struct {
	UserID int64  `json:"userId"`
	Email  string `json:"email"`
}

// Encapsulates all the information about a user account.
type User struct {
	// The unique user ID of the user.
	UserID int64 `json:"userId"`
	// The unique email address of the user.
	Email string `json:"email"`
	// A hash of the user's password.
	HashedPassword string `json:"hashedPassword"`
	// The salt used by the user to create the password.
	Salt string `json:"salt"`
	// The user's home folder on the app. Not used by the server.
	HomeFolder string `json:"homeFolder"`
	// The user's key bundle. It contains the user's public key, and
	// optionally, their encrypted secret key.
	KeyBundle string `json:"keyBundle"`
	// Whether KeyBundle contains the encrypted secret key.
	IsBackup string `json:"isBackup"`
	// The server's secret key used with this user.
	ServerKey stingle.SecretKey `json:"serverKey"`
	// The user's public key, extracted from the key bundle.
	PublicKey stingle.PublicKey `json:"publicKey"`
	// The server's secret key used for encrypting tokens for this user.
	TokenKey *token.Key `json:"serverTokenKey"`
}

// A user's contact list information.
type ContactList struct {
	// All the user's contacts, keyed by UserID.
	Contacts map[int64]*Contact `json:"contacts"`
	// All users who have this user in their contact list.
	In map[int64]bool `json:"in"`
	// Delete events for contacts.
	Deletes []DeleteEvent `json:"deletes"`
}

// Encapsulates the information about a user's contact (another user).
type Contact struct {
	// The contact's UserID.
	UserID int64 `json:"userId"`
	// The contact's email address.
	Email string `json:"email"`
	// The contact's public key.
	PublicKey string `json:"publicKey"`
	// ?
	DateUsed int64 `json:"dateUsed,omitempty"`
	// The time when the contact was added or modified.
	DateModified int64 `json:"dateModified,omitempty"`
}

// ServerPublicKeyForExport returns the server's public key associated with this
// user.
func (u User) ServerPublicKeyForExport() string {
	defer recordLatency("ServerPublicKeyForExport")()

	return base64.StdEncoding.EncodeToString(u.ServerKey.PublicKey().ToBytes())
}

func (u User) home(elems ...string) string {
	return homeByUserID(u.UserID, elems...)
}

func homeByUserID(userID int64, elems ...string) string {
	e := []string{"home", fmt.Sprintf("%d", userID)}
	e = append(e, elems...)
	return filepath.Join(e...)
}

// AddUser creates a new user account for u.
func (d *Database) AddUser(u User) (retErr error) {
	defer recordLatency("AddUser")()

	var ul []userList
	commit, err := d.storage.OpenForUpdate(d.filePath(userListFile), &ul)
	if err != nil {
		log.Errorf("d.storage.OpenForUpdate: %v", err)
		return err
	}
	defer commit(true, &retErr)
	uids := make(map[int64]bool)
	for _, i := range ul {
		if i.Email == u.Email {
			return os.ErrExist
		}
		uids[i.UserID] = true
	}

	var uid int64
	for {
		bi, err := rand.Int(rand.Reader, big.NewInt(int64(math.MaxInt32-1000000)))
		if err != nil {
			commit(false, nil)
			return err
		}
		if uid = bi.Int64() + 1000000; !uids[uid] {
			break
		}
	}
	ul = append(ul, userList{UserID: uid, Email: u.Email})

	u.UserID = uid
	u.HomeFolder = hex.EncodeToString(d.Hash([]byte(u.Email)))
	u.ServerKey = stingle.MakeSecretKey()
	u.TokenKey = token.MakeKey()
	if err := d.storage.SaveDataFile(d.filePath(u.home(userFile)), u); err != nil {
		return err
	}

	if err := d.storage.CreateEmptyFile(d.fileSetPath(u, stingle.TrashSet), FileSet{}); err != nil {
		return err
	}
	if err := d.storage.CreateEmptyFile(d.fileSetPath(u, stingle.GallerySet), FileSet{}); err != nil {
		return err
	}
	if err := d.storage.CreateEmptyFile(d.filePath(u.home(albumManifest)), AlbumManifest{}); err != nil {
		return err
	}
	if err := d.storage.CreateEmptyFile(d.filePath(u.home(contactListFile)), ContactList{}); err != nil {
		return err
	}
	return nil
}

// UpdateUser adds or updates a user object.
func (d *Database) UpdateUser(u User) error {
	defer recordLatency("UpdateUser")()

	var f User
	commit, err := d.storage.OpenForUpdate(d.filePath(u.home(userFile)), &f)
	if err != nil {
		return err
	}
	f = u
	return commit(true, nil)
}

// UserByID returns the User object with the given ID.
func (d *Database) UserByID(id int64) (User, error) {
	defer recordLatency("UserByID")()

	var u User
	err := d.storage.ReadDataFile(d.filePath("home", fmt.Sprintf("%d", id), userFile), &u)
	return u, err
}

// User returns the User object with the given email address.
func (d *Database) User(email string) (User, error) {
	defer recordLatency("User")()

	var ul []userList
	if err := d.storage.ReadDataFile(d.filePath(userListFile), &ul); err != nil {
		return User{}, err
	}
	for _, u := range ul {
		if u.Email == email {
			return d.UserByID(u.UserID)
		}
	}
	return User{}, os.ErrNotExist
}

// TokenKeyForUser returns the server's TokenKey associated with this user.
func (d *Database) TokenKeyForUser(email string) *token.Key {
	defer recordLatency("TokenKeyForUser")()

	if u, err := d.User(email); err == nil {
		return u.TokenKey
	}
	return nil
}

// DeleteUser deletes a user object and all resources attached to it.
func (d *Database) DeleteUser(u User) error {
	defer recordLatency("DeleteUser")()

	var ul []userList
	commit, err := d.storage.OpenForUpdate(d.filePath(userListFile), &ul)
	if err != nil {
		log.Errorf("d.storage.OpenForUpdate: %v", err)
		return err
	}
	for i := range ul {
		if ul[i].UserID == u.UserID {
			ul[i] = ul[len(ul)-1]
			ul = ul[:len(ul)-1]
			break
		}
	}
	if err := commit(true, nil); err != nil {
		return err
	}
	if err := d.removeAllContacts(u); err != nil {
		return err
	}

	albumRefs, err := d.AlbumRefs(u)
	if err != nil {
		return err
	}

	for albumID := range albumRefs {
		album, err := d.Album(u, albumID)
		if err != nil {
			return err
		}
		if album.OwnerID == u.UserID {
			if err := d.DeleteAlbum(u, albumID); err != nil {
				return err
			}
			continue
		}
		if err := d.RemoveAlbumMember(u, albumID, u.UserID); err != nil {
			return err
		}
	}
	commit, filesets, err := d.fileSetsForUpdate(u, []string{stingle.GallerySet, stingle.TrashSet}, []string{"", ""})
	if err != nil {
		return err
	}
	for _, fs := range filesets {
		for _, f := range fs.Files {
			d.incRefCount(f.StoreFile, -1)
			d.incRefCount(f.StoreThumb, -1)
		}
	}
	if err := commit(true, nil); err != nil {
		return err
	}
	for _, f := range []string{
		d.filePath(u.home(userFile)),
		d.fileSetPath(u, stingle.TrashSet),
		d.fileSetPath(u, stingle.GallerySet),
		d.filePath(u.home(albumManifest)),
		d.filePath(u.home(contactListFile)),
	} {
		if err := os.Remove(filepath.Join(d.Dir(), f)); err != nil {
			return err
		}
	}
	return nil
}

// Export converts a Contact to stingle.Contact.
func (c Contact) Export() stingle.Contact {
	return stingle.Contact{
		UserID:       number(c.UserID),
		Email:        c.Email,
		PublicKey:    c.PublicKey,
		DateUsed:     number(c.DateUsed),
		DateModified: number(c.DateModified),
	}
}

// addContactToUser adds contact to user's contact list.
func (d *Database) addContactToUser(user, contact User) (c *Contact, retErr error) {
	var (
		userContacts    ContactList
		contactContacts ContactList
	)
	files := []string{
		d.filePath(user.home(contactListFile)),
		d.filePath(contact.home(contactListFile)),
	}
	contactLists := []*ContactList{
		&userContacts,
		&contactContacts,
	}
	commit, err := d.storage.OpenManyForUpdate(files, contactLists)
	if err != nil {
		log.Errorf("d.storage.OpenManyForUpdate: %v", err)
		return nil, err
	}
	defer commit(true, &retErr)

	if userContacts.Contacts == nil {
		userContacts.Contacts = make(map[int64]*Contact)
	}
	userContacts.Contacts[contact.UserID] = &Contact{
		UserID:       contact.UserID,
		Email:        contact.Email,
		PublicKey:    base64.StdEncoding.EncodeToString(contact.PublicKey.ToBytes()),
		DateModified: nowInMS(),
	}
	if contactContacts.In == nil {
		contactContacts.In = make(map[int64]bool)
	}
	contactContacts.In[user.UserID] = true

	return userContacts.Contacts[contact.UserID], nil
}

// AddContact adds the user with the given email address to user's contact list.
func (d *Database) AddContact(user User, contactEmail string) (*Contact, error) {
	defer recordLatency("AddContact")()

	c, err := d.User(contactEmail)
	if err != nil {
		return nil, err
	}
	return d.addContactToUser(user, c)
}

// removeAllContacts removes all contacts.
func (d *Database) removeAllContacts(user User) (retErr error) {
	var contacts ContactList
	if err := d.storage.ReadDataFile(d.filePath(user.home(contactListFile)), &contacts); err != nil {
		return err
	}
	uids := make(map[int64]struct{})
	uids[user.UserID] = struct{}{}
	for uid := range contacts.Contacts {
		uids[uid] = struct{}{}
	}
	for uid := range contacts.In {
		uids[uid] = struct{}{}
	}
	uidSlice := make([]int64, 0, len(uids))
	fileSlice := make([]string, 0, len(uids))
	contactListSlice := make([]*ContactList, 0, len(uids))
	for uid := range uids {
		uidSlice = append(uidSlice, uid)
		fileSlice = append(fileSlice, d.filePath(homeByUserID(uid, contactListFile)))
		contactListSlice = append(contactListSlice, new(ContactList))
	}
	commit, err := d.storage.OpenManyForUpdate(fileSlice, contactListSlice)
	if err != nil {
		log.Errorf("d.storage.OpenManyForUpdate: %v", err)
		return err
	}
	defer commit(false, &retErr)
	uc := make(map[int64]*ContactList)
	for i, uid := range uidSlice {
		uc[uid] = contactListSlice[i]
	}
	for uid, cl := range uc {
		// Remove user from contact's list.
		delete(uc[user.UserID].In, uid)
		delete(cl.Contacts, user.UserID)
		cl.Deletes = append(cl.Deletes, DeleteEvent{
			File: fmt.Sprintf("%d", user.UserID),
			Date: nowInMS(),
			Type: stingle.DeleteEventContact,
		})
		// Remove contact from user's list.
		delete(uc[uid].In, user.UserID)
		delete(uc[uid].Contacts, uid)
		uc[user.UserID].Deletes = append(uc[user.UserID].Deletes, DeleteEvent{
			File: fmt.Sprintf("%d", uid),
			Date: nowInMS(),
			Type: stingle.DeleteEventContact,
		})
	}
	return commit(true, nil)
}

// lookupContacts returns a Contact for each UserIDs in the list.
func (d *Database) lookupContacts(uids map[int64]bool) []Contact {
	var ul []userList
	if err := d.storage.ReadDataFile(d.filePath(userListFile), &ul); err != nil {
		return nil
	}
	var out []Contact
	for _, u := range ul {
		if uids[u.UserID] {
			user, err := d.UserByID(u.UserID)
			if err != nil {
				log.Errorf("d.UserByID(%q) failed, but user in %q: %v", u.UserID, userListFile, err)
				continue
			}
			out = append(out, Contact{
				UserID:    user.UserID,
				Email:     user.Email,
				PublicKey: base64.StdEncoding.EncodeToString(user.PublicKey.ToBytes()),
			})
		}
	}
	return out
}

// addCrossContacts adds contacts to each other.
func (d *Database) addCrossContacts(list []Contact) {
	files := make([]string, len(list))
	contactLists := make([]*ContactList, len(list))
	for i, c := range list {
		files[i] = d.filePath("home", fmt.Sprintf("%d", c.UserID), contactListFile)
		contactLists[i] = &ContactList{}
	}
	commit, err := d.storage.OpenManyForUpdate(files, contactLists)
	if err != nil {
		log.Errorf("d.storage.OpenManyForUpdate: %v", err)
		return
	}
	count := 0
	for i, c1 := range list {
		contactList := contactLists[i]
		if contactList.Contacts == nil {
			contactList.Contacts = make(map[int64]*Contact)
		}
		if contactList.In == nil {
			contactList.In = make(map[int64]bool)
		}
		for _, c2 := range list {
			if c1.UserID == c2.UserID {
				continue
			}
			if contactList.Contacts[c2.UserID] == nil {
				count++
				c := c2
				c.DateModified = nowInMS()
				contactList.Contacts[c2.UserID] = &c
			}
			contactList.In[c2.UserID] = true
		}
	}
	if err := commit(true, nil); err != nil {
		log.Errorf("Failed to save user contact lists: %v", err)
	} else {
		log.Debugf("Added %d contact(s) to %d user(s)", count, len(list))
	}
}

// ContactUpdates returns changes to a user's contact list that are more recent
// than ts.
func (d *Database) ContactUpdates(user User, ts int64) ([]stingle.Contact, error) {
	defer recordLatency("ContactUpdates")()

	var contactList ContactList
	if err := d.storage.ReadDataFile(d.filePath(user.home(contactListFile)), &contactList); err != nil {
		return nil, err
	}
	if contactList.Contacts == nil {
		contactList.Contacts = make(map[int64]*Contact)
	}
	out := []stingle.Contact{}
	for _, v := range contactList.Contacts {
		if v.DateModified > ts {
			sc := stingle.Contact{
				UserID:       number(v.UserID),
				Email:        v.Email,
				PublicKey:    v.PublicKey,
				DateModified: number(v.DateModified),
			}

			out = append(out, sc)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DateModified == out[j].DateModified {
			return out[i].Email < out[j].Email
		}
		return out[i].DateModified < out[j].DateModified
	})
	return out, nil
}
