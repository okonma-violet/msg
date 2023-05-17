// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"log"
	"net/http"
)

func serveHome(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "home.html")
}

func main() {
	http.HandleFunc("/", serveHome)
	err := http.ListenAndServe(":9020", nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
