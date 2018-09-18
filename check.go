package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/boltdb/bolt"
	"github.com/robfig/cron"
)

// Helper struct for serialization.
type Check struct {
	ID            uint64    `json:"id"`
	UserID        uint64    `json:"user_id"`
	URL           string    `json:"url"`
	Selector      string    `json:"selector"`
	Schedule      string    `json:"schedule"`
	LastChecked   time.Time `json:"last_checked"`
	LastChanged   time.Time `json:"last_changed"`
	LastHash      string    `json:"last_hash"`
	SeenChange    bool      `json:"seen"`
	NotifyPresent bool      `json:"is_present"`
	IsEnabled     bool      `json:"is_enabled"`
	Content       string    `json:"content"`
	Title         string    `json:"title"`

	// The last-checked date, as a string.
	LastCheckedPretty string `json:"-"`
	LastChangedPretty string `json:"-"`
	IsEnabledPretty   string `json:"-"`

	// The first 8 characters of the hash
	ShortHash string `json:"-"`

	ShortURL string `json:"-"`
}

// Helper struct for serialization.
type User struct {
	ID          uint64    `json:"id"`
	UserID      int64     `json:"user_id"`
	LastChanged time.Time `json:"last_changed"`
	IsEnabled   bool      `json:"is_enabled"`

	// TODO: The last-checked date, as a string.
	LastChangedPretty string `json:"-"`
}

func KeyFor(id interface{}) (key []byte) {
	key = make([]byte, 8)

	switch v := id.(type) {
	case int:
		binary.LittleEndian.PutUint64(key, uint64(v))
	case int64:
		binary.LittleEndian.PutUint64(key, uint64(v))
	case uint:
		binary.LittleEndian.PutUint64(key, uint64(v))
	case uint64:
		binary.LittleEndian.PutUint64(key, v)
	default:
		panic("unknown id type")
	}
	return
}

func (c *Check) PrepareForDisplay() {
	if c.LastChecked.IsZero() {
		c.LastCheckedPretty = "never"
		c.LastChangedPretty = "never"
	} else {
		c.LastCheckedPretty = c.LastChecked.Format(
			"Jan 2, 2006 at 3:04pm (MST)")

		c.LastChangedPretty = c.LastChanged.Format(
			"Jan 2, 2006 at 3:04pm (MST)")
	}

	if len(c.LastHash) > 0 {
		c.ShortHash = c.LastHash[0:8]
	} else {
		c.ShortHash = "none"
	}

	if c.IsEnabled {
		c.IsEnabledPretty = "enabled"
	} else {
		c.IsEnabledPretty = "disabled"
	}

	if len(c.URL) > 0 {
		u, err := url.Parse(c.URL)
		if err == nil {
			c.ShortURL = fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, u.Path)
		}
	}
}

func GetAllChecks(db *bolt.DB, output *[]*Check) error {
	return db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(UrlsBucket)
		b.ForEach(func(k, v []byte) error {
			check := &Check{}
			if err := json.Unmarshal(v, check); err != nil {
				println("error unmarshaling json", err)
				return nil
			}

			check.ID = binary.LittleEndian.Uint64(k)
			check.PrepareForDisplay()

			*output = append(*output, check)
			return nil
		})
		return nil
	})
}

func GetMyChecks(db *bolt.DB, requester int64, output *[]*Check) error {
	return db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(UrlsBucket)
		b.ForEach(func(k, v []byte) error {
			check := &Check{}
			if err := json.Unmarshal(v, check); err != nil {
				println("error unmarshaling json", err)
				return nil
			}

			if check.UserID == uint64(requester) {
				check.ID = binary.LittleEndian.Uint64(k)
				check.PrepareForDisplay()

				*output = append(*output, check)
			}
			return nil
		})
		return nil
	})
}

