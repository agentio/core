package main

import (
	"bytes"
	"code.google.com/p/go-uuid/uuid"
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"flag"
	"fmt"
	"html/template"
	"labix.org/v2/mgo"
	"labix.org/v2/mgo/bson"
	"log"
	"net/http"
	"time"
)

type Node struct {
	Id       string
	ParentId string
	Created  time.Time
	Modified time.Time
	Name     string
	Hash     string
	Length   uint64
}

func getMongoSession() (mongoSession *mgo.Session) {
	mongoSession, err := mgo.Dial("127.0.0.1")
	if err != nil {
		panic(err)
	}
	mongoSession.SetMode(mgo.Monotonic, true)
	return mongoSession
}

func WebDAVRequestHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
	case "DELETE":
	case "PUT":
	case "MKCOL":
	case "OPTIONS":
	case "COPY":
	case "MOVE":
	case "PROPFIND":
	case "LOCK":
	case "UNLOCK":
	}
}

var port = flag.Uint("p", 8080, "the port to use for serving HTTP requests")

func main() {
	flag.Parse()
	http.HandleFunc("/", WebDAVRequestHandler)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", *port), nil))
}
