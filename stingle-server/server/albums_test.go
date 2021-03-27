package server_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"stingle-server/database"
	"stingle-server/stingle"
)

func createAccountsAndLogin(sock string) (*client, *client, *client, error) {
	alice, err := createAccountAndLogin(sock, "alice")
	if err != nil {
	}
	bob, err := createAccountAndLogin(sock, "bob")
	if err != nil {
		return nil, nil, nil, err
	}
	carol, err := createAccountAndLogin(sock, "carol")
	if err != nil {
		return nil, nil, nil, err
	}
	return alice, bob, carol, nil
}

func TestAddDeleteAlbum(t *testing.T) {
	sock, shutdown := startServer(t)
	defer shutdown()

	database.CurrentTimeForTesting = 1000

	c, err := createAccountAndLogin(sock, "alice")
	if err != nil {
		t.Fatalf("createAccountAndLogin failed: %v", err)
	}
	if err := c.addAlbum("album1", 1000); err != nil {
		t.Fatalf("c.addAlbum failed: %v", err)
	}

	got, err := c.getUpdates(0, 0, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("c.getUpdates failed: %v", err)
	}
	want := stingle.ResponseOK().
		AddPartList("albums", map[string]interface{}{
			"albumId":       "album1",
			"cover":         "",
			"dateCreated":   "1000",
			"dateModified":  "1000",
			"encPrivateKey": "album1 encPrivateKey",
			"isHidden":      "0",
			"isLocked":      "0",
			"isOwner":       "1",
			"isShared":      "0",
			"members":       "",
			"metadata":      "album1 metadata",
			"permissions":   "",
			"publicKey":     "album1 publicKey",
		})
	if diff := diffUpdates(want, got); diff != "" {
		t.Errorf("Unexpected updates:\n%v", diff)
	}

	database.CurrentTimeForTesting = 2000

	if err := c.deleteAlbum("album1"); err != nil {
		t.Fatalf("c.deleteAlbum failed: %v", err)
	}

	if got, err = c.getUpdates(0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("c.getUpdates failed: %v", err)
	}
	want = stingle.ResponseOK().
		AddPartList("deletes", map[string]interface{}{
			"albumId": "album1", "date": "2000", "file": "", "type": "4",
		})
	if diff := diffUpdates(want, got); diff != "" {
		t.Errorf("Unexpected updates:\n%v", diff)
	}
}

