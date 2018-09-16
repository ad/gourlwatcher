package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/boltdb/bolt"
	"github.com/robfig/cron"
	"gopkg.in/telegram-bot-api.v4"
)

type telegramResponse struct {
	body string
	to   int64
}

var (
	UrlsBucket  = []byte("urls")
	UsersBucket = []byte("users")

	telegramChan chan telegramResponse
	innerChan    chan telegramResponse
	outerChan    chan telegramResponse

	commandKeyboard tgbotapi.ReplyKeyboardMarkup
)

var telegramToken = flag.String("token", "", "token")
var authSecret = flag.String("secret", "", "secret")

func main() {
	flag.Parse()
	dbPath := "./monitor.db"
	db, err := bolt.Open(dbPath, 0666, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		println("error opening db", dbPath, err)
		return
	}
	defer db.Close()

	// Create collections.
	buckets := [][]byte{UrlsBucket, UsersBucket}
	db.Update(func(tx *bolt.Tx) error {
		for _, v := range buckets {
			b := tx.Bucket(v)
			if b == nil {
				tx.CreateBucket(v)
			}
		}
		return nil
	})

	c := cron.New()
	c.Start()
	defer c.Stop()

	startChan, oc, ic, _ := commandsManager(db, c)
	innerChan = ic
	outerChan = oc
	telegramChan = make(chan telegramResponse)

	bot, err := tgbotapi.NewBotAPI(*telegramToken)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = false
	log.Printf("Authorized on account %s", bot.Self.UserName)

	var ucfg = tgbotapi.NewUpdate(0)
	ucfg.Timeout = 60

	updates, err := bot.GetUpdatesChan(ucfg)

	if err != nil {
		log.Fatalf("[INIT] [Failed to init Telegram updates chan: %v]", err)
	}

	// check := Check{
	// 	Schedule: "0 * * * * *",
	// }

	// check.Delete(db, c, "5")

	// check.New(db, c, "https://ticket-consols.teamlab.art/stock_items/daily?from=2018-11-01&to=2018-11-30&language=en&stock_location_name=EC", "\"2018-11-01\": [],", true)

	// check.Modify(db, c, "7", "https://ticket-consols.teamlab.art/stock_items/daily?from=2018-11-01&to=2018-11-30&language=en&stock_location_name=EC", "\"2018-11-01\": [],", "false", "true")

	// Initialize for each of the existing URLs
	var items []*Check
	if err = GetAllChecks(db, &items); err != nil {
		println("error loading checks", err)
	}

	for _, v := range items {
		if v.IsEnabled {
			go v.Update(db)
			id := v.ID
			c.AddFunc(v.Schedule, func() {
				TryUpdate(db, id)
			})
		}
	}

	startChan <- true

	for {
		select {
		case update := <-updates:
			text := update.Message.Text
			command := update.Message.Command()
			args := update.Message.CommandArguments()
			userID := int64(update.Message.From.ID)
			chatID := int64(update.Message.Chat.ID)

			log.Printf("[%d] %s, %s, %s", chatID, text, command, args)

			msg := tgbotapi.NewMessage(userID, "")
			user := User{
				UserID:    userID,
				IsEnabled: true,
			}

			switch command {

			case "auth":
				if args == *authSecret {
					go func() {
						if user.New(db, uint64(userID)) {
							telegramChan <- telegramResponse{"Authorized", chatID}
						} else {
							telegramChan <- telegramResponse{"Not authorized", chatID}
						}
					}()
				}
			case "add":
				if user.Check(db, uint64(userID)) {
					println("add")
					innerChan <- telegramResponse{text, chatID}
				}
			case "edit":
				if user.Check(db, uint64(userID)) {
					println("edit")
					innerChan <- telegramResponse{text, chatID}
				}
			case "delete":
				if user.Check(db, uint64(userID)) {
					println("delete")
					innerChan <- telegramResponse{text, chatID}
				}
			case "shot":
				// https://github.com/suntong/web2image/blob/master/cdp-screenshot.go
				// https://github.com/chromedp/examples/blob/master/screenshot/main.go
				if user.Check(db, uint64(userID)) {
					println("shot")
					innerChan <- telegramResponse{text, chatID}
				}
			default:
				msg.Text = text
				msg.ReplyMarkup = commandKeyboard
				bot.Send(msg)
			}
			// }
		case resp := <-telegramChan:
			log.Println(resp.body)

			msg := tgbotapi.NewMessage(resp.to, resp.body)
			_, err := bot.Send(msg)
			if err == nil {

			}
		}
	}

	// println("Finished")
}

func TryUpdate(db *bolt.DB, id uint64) {
	// The task may have been deleted from the DB, so we try to fetch it first
	check := &Check{}
	found := false

	err := db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(UrlsBucket).Get(KeyFor(id))
		if data == nil {
			return nil
		}

		if err := json.Unmarshal(data, check); err != nil {
			println("error unmarshaling json", err)
			return err
		}

		found = true
		return nil
	})

	if err != nil {
		// TODO: log something
		return
	}

	if !found {
		println("skipping update for deleted check", id)
		return
	}

	check.PrepareForDisplay()
	// Got a check.  Trigger an update.
	go check.Update(db)
}

func commandsManager(db *bolt.DB, cron *cron.Cron) (startChan chan bool, outerChan, innerChan chan telegramResponse, stopChan chan int64) {
	startChan = make(chan bool)
	outerChan = make(chan telegramResponse)
	innerChan = make(chan telegramResponse)
	stopChan = make(chan int64)
	go func() {
		for {
			select {
			case <-startChan:
				go doCommand(db, cron, innerChan, stopChan)
			case msg := <-outerChan:
				fmt.Println("command <- ", msg)
				//default:
				//	time.Sleep(100 * time.Millisecond)
			}
		}
	}()
	return startChan, outerChan, innerChan, stopChan
}

func doCommand(db *bolt.DB, cron *cron.Cron, innerChan chan telegramResponse, stopChan chan int64) {
	for {
		select {
		case msg := <-innerChan:
			fmt.Println("command <- ", msg.body)
			go func() {
				if strings.HasPrefix(msg.body, "/delete") {
					stringSlice := strings.Split(msg.body, " ")
					if len(stringSlice) >= 2 {
						if _, err := strconv.ParseInt(stringSlice[1], 10, 64); err == nil {
							// fmt.Printf("%q looks like a number.\n", v)
							check := Check{
								Schedule: "0 * * * * *",
							}

							if check.Delete(db, stringSlice[1]) {
								telegramChan <- telegramResponse{"Deleted", msg.to}
							} else {
								telegramChan <- telegramResponse{"Not deleted", msg.to}
							}
						}
					}
				} else if strings.HasPrefix(msg.body, "/add") {
					stringSlice := strings.Split(msg.body, "\n\n")
					if len(stringSlice) >= 2 {
						commandURL := strings.Split(stringSlice[0], " ")
						url := commandURL[1]
						body := strings.Join(stringSlice[1:], "\n\n")

						check := Check{
							Schedule: "0 * * * * *",
						}

						if check.New(db, cron, url, body, "true") {
							telegramChan <- telegramResponse{"Added", msg.to}
						} else {
							telegramChan <- telegramResponse{"Not added", msg.to}
						}
					}
				}
				stopChan <- msg.to
			}()
		case id := <-stopChan:
			println(id, "stopped")
			// telegramChan <- telegramResponse{"stop", id}
		}
	}
}
