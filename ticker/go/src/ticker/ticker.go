package main

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"github.com/gorilla/mux"
	"labix.org/v2/mgo"
	"labix.org/v2/mgo/bson"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const PasswordSalt = "agent.io"

func dumpRequest(request *http.Request) {
	fmt.Println("Form")
	for key, value := range request.Form {
		fmt.Println("Key:", key, "Value:", value)
	}
	fmt.Println("Headers")
	for key, value := range request.Header {
		fmt.Println("Key:", key, "Value:", value)
	}
}

type User struct {
	Id       bson.ObjectId `json:"id" bson:"_id,omitempty"`
	Username string        `json:"username"`
	Password string        `json:"password"`
}

type QueueEntry struct {
	ID       bson.ObjectId `bson:"_id,omitempty"`
	Time     time.Time
	Password string
	EventId  bson.ObjectId
}

type Event struct {
	ID       bson.ObjectId `bson:"_id,omitempty"`
	Name     string
	Periodic bool
	Interval int64
	Method   string
	Url      string
}

func md5HashWithSalt(input, salt string) string {
	hasher := hmac.New(md5.New, []byte(salt))
	hasher.Write([]byte(input))
	return hex.EncodeToString(hasher.Sum(nil))
}

func authorizeUser(username string, password string) (user User, err error) {
	saltedPassword := md5HashWithSalt(password, PasswordSalt)
	mongoSession := getMongoSession()
	usersCollection := mongoSession.DB("accounts").C("users")
	err = usersCollection.Find(bson.M{"username": username, "password": saltedPassword}).One(&user)
	return user, err
}

func getMongoSession() (mongoSession *mgo.Session) {
	AGENT_MONGO_HOST := os.Getenv("AGENT_MONGO_HOST")
	if len(AGENT_MONGO_HOST) == 0 {
		AGENT_MONGO_HOST = "127.0.0.1"
	}
	var dialInfo mgo.DialInfo
	dialInfo.Addrs = []string{AGENT_MONGO_HOST}
	dialInfo.Username = "root"
	dialInfo.Password = "agent123"
	mongoSession, err := mgo.DialWithInfo(&dialInfo)
	if err != nil {
		panic(err)
	}
	mongoSession.SetMode(mgo.Monotonic, true)
	return mongoSession
}

func getEventsHandler(w http.ResponseWriter, r *http.Request) {

}

func postEventsHandler(w http.ResponseWriter, r *http.Request) {

}

func deleteEventsHandler(w http.ResponseWriter, r *http.Request) {

}

func getEventHandler(w http.ResponseWriter, r *http.Request) {

}

func postEventHandler(w http.ResponseWriter, r *http.Request) {

}

func deleteEventHandler(w http.ResponseWriter, r *http.Request) {

}

func authorize(r *http.Request) (user User, err error) {
	authorization := r.Header["Authorization"]
	if len(authorization) == 1 {
		fields := strings.Fields(authorization[0])
		authorizationType := strings.ToLower(fields[0])
		authorizationToken := fields[1]
		if authorizationType == "basic" {
			data, err := base64.StdEncoding.DecodeString(authorizationToken)
			if err != nil {
				fmt.Println("error:", err)
				return user, err
			}
			credentials := string(data)
			pair := strings.SplitN(credentials, ":", 2)
			user, err := authorizeUser(pair[0], pair[1])
			return user, err
		} else {
			return user, errors.New(fmt.Sprintf("Unsupported authorization type: %s", authorizationType))
		}
	} else {
		return user, errors.New("No authorization header")
	}
	return user, err
}

func authorizedHandler(fn func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := authorize(r)
		if err != nil {
			w.WriteHeader(401)
			w.Write([]byte("Unauthorized"))
			return
		}
		fn(w, r)
	}
}

func tickerFunction(t time.Time) {
	fmt.Println("Tick at", t)
}

var port = flag.Uint("p", 8080, "the port to use for serving HTTP requests")

var API = []struct {
	path        string
	method      string
	handler     func(http.ResponseWriter, *http.Request)
	description string
}{
	{"/ticker/events", "GET", getEventsHandler, "get list of events"},
	{"/ticker/events", "POST", postEventsHandler, "create an event or send a command to all events (start, stop)"},
	{"/ticker/events", "DELETE", deleteEventsHandler, "delete all events"},
	{"/ticker/events/{eventid}", "GET", getEventHandler, "get an event"},
	{"/ticker/events/{eventid}", "POST", postEventHandler, "send a command to an event (start, stop)"},
	{"/ticker/events/{eventid}", "DELETE", deleteEventHandler, "delete an event"},
}

func main() {
	flag.Parse()

	ticker := time.NewTicker(time.Millisecond * 1000)
	go func() {
		for t := range ticker.C {
			tickerFunction(t)
		}
	}()

	r := mux.NewRouter()
	for _, endpoint := range API {
		// extract the pieces
		path := endpoint.path
		method := endpoint.method
		handler := endpoint.handler
		description := endpoint.description
		// register the handler
		fmt.Printf("adding %v %v # %v\n", method, path, description)
		r.HandleFunc(path, authorizedHandler(handler)).Methods(method)
	}
	http.Handle("/", r)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", *port), nil))
}