func TestShareAlbum(t *testing.T) {
	sock, shutdown := startServer(t)
	defer shutdown()

	database.CurrentTimeForTesting = 1000

	alice, bob, carol, err := createAccountsAndLogin(sock)
	if err != nil {
		t.Fatalf("createAccountsAndLogin failed: %v", err)
	}
	if err := alice.addAlbum("album", 1000); err != nil {
		t.Fatalf("alice.addAlbum failed: %v", err)
	}

	database.CurrentTimeForTesting = 2000

	if err := alice.shareAlbum(stingle.Album{
		AlbumID:     "album",
		Permissions: "1111",
		Members:     fmt.Sprintf("%d,%d", alice.userID, bob.userID),
		SharingKeys: map[string]string{
			fmt.Sprintf("%d", bob.userID): "Bob's Sharing Key",
		},
	}); err != nil {
		t.Fatalf("alice.shareAlbum failed: %v", err)
	}

	got, err := alice.getUpdates(0, 0, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("alice.getUpdates failed: %v", err)
	}
	want := stingle.ResponseOK().
		AddPartList("albums", map[string]interface{}{
			"albumId":       "album",
			"cover":         "",
			"dateCreated":   "1000",
			"dateModified":  "2000",
			"encPrivateKey": "album encPrivateKey",
			"isHidden":      "0",
			"isLocked":      "0",
			"isOwner":       "1",
			"isShared":      "1",
			"members":       "1,2",
			"metadata":      "album metadata",
			"permissions":   "1111",
			"publicKey":     "album publicKey",
		}).
		AddPartList("contacts", map[string]interface{}{
			"dateModified": "2000", "email": "bob", "publicKey": base64.StdEncoding.EncodeToString(bob.secretKey.PublicKey().Bytes), "userId": fmt.Sprintf("%d", bob.userID),
		})

	if diff := diffUpdates(want, got); diff != "" {
		t.Errorf("Unexpected updates:\n%v", diff)
	}

	if got, err = bob.getUpdates(0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("bob.getUpdates failed: %v", err)
	}
	want = stingle.ResponseOK().
		AddPartList("albums", map[string]interface{}{
			"albumId":       "album",
			"cover":         "",
			"dateCreated":   "1000",
			"dateModified":  "2000",
			"encPrivateKey": "Bob's Sharing Key",
			"isHidden":      "0",
			"isLocked":      "0",
			"isOwner":       "0",
			"isShared":      "1",
			"members":       "1,2",
			"metadata":      "album metadata",
			"permissions":   "1111",
			"publicKey":     "album publicKey",
		}).
		AddPartList("contacts", map[string]interface{}{
			"dateModified": "2000", "email": "alice", "publicKey": base64.StdEncoding.EncodeToString(alice.secretKey.PublicKey().Bytes), "userId": fmt.Sprintf("%d", alice.userID),
		})

	if diff := diffUpdates(want, got); diff != "" {
		t.Errorf("Unexpected updates:\n%v", diff)
	}

	database.CurrentTimeForTesting = 3000

	if err := bob.shareAlbum(stingle.Album{
		AlbumID: "album",
		Members: fmt.Sprintf("%d", carol.userID),
		SharingKeys: map[string]string{
			fmt.Sprintf("%d", carol.userID): "Carol's Sharing Key",
		},
	}); err != nil {
		t.Fatalf("bob.shareAlbum failed: %v", err)
	}

	if got, err = carol.getUpdates(0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("carol.getUpdates failed: %v", err)
	}
	want = stingle.ResponseOK().
		AddPartList("albums", map[string]interface{}{
			"albumId":       "album",
			"cover":         "",
			"dateCreated":   "1000",
			"dateModified":  "3000",
			"encPrivateKey": "Carol's Sharing Key",
			"isHidden":      "0",
			"isLocked":      "0",
			"isOwner":       "0",
			"isShared":      "1",
			"members":       "1,2,3",
			"metadata":      "album metadata",
			"permissions":   "1111",
			"publicKey":     "album publicKey",
		}).
		AddPartList("contacts",
			map[string]interface{}{
				"dateModified": "3000", "email": "alice", "publicKey": base64.StdEncoding.EncodeToString(alice.secretKey.PublicKey().Bytes), "userId": fmt.Sprintf("%d", alice.userID),
			},
			map[string]interface{}{
				"dateModified": "3000", "email": "bob", "publicKey": base64.StdEncoding.EncodeToString(bob.secretKey.PublicKey().Bytes), "userId": fmt.Sprintf("%d", bob.userID),
			})

	if diff := diffUpdates(want, got); diff != "" {
		t.Errorf("Unexpected updates:\n%v", diff)
	}
}

func TestAlbumEdits(t *testing.T) {
	sock, shutdown := startServer(t)
	defer shutdown()

	alice, bob, carol, err := createAccountsAndLogin(sock)
	if err != nil {
		t.Fatalf("createAccountsAndLogin failed: %v", err)
	}
	database.CurrentTimeForTesting = 1000
	if err := alice.addAlbum("album", 1000); err != nil {
		t.Errorf("alice.addAlbum failed: %v", err)
	}
	if err := alice.shareAlbum(stingle.Album{
		AlbumID:     "album",
		Permissions: "1111",
		Members:     fmt.Sprintf("%d,%d,%d", alice.userID, bob.userID, carol.userID),
		SharingKeys: map[string]string{
			fmt.Sprintf("%d", bob.userID):   "Bob's Sharing Key",
			fmt.Sprintf("%d", carol.userID): "Carol's Sharing Key",
		},
	}); err != nil {
		t.Fatalf("alice.shareAlbum failed: %v", err)
	}
	database.CurrentTimeForTesting = 2000
	if err := alice.changeAlbumCover("album", "new-cover"); err != nil {
		t.Errorf("alice.changeAlbumCover failed: %v", err)
	}
	database.CurrentTimeForTesting = 3000
	if err := alice.renameAlbum("album", "new-metadata"); err != nil {
		t.Errorf("alice.renameAlbum failed: %v", err)
	}
	database.CurrentTimeForTesting = 4000
	if err := alice.editPerms(stingle.Album{AlbumID: "album", Permissions: "1101"}); err != nil {
		t.Errorf("alice.editPerms failed: %v", err)
	}

	got, err := alice.getUpdates(0, 0, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("alice.getUpdates failed: %v", err)
	}
	want := stingle.ResponseOK().
		AddPartList("albums", map[string]interface{}{
			"albumId":       "album",
			"cover":         "new-cover",
			"dateCreated":   "1000",
			"dateModified":  "4000",
			"encPrivateKey": "album encPrivateKey",
			"isHidden":      "0",
			"isLocked":      "0",
			"isOwner":       "1",
			"isShared":      "1",
			"members":       "1,2,3",
			"metadata":      "new-metadata",
			"permissions":   "1101",
			"publicKey":     "album publicKey",
		}).
		AddPartList("contacts", map[string]interface{}{
			"dateModified": "1000", "email": "bob", "publicKey": base64.StdEncoding.EncodeToString(bob.secretKey.PublicKey().Bytes), "userId": fmt.Sprintf("%d", bob.userID),
		}, map[string]interface{}{
			"dateModified": "1000", "email": "carol", "publicKey": base64.StdEncoding.EncodeToString(carol.secretKey.PublicKey().Bytes), "userId": fmt.Sprintf("%d", carol.userID),
		})

	if diff := diffUpdates(want, got); diff != "" {
		t.Errorf("Unexpected updates:\n%v", diff)
	}

	database.CurrentTimeForTesting = 5000
	if err := alice.removeAlbumMember(stingle.Album{AlbumID: "album"}, bob.userID); err != nil {
		t.Errorf("alice.removeAlbumMember failed: %v", err)
	}
	if err := carol.leaveAlbum("album"); err != nil {
		t.Errorf("catol.leaveAlbum failed: %v", err)
	}

	if got, err = alice.getUpdates(4000, 4000, 4000, 4000, 4000, 4000); err != nil {
		t.Fatalf("alice.getUpdates failed: %v", err)
	}
	want = stingle.ResponseOK().
		AddPartList("albums", map[string]interface{}{
			"albumId":       "album",
			"cover":         "new-cover",
			"dateCreated":   "1000",
			"dateModified":  "5000",
			"encPrivateKey": "album encPrivateKey",
			"isHidden":      "0",
			"isLocked":      "0",
			"isOwner":       "1",
			"isShared":      "1",
			"members":       "1",
			"metadata":      "new-metadata",
			"permissions":   "1101",
			"publicKey":     "album publicKey",
		})

	if diff := diffUpdates(want, got); diff != "" {
		t.Errorf("Unexpected updates:\n%v", diff)
	}
}

