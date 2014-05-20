// Command reloader serves a directory via HTTP, injecting JavaScript into the
// HTML files that uses long polling to reload when the file changes.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/howeyc/fsnotify"
	"sethwklein.net/go/errutil"
)

const payload = `<script>
(function() {
	var disabler = document.createElement('div');
	disabler.style.position = 'fixed';
	disabler.style.left = '1px';
	disabler.style.top = '1px';
	disabler.style.width = '10px';
	disabler.style.height = '10px';
	disabler.style.borderRadius = '5px';
	disabler.style.backgroundColor = 'red';
	disabler.style.cursor = 'pointer';
	// disabler.appendChild(document.createTextNode("..."));
	document.body.appendChild(disabler);

	var poll = function() {
		var xhr = new XMLHttpRequest();
		var abort = function() {
			xhr.abort();
			disabler.style.display = 'none';
		};
		disabler.addEventListener("click", abort, false);
		disabler.style.display = 'block';

		xhr.onload = function() {
			disabler.removeEventListener("click", abort, false);
			disabler.style.display = 'none';
			if (xhr.status === 408) {
				poll();
			} else {
				document.location.reload(true)
			}
		};
		xhr.onerror = function() {
			disabler.removeEventListener("click", abort, false);
			disabler.style.display = 'none';
			console.log("error", xhr);
			console.log("reload to restart automatic reloading");
		};
		xhr.open("GET", "/notification");
		xhr.send();
	};
	poll();
})();
</script>
`

var payloadBytes = []byte(payload)

var (
	rBody = regexp.MustCompile(`</[bB][oO][dD][yY][ \t\n]*>`)
	rHtml = regexp.MustCompile(`</[hH][tT][mM][lL][ \t\n]*>`)
)

func injectionPoint(content []byte) int {
	// BUG: this can be fooled by comments
	match := rBody.FindIndex(content)
	if len(match) > 0 {
		return match[0]
	}
	match = rHtml.FindIndex(content)
	if len(match) > 0 {
		return match[0]
	}
	return len(content)
}

// httpError replies to the request with the specified error message and HTTP code.
// The error message should be plain text.
func httpError(w http.ResponseWriter, error string, code int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	io.WriteString(w, template.HTMLEscapeString(error))
	w.Write(payloadBytes)
}

func mainError() (err error) {
	if len(os.Args) < 2 {
		return UsageError{errors.New("directory required")}
	}
	if os.Args[1] == "--help" {
		return UsageError{nil}
	}

	directory := os.Args[1]

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

	changed := make(chan chan int)
	go func() {
		// we want a stopped timer. whoever designed time.Timer may not
		// have considered the possibility, so we do this hack.
		settled := time.NewTimer(math.MaxInt64)
		settled.Stop()

		const patience = 60 * time.Second
		timeout := time.NewTimer(patience)

		var listeners []chan int
		next := make(chan int, 1)

		for {
			select {
			case event, open := <-watcher.Event:
				if !open {
					log.Println("shutting down file change watcher")
					return
				}
				if name := filepath.Base(event.Name); strings.HasPrefix(name, ".") {
					log.Println("ignoring", name)
					continue
				}
				settled.Reset(time.Second / 4)
			case <-settled.C:
				for _, listener := range listeners {
					listener <- 200
				}
				listeners = listeners[:0]
				timeout.Reset(patience)
			case <-timeout.C:
				for _, listener := range listeners {
					listener <- http.StatusRequestTimeout
				}
				listeners = listeners[:0]
				timeout.Reset(patience)
			case changed <- next:
				listeners = append(listeners, next)
				next = make(chan int, 1)
			}
		}
	}()

	err = watcher.Watch(directory)
	if err != nil {
		return err
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/notification":
			log.Println("holding", r.URL.Path)
			status := <-<-changed
			log.Println("reporting", status)
			w.WriteHeader(status)
		default:
			url := r.URL.Path
			if url == "/" {
				url = "/index.html"
			}

			path := filepath.Join(directory, filepath.FromSlash(url))

			content, err := ioutil.ReadFile(path)
			if err != nil {
				log.Printf("error %s: %s\n", path, err)
				httpError(w, err.Error(), http.StatusNotFound)
				return
			}

			if filepath.Ext(url) != ".html" && http.DetectContentType(content) != "text/html" {
				log.Printf("blob %s\n", path)
				http.ServeContent(w, r, url, time.Time{}, bytes.NewReader(content))
				return
			}

			log.Printf("html %s\n", path)
			w.Header().Set("Content-Type", "text/html")

			i := injectionPoint(content)
			w.Write(content[:i])
			w.Write(payloadBytes)
			w.Write(content[i:])
		}
	})

	address := "localhost:8000"
	log.Println("listening on", address)
	err = http.ListenAndServe(address, nil)
	return err
}

type UsageError struct {
	error
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