func (c *Check) Update(db *bolt.DB) {
	// println("Requesting page id", c.ID, "last checked", c.LastCheckedPretty, "last changed", c.LastChangedPretty, "must", (c.NotifyPresent), "contain", c.Selector, c.ShortHash)

	timeout := time.Duration(10 * time.Second)
	client := http.Client{
		Timeout: timeout,
	}

	resp, err := client.Get(c.URL)
	if err != nil {
		println("error fetching check", c.ID, c.URL, err)
		return
	}

	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		println("error status check", c.ID, c.URL, resp.StatusCode)
		return
	}

	test, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		println("error", err.Error())
		return
		// os.Exit(1)
	}

	text := string(test) //Short(string(test), 81920)
	c.Content = text

	println("old size", len(string(test)), "new size", len(text))

	// println(text)

	// println(string(test))

	// TODO: replace with str search
	// test := resp. // sel.Text(
	// doc, err := goquery.NewDocumentFromResponse(resp)
	// if err != nil {
	// 	println("error parsing check", c.ID, err)
	// 	return
	// }

	// // Get all nodes matching the given selector
	// sel := doc.Find(c.Selector)
	// if sel.Length() == 0 {
	// 	println("error in check: no nodes in selection", c.ID, c.Selector)
	// 	return
	// }

	// Hash the content
	hash := sha256.New()
	io.WriteString(hash, string(text))
	sum := hex.EncodeToString(hash.Sum(nil))

	// Check for update
	if c.LastHash != sum {
		contains := strings.Contains(string(text), c.Selector)

		if c.NotifyPresent && !contains {
			telegramChan <- telegramResponse{fmt.Sprintf("/%d <b>%s</b> <i>NOT found</i>", c.ID, c.Title), int64(c.UserID)}
		} else if !c.NotifyPresent && contains {
			telegramChan <- telegramResponse{fmt.Sprintf("/%d <b>%s</b> <i>found</i>", c.ID, c.Title), int64(c.UserID)}
		}

		c.LastHash = sum
		c.SeenChange = true
		c.LastChanged = time.Now()
	} else {
		c.SeenChange = false
		// println("document not changed", c.ID, c.LastHash, c.SeenChange)
	}

	c.LastChecked = time.Now()

	// println("result", c.SeenChange)

	// Need to update the database now, since we've changed (at least the last
	// checked time).
	err = db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(c)
		if err != nil {
			return err
		}

		if err = tx.Bucket(UrlsBucket).Put(KeyFor(c.ID), data); err != nil {
			return err
		}
		return nil
	})
}

func (c *Check) New(db *bolt.DB, cron *cron.Cron, url string, search string, contains string, userID int64) (result string) {
	println("adding new check", url, search)

	if len(url) == 0 {
		return "missing URL parameter"
	}
	if len(search) == 0 {
		return "missing search parameter"
	}

	if len(contains) == 0 {
		return "missing contains parameter"
	}

	if userID <= 0 {
		return "missing userid parameter"
	}

	check := Check{
		URL:       url,
		Selector:  search,
		Schedule:  "0 * * * * *",
		UserID:    uint64(userID),
		IsEnabled: true,
	}

	check.NotifyPresent = contains == "true"

	err := db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(check)
		if err != nil {
			return err
		}

		b := tx.Bucket(UrlsBucket)

		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		check.ID = uint64(seq)

		if err = b.Put(KeyFor(seq), data); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return "error inserting new item"
	}

	// If we succeeded, we update right now...
	check.Update(db)

	// ... and add a new Cron callback
	cr := cron
	cr.AddFunc(check.Schedule, func() {
		TryUpdate(db, check.ID)
	})
	return fmt.Sprintf("/%d added", check.ID)
}

func (c *Check) Delete(db *bolt.DB, requester int64, findID string) (result bool) {
	id, err := strconv.ParseUint(findID, 10, 64)
	if err != nil {
		println(err.Error(), http.StatusBadRequest)
		return false
	}

	check := &Check{}
	err = db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(UrlsBucket).Get(KeyFor(id))
		if data == nil {
			return fmt.Errorf("no such check: %d", id)
		}
		if err := json.Unmarshal(data, check); err != nil {
			println("error unmarshaling json", err)
			return err
		}

		check.ID = id
		return nil
	})

	if err != nil {
		println(err.Error(), http.StatusInternalServerError)
		return false
	}

	if requester != int64(check.UserID) {
		return //"Not your check"
	}

	err = db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(UrlsBucket).Delete(KeyFor(id))
	})
	if err != nil {
		println(err.Error(), http.StatusInternalServerError)
		return false
	}
	return true
}