func TestUnshareAlbumEdits(t *testing.T) {
	sock, shutdown := startServer(t)
	defer shutdown()

	alice, bob, carol, err := createAccountsAndLogin(sock)
	if err != nil {
		t.Fatalf("createAccountsAndLogin failed: %v", err)
	}
	database.CurrentTimeForTesting = 1000
	if err := alice.addAlbum("album", 1000); err != nil {
		t.Errorf("alice.addAlbum failed: %v", err)
	}
	if err := alice.shareAlbum(stingle.Album{
		AlbumID:     "album",
		Permissions: "1111",
		Members:     fmt.Sprintf("%d,%d,%d", alice.userID, bob.userID, carol.userID),
		SharingKeys: map[string]string{
			fmt.Sprintf("%d", bob.userID):   "Bob's Sharing Key",
			fmt.Sprintf("%d", carol.userID): "Carol's Sharing Key",
		},
	}); err != nil {
		t.Fatalf("alice.shareAlbum failed: %v", err)
	}
	database.CurrentTimeForTesting = 2000
	if err := alice.unshareAlbum("album"); err != nil {
		t.Errorf("alice.unshareAlbum failed: %v", err)
	}
	got, err := alice.getUpdates(1000, 1000, 1000, 1000, 1000, 1000)
	if err != nil {
		t.Fatalf("alice.getUpdates failed: %v", err)
	}
	want := stingle.ResponseOK().
		AddPartList("albums", map[string]interface{}{
			"albumId":       "album",
			"cover":         "",
			"dateCreated":   "1000",
			"dateModified":  "2000",
			"encPrivateKey": "album encPrivateKey",
			"isHidden":      "0",
			"isLocked":      "0",
			"isOwner":       "1",
			"isShared":      "0",
			"members":       "",
			"metadata":      "album metadata",
			"permissions":   "1111",
			"publicKey":     "album publicKey",
		})
	if diff := diffUpdates(want, got); diff != "" {
		t.Errorf("Unexpected updates:\n%v", diff)
	}

	if got, err = bob.getUpdates(1000, 1000, 1000, 1000, 1000, 1000); err != nil {
		t.Fatalf("bob.getUpdates failed: %v", err)
	}
	want = stingle.ResponseOK().
		AddPartList("deletes", map[string]interface{}{"albumId": "album", "date": "2000", "file": "", "type": "4"})
	if diff := diffUpdates(want, got); diff != "" {
		t.Errorf("Unexpected updates:\n%v", diff)
	}

	if got, err = carol.getUpdates(1000, 1000, 1000, 1000, 1000, 1000); err != nil {
		t.Fatalf("carol.getUpdates failed: %v", err)
	}
	want = stingle.ResponseOK().
		AddPartList("deletes", map[string]interface{}{"albumId": "album", "date": "2000", "file": "", "type": "4"})
	if diff := diffUpdates(want, got); diff != "" {
		t.Errorf("Unexpected updates:\n%v", diff)
	}
}

