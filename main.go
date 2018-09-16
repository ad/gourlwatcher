package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
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

	startChan, oc, ic, _ := commandsManager(db)
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

			log.Printf("[%d] %s, %s, %s", update.Message.Chat.ID, text, command, args)

			msg := tgbotapi.NewMessage(int64(update.Message.From.ID), "")
			user := User{
				UserID:    int64(update.Message.From.ID),
				IsEnabled: true,
			}

			switch command {

			case "auth":
				if args == *authSecret {
					user.New(db, uint64(update.Message.From.ID))
				}
			case "add":
				if user.Check(db, uint64(update.Message.From.ID)) {
					println("add")
					innerChan <- telegramResponse{text, int64(update.Message.Chat.ID)}
				}
			case "edit":
				if user.Check(db, uint64(update.Message.From.ID)) {
					println("add")
					innerChan <- telegramResponse{text, int64(update.Message.Chat.ID)}
				}
			case "delete":
				if user.Check(db, uint64(update.Message.From.ID)) {
					println("add")
					innerChan <- telegramResponse{text, int64(update.Message.Chat.ID)}
				}
			case "shot":
				// https://github.com/suntong/web2image/blob/master/cdp-screenshot.go
				// https://github.com/chromedp/examples/blob/master/screenshot/main.go
				if user.Check(db, uint64(update.Message.From.ID)) {
					println("add")
					innerChan <- telegramResponse{text, int64(update.Message.Chat.ID)}
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

func commandsManager(db *bolt.DB) (startChan chan bool, outerChan, innerChan chan telegramResponse, stopChan chan int64) {
	startChan = make(chan bool)
	outerChan = make(chan telegramResponse)
	innerChan = make(chan telegramResponse)
	stopChan = make(chan int64)
	go func() {
		for {
			select {
			case <-startChan:
				go doCommand(db, innerChan, stopChan)
			case msg := <-outerChan:
				fmt.Println("command <- ", msg)
				//default:
				//	time.Sleep(100 * time.Millisecond)
			}
		}
	}()
	return startChan, outerChan, innerChan, stopChan
}

func doCommand(db *bolt.DB, innerChan chan telegramResponse, stopChan chan int64) {
	for {
		select {
		case msg := <-innerChan:
			fmt.Println("command <- ", msg.body)
			go func() {
				stopChan <- msg.to
			}()
		case id := <-stopChan:
			telegramChan <- telegramResponse{"stop", id}
		}
	}
}
