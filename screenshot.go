package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/gobs/args"
	"github.com/raff/godet"
)

func screenshot(url string) (filename string) {
	var chromeapp string

	hash := sha256.New()
	io.WriteString(hash, url)
	os.MkdirAll("gourlwatcher", 0644)
	filename = "gourlwatcher/" + hex.EncodeToString(hash.Sum(nil)) + ".png"

	switch runtime.GOOS {
	case "darwin":
		for _, c := range []string{
			"/Applications/Google Chrome Canary.app",
			"/Applications/Google Chrome.app",
		} {
			// MacOS apps are actually folders
			if info, err := os.Stat(c); err == nil && info.IsDir() {
				chromeapp = fmt.Sprintf("open %q --args", c)
				break
			}
		}

	case "linux":
		for _, c := range []string{
			"headless_shell",
			"chromium",
			"google-chrome-beta",
			"google-chrome-unstable",
			"google-chrome-stable"} {
			if _, err := exec.LookPath(c); err == nil {
				chromeapp = c
				break
			}
		}

	case "windows":
	}

	if chromeapp != "" {
		if chromeapp == "headless_shell" {
			chromeapp += " --no-sandbox"
		} else {
			chromeapp += " --headless"
		}

		chromeapp += " --no-gpu --disable-software-rasterizer --headless --mute-audio --hide-scrollbars --no-sandbox --remote-debugging-port=9222 --disable-extensions --disable-gpu about:blank"
	}

	parts := args.GetArgs(chromeapp)
	cmd := exec.Command(parts[0], parts[1:]...)
	if err := cmd.Start(); err != nil {
		// log.Println("cannot start browser", err)
		return
	}

	var remote *godet.RemoteDebugger
	var err error

	for i := 0; i < 10; i++ {
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}

		remote, err = godet.Connect("localhost:9222", false)
		if err == nil {
			break
		}

		// log.Println("connect", err)
	}

	if err != nil {
		// log.Fatal("cannot connect to browser")
		return
	}

	defer remote.Close()

	// get browser and protocol version
	// version, _ := remote.Version()
	// fmt.Println(version)

	// get list of open tabs
	tabs, _ := remote.TabList("")
	// fmt.Println(tabs)

	// block loading of most images
	_ = remote.SetBlockedURLs("*.jpg", "*.png", "*.gif", "*.svg", "*.tiff")

	// create new tab
	_, _ = remote.NewTab(url)
	// fmt.Println(tab)

	// enable event processing
	remote.RuntimeEvents(true)
	remote.NetworkEvents(true)
	remote.PageEvents(true)
	remote.DOMEvents(true)
	remote.LogEvents(true)

	// navigate in existing tab
	_ = remote.ActivateTab(tabs[0])

	// re-enable events when changing active tab
	remote.AllEvents(true) // enable all events

	_, _ = remote.Navigate(url)

	time.Sleep(5 * time.Second)

	// id := documentNode(remote, false)

	// res, err := remote.QuerySelector(id, "html")
	// if err != nil {
	// 	log.Fatal("error in querySelector: ", err)
	// }

	// id = int(res["nodeId"].(float64))

	// res, err = remote.GetBoxModel(id)
	// if err != nil {
	// 	log.Fatal("error in getBoxModel: ", err)
	// }

	// if res == nil {
	// 	log.Println("BoxModel not available")
	// } else {
	// res = res["model"].(map[string]interface{})
	width := 1024 //int(res["width"].(float64))
	height := 1536

	_ = remote.SetVisibleSize(width, height)
	// }

	// take a screenshot
	_ = remote.SaveScreenshot(filename, 0644, 0, true)
	time.Sleep(time.Second)

	return filename
}