func (c *Check) Info(db *bolt.DB, requester int64, findID string) (result string) {
	id, err := strconv.ParseUint(findID, 10, 64)
	if err != nil {
		println(err.Error(), http.StatusBadRequest)
		return "wrong id"
	}

	check := &Check{}
	err = db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(UrlsBucket).Get(KeyFor(id))
		if data == nil {
			return fmt.Errorf("no such check: %d", id)
		}

		if err := json.Unmarshal(data, check); err != nil {
			println("error unmarshaling json", err)
			return err
		}

		check.ID = id
		return nil
	})

	if err != nil {
		println(err.Error(), http.StatusInternalServerError)
		return err.Error()
	}

	if requester != int64(check.UserID) {
		return "Not your check"
	}

	check.PrepareForDisplay()

	return fmt.Sprintf("<b>%s</b>\n/%d from %d (%s)\nURL: %s\nSearch: %s\nlast checked: %s\nlast changed: %s\nMust contain string: %t", check.Title, check.ID, check.UserID, check.IsEnabledPretty, check.URL, check.Selector, check.LastCheckedPretty, check.LastChangedPretty, check.NotifyPresent)
}

func (c *Check) Modify(db *bolt.DB, requester int64, findID int64, title string, url string, search string, notifyPresent bool, isEnabled bool) (result string) {
	// id, err := strconv.ParseUint(findID, 10, 64)
	// if err != nil {
	// 	println(err.Error(), http.StatusBadRequest)
	// 	return
	// }

	check := &Check{}
	err := db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(UrlsBucket).Get(KeyFor(findID))
		if data == nil {
			return fmt.Errorf("no such check: %d", findID)
		}

		if err := json.Unmarshal(data, check); err != nil {
			println("error unmarshaling json", err)
			return err
		}

		check.ID = uint64(findID)
		return nil
	})

	if err != nil {
		println(err.Error(), http.StatusBadRequest)
		return err.Error()
	}

	if requester != int64(check.UserID) {
		return "Not your check"
	}

	// Update each of the fields in the check
	updated := false
	if c.URL != url {
		check.URL = url
		updated = true
	}
	if c.Selector != search {
		check.Selector = search
		updated = true
	}
	if c.NotifyPresent != notifyPresent {
		check.NotifyPresent = notifyPresent
		updated = true
	}
	if c.IsEnabled != isEnabled {
		check.IsEnabled = isEnabled
		updated = true
	}
	if c.Title != title {
		check.Title = title
		updated = true
	}

	if !updated {
		// println("no modifications given")
		return "no modifications given"
	}

	err = db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(check)
		if err != nil {
			return err
		}

		if err = tx.Bucket(UrlsBucket).Put(KeyFor(check.ID), data); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		// println(err.Error(), http.StatusBadRequest)
		return err.Error()
	}

	return "Edited"
}

func (c *Check) Get(db *bolt.DB /*requester int64,*/, findID string) (result *Check) {
	id, err := strconv.ParseUint(findID, 10, 64)
	if err != nil {
		println(err.Error(), http.StatusBadRequest)
		return
	}

	check := &Check{}
	err = db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(UrlsBucket).Get(KeyFor(id))
		if data == nil {
			return fmt.Errorf("no such check: %d", id)
		}

		if err := json.Unmarshal(data, check); err != nil {
			println("error unmarshaling json", err)
			return err
		}

		check.ID = id
		return nil
	})

	if err != nil {
		println(err.Error(), http.StatusBadRequest)
		return
	}

	// if requester != int64(check.UserID) {
	// 	return // "Not your check"
	// }
	return check
}

func (c *User) New(db *bolt.DB, id uint64) (result bool) {
	println("adding new user", id)

	if id <= 0 {
		println("missing id parameter", http.StatusBadRequest)
		return false
	}

	user := User{
		UserID:    int64(id),
		IsEnabled: true,
	}

	err := db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(UsersBucket).Get(KeyFor(id))
		if data != nil {
			println("already exist", id)
			return fmt.Errorf("already exist: %d", id)
		}
		return nil
	})

	if err != nil {
		println(err.Error(), http.StatusBadRequest)
		return false
	}

	err = db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(user)
		if err != nil {
			return err
		}

		b := tx.Bucket(UsersBucket)

		// seq, err := b.NextSequence()
		// if err != nil {
		// 	return err
		// }
		user.ID = uint64(id)

		if err = b.Put(KeyFor(user.ID), data); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		println("error inserting new item", err, user.UserID)
		// http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}

	return true
}

func (c *User) Check(db *bolt.DB, id uint64) (found bool) {
	println("checking user", id)

	if id <= 0 {
		println("missing id parameter", http.StatusBadRequest)
		return
	}

	err := db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(UsersBucket).Get(KeyFor(id))
		if data == nil {
			println("User not found", id)
			return fmt.Errorf("User not found: %d", id)
		}
		return nil
	})

	return (err == nil)
}
