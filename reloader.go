// Command reloader serves an HTML file, injecting JavaScript into it that
// uses long polling to reload when the file changes.
package main

import (
	"log"
	"os"
)

func mainError() (err error) {
	return nil
}

func mainCode() int {
	err := mainError()
	if err == nil {
		return 0
	}
	log.Println("Error:", err)
	return 1
}

func main() {
	os.Exit(mainCode())
}
