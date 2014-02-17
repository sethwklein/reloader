// Command reloader serves an HTML file, injecting JavaScript into it that
// uses long polling to reload when the file changes.
package main

import (
	"errors"
	"fmt"
	"github.com/howeyc/fsnotify"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sethwklein.net/go/errutil"
	"time"
)

const code = `<!DOCTYPE html>
<html>
  <head>
    <meta charset="utf-8">
    <script>
	var poll = function() {
		var xhr = new XMLHttpRequest();
		xhr.onload = function() {
			if (xhr.status === 408) {
				console.log("timeout");
				poll();
			} else {
				document.location.reload(true)
			}
		};
		xhr.onerror = function() {
			// reload to restart automatic reloading
			// currently, we expect the user to figure this out without a tip
			console.log("error", xhr);
		};
		xhr.open("GET", "/notification");
		xhr.send();
	};
	poll();
    </script>
  </head>
  <body>
  </body>
</html>
`

type UsageError struct {
	error
}

func mainError() (err error) {
	if len(os.Args) < 2 {
		return UsageError{errors.New("filename required")}
	}
	if os.Args[1] == "--help" {
		return UsageError{nil}
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer errutil.AppendCall(&err, watcher.Close)

	go func() {
		for err := range watcher.Error {
			log.Println("error: fsnotify:", err)
		}
	}()

	changed := make(chan struct{})
	go func() {
		// we want a stopped timer. whoever designed time.Timer may not
		// have considered the possibility, so we do this hack.
		var timer = time.NewTimer(math.MaxInt64)
		timer.Stop()
		var haveChange chan struct{}
		for {
			select {
			case event, open := <-watcher.Event:
				if !open {
					log.Println("shutdown: watcher")
					return
				}
				log.Println(event)
				timer.Reset(time.Second / 4)
			case <-timer.C:
				log.Println("storing change")
				haveChange = changed
			case haveChange <- struct{}{}:
				log.Println("reported change")
				haveChange = nil
			}
		}
	}()

	err = watcher.Watch(os.Args[1])
	if err != nil {
		return err
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			log.Println("serving", r.URL.Path)
			w.Write([]byte(code))
		case "/notification":
			log.Println("holding", r.URL.Path)
			select {
			case <-time.After(60 * time.Second):
				w.WriteHeader(http.StatusRequestTimeout)
			case <-changed:
				w.WriteHeader(200)
			}
		default:
			log.Println("not found:", r.URL.Path)
			http.NotFound(w, r)
		}
	})

	address := "localhost:8000"
	log.Println("listening on", address)
	err = http.ListenAndServe(address, nil)
	return err
}

func Usage(w io.Writer) {
	bin := filepath.Base(os.Args[0])
	fmt.Fprintf(w, "usage: %s <filename>\n", bin)
	fmt.Fprintf(w, "       %s --help\n", bin)
}

func mainCode() int {
	err := mainError()
	if err == nil {
		return 0
	}
	if (err == UsageError{nil}) {
		Usage(os.Stdout)
		return 0
	}
	if err, ok := err.(UsageError); ok {
		fmt.Fprintf(os.Stderr, "error: %v\n\n", err)
		Usage(os.Stderr)
		return 1
	}
	log.Println("error:", err)
	return 1
}

func main() {
	os.Exit(mainCode())
}