func (c *client) addAlbum(albumID string, ts int64) error {
	params := make(map[string]string)
	params["albumId"] = albumID
	params["dateCreated"] = fmt.Sprintf("%d", ts)
	params["dateModified"] = fmt.Sprintf("%d", ts)
	params["encPrivateKey"] = albumID + " encPrivateKey"
	params["metadata"] = albumID + " metadata"
	params["publicKey"] = albumID + " publicKey"

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("params", c.encodeParams(params))

	sr, err := c.sendRequest("/v2/sync/addAlbum", form)
	if err != nil {
		return err
	}
	if sr.Status != "ok" {
		return fmt.Errorf("status:nok %+v", sr)
	}
	return nil
}

func (c *client) deleteAlbum(albumID string) error {
	params := make(map[string]string)
	params["albumId"] = albumID

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("params", c.encodeParams(params))

	sr, err := c.sendRequest("/v2/sync/deleteAlbum", form)
	if err != nil {
		return err
	}
	if sr.Status != "ok" {
		return fmt.Errorf("status:nok %+v", sr)
	}
	return nil
}

func (c *client) shareAlbum(album stingle.Album) error {
	ja, err := json.Marshal(album)
	if err != nil {
		return err
	}
	jk, err := json.Marshal(album.SharingKeys)
	if err != nil {
		return err
	}
	params := make(map[string]string)
	params["album"] = string(ja)
	params["sharingKeys"] = string(jk)

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("params", c.encodeParams(params))

	sr, err := c.sendRequest("/v2/sync/share", form)
	if err != nil {
		return err
	}
	if sr.Status != "ok" {
		return fmt.Errorf("status:nok %+v", sr)
	}
	return nil
}

func (c *client) changeAlbumCover(albumID, cover string) error {
	params := make(map[string]string)
	params["albumId"] = albumID
	params["cover"] = cover

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("params", c.encodeParams(params))

	sr, err := c.sendRequest("/v2/sync/changeAlbumCover", form)
	if err != nil {
		return err
	}
	if sr.Status != "ok" {
		return fmt.Errorf("status:nok %+v", sr)
	}
	return nil
}

func (c *client) renameAlbum(albumID, metadata string) error {
	params := make(map[string]string)
	params["albumId"] = albumID
	params["metadata"] = metadata

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("params", c.encodeParams(params))

	sr, err := c.sendRequest("/v2/sync/renameAlbum", form)
	if err != nil {
		return err
	}
	if sr.Status != "ok" {
		return fmt.Errorf("status:nok %+v", sr)
	}
	return nil
}

func (c *client) editPerms(album stingle.Album) error {
	ja, err := json.Marshal(album)
	if err != nil {
		return err
	}
	params := make(map[string]string)
	params["album"] = string(ja)

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("params", c.encodeParams(params))

	sr, err := c.sendRequest("/v2/sync/editPerms", form)
	if err != nil {
		return err
	}
	if sr.Status != "ok" {
		return fmt.Errorf("status:nok %+v", sr)
	}
	return nil
}

func (c *client) removeAlbumMember(album stingle.Album, memberUserID int64) error {
	ja, err := json.Marshal(album)
	if err != nil {
		return err
	}
	params := make(map[string]string)
	params["album"] = string(ja)
	params["memberUserId"] = fmt.Sprintf("%d", memberUserID)

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("params", c.encodeParams(params))

	sr, err := c.sendRequest("/v2/sync/removeAlbumMember", form)
	if err != nil {
		return err
	}
	if sr.Status != "ok" {
		return fmt.Errorf("status:nok %+v", sr)
	}
	return nil
}

func (c *client) leaveAlbum(albumID string) error {
	params := make(map[string]string)
	params["albumId"] = albumID

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("params", c.encodeParams(params))

	sr, err := c.sendRequest("/v2/sync/leaveAlbum", form)
	if err != nil {
		return err
	}
	if sr.Status != "ok" {
		return fmt.Errorf("status:nok %+v", sr)
	}
	return nil
}

func (c *client) unshareAlbum(albumID string) error {
	params := make(map[string]string)
	params["albumId"] = albumID

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("params", c.encodeParams(params))

	sr, err := c.sendRequest("/v2/sync/unshareAlbum", form)
	if err != nil {
		return err
	}
	if sr.Status != "ok" {
		return fmt.Errorf("status:nok %+v", sr)
	}
	return nil
}
