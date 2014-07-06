package main

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/gorilla/mux"
	"io/ioutil"
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
	Id       bson.ObjectId `bson:"_id,omitempty" json:"id"`
	Username string        `json:"username" bson:"username"`
	Password string        `json:"password" bson:"password"`
}

type QueueEntry struct {
	Id      bson.ObjectId `bson:"_id,omitempty"`
	Time    time.Time     `bson:"time"`
	EventId bson.ObjectId `bson:"eventid"`
}

type Event struct {
	Id       bson.ObjectId `bson:"_id,omitempty" json:"id"`
	Name     string        `bson:"name" json:"name"`
	Periodic bool          `bson:"periodic" json:"periodic"`
	Interval int64         `bson:"interval" json:"interval"`
	Method   string        `bson:"method" json:"method"`
	Url      string        `bson:"url" json:"url"`
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func md5HashWithSalt(input, salt string) string {
	hasher := hmac.New(md5.New, []byte(salt))
	hasher.Write([]byte(input))
	return hex.EncodeToString(hasher.Sum(nil))
}

func authorizeUser(username string, password string) (user User, err error) {
	saltedPassword := md5HashWithSalt(password, PasswordSalt)
	mongoSession := getMongoSession()
	defer mongoSession.Close()
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
	if AGENT_MONGO_HOST != "127.0.0.1" {
		dialInfo.Username = "root"
		dialInfo.Password = "agent123"
	}
	mongoSession, err := mgo.DialWithInfo(&dialInfo)
	if err != nil {
		panic(err)
	}
	mongoSession.SetMode(mgo.Monotonic, true)
	return mongoSession
}

// generate an appropriate response
func respondWithResult(w http.ResponseWriter, result interface{}) {
	jsonData, err := json.Marshal(result)
	if err != nil {
		fmt.Println(err)
	}
	w.Write(jsonData)
}

func getEventsHandler(w http.ResponseWriter, r *http.Request) {
	var events []Event
	mongoSession := getMongoSession()
	defer mongoSession.Close()
	collection := mongoSession.DB("ticker").C("events")
	err := collection.Find(nil).All(&events)
	check(err)
	if events == nil {
		events = []Event{}
	}
	respondWithResult(w, events)
}

func enqueueEventWithId(eventid bson.ObjectId) {
	mongoSession := getMongoSession()
	defer mongoSession.Close()
	eventsCollection := mongoSession.DB("ticker").C("events")
	var event Event
	err := eventsCollection.Find(bson.M{"_id": eventid}).One(&event)
	if err != nil {
		fmt.Printf("no event? %v\n", err)
		return
	}
	var queueEntry QueueEntry
	queueEntry.Time = time.Now().Add(time.Duration(event.Interval) * time.Second)
	queueEntry.EventId = eventid
	queueCollection := mongoSession.DB("ticker").C("queue")
	err = queueCollection.Insert(queueEntry)
	if err != nil {
		fmt.Printf("what? %+v\n", err)
		return
	}
}

func createEvent(event map[string]interface{}) (eventid bson.ObjectId, err error) {
	mongoSession := getMongoSession()
	defer mongoSession.Close()
	collection := mongoSession.DB("ticker").C("events")
	newId := bson.NewObjectId()
	event["_id"] = newId
	err = collection.Insert(event)

	enqueueEventWithId(newId)

	return newId, err
}

func postEventsHandler(w http.ResponseWriter, r *http.Request) {
	buffer, err := ioutil.ReadAll(r.Body)
	r.Body.Close()
	var event map[string]interface{}
	err = json.Unmarshal(buffer, &event)
	if err != nil {
		panic(err)
	}
	eventid, err := createEvent(event)
	check(err)
	result := map[string]interface{}{
		"message": "OK",
		"eventid": eventid,
	}
	respondWithResult(w, result)
}

func deleteAllEvents() (err error) {
	mongoSession := getMongoSession()
	defer mongoSession.Close()
	collection := mongoSession.DB("ticker").C("events")
	_, err = collection.RemoveAll(bson.M{})
	return err
}

func deleteEventsHandler(w http.ResponseWriter, r *http.Request) {
	err := deleteAllEvents()
	check(err)
	result := map[string]interface{}{
		"message": "OK",
	}
	respondWithResult(w, result)
}

func getEvent(eventid string, event *Event) (err error) {
	mongoSession := getMongoSession()
	defer mongoSession.Close()
	collection := mongoSession.DB("ticker").C("events")
	if bson.IsObjectIdHex(eventid) {
		oid := bson.ObjectIdHex(eventid)
		return collection.Find(bson.M{"_id": oid}).One(&event)
	} else {
		return collection.Find(bson.M{"name": eventid}).One(&event)
	}
}

func getEventHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	eventid := vars["eventid"]
	var event Event
	err := getEvent(eventid, &event)
	check(err)
	respondWithResult(w, event)
}

func postEventHandler(w http.ResponseWriter, r *http.Request) {

}

func deleteEvent(event Event) {
	oid := event.Id
	mongoSession := getMongoSession()
	defer mongoSession.Close()
	collection := mongoSession.DB("ticker").C("events")
	err := collection.Remove(bson.M{"_id": oid})
	check(err)
}

func deleteEventHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	eventid := vars["eventid"]
	var event Event
	err := getEvent(eventid, &event)
	check(err)
	deleteEvent(event)
	result := map[string]interface{}{
		"message": "OK",
	}
	respondWithResult(w, result)
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
		if false {
			_, err := authorize(r)
			if err != nil {
				w.WriteHeader(401)
				w.Write([]byte("Unauthorized"))
				return
			}
		}
		fn(w, r)
	}
}

var counter uint64

func tickerFunction(t time.Time) {
	fmt.Printf("%v\t Tick at %v\n", counter, t)
	counter++
	mongoSession := getMongoSession()
	defer mongoSession.Close()
	queueCollection := mongoSession.DB("ticker").C("queue")
	var queueEntry QueueEntry

	err := queueCollection.Find(bson.M{}).Sort("time").One(&queueEntry)
	if err != nil {
		return
	}
	fmt.Printf("Found queue entry %+v\n\n", queueEntry)
	if queueEntry.Time.Before(time.Now()) {

		eventsCollection := mongoSession.DB("ticker").C("events")
		var event Event
		err = eventsCollection.Find(bson.M{"_id": queueEntry.EventId}).One(&event)
		if err != nil {
			// the event is gone
			err = queueCollection.Remove(bson.M{"_id": queueEntry.Id})
			return
		}
		if event.Periodic {
			var newQueueEntry QueueEntry
			newQueueEntry.Time = queueEntry.Time.Add(time.Duration(event.Interval) * time.Second)
			newQueueEntry.EventId = queueEntry.EventId
			err = queueCollection.Insert(newQueueEntry)
		}
		// perform the event action
		client := &http.Client{}
		req, err := http.NewRequest(event.Method, event.Url, nil)
		req.Header.Set("User-Agent", "Ticker")
		resp, err := client.Do(req)
		fmt.Printf("%+v\n\n", resp)

		defer resp.Body.Close()
		if false {
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				panic(err)
			}
			fmt.Printf("%+v\n\n", string(body))
		}
		err = queueCollection.Remove(bson.M{"_id": queueEntry.Id})

		if err != nil {
			return
		}
	}
}

var port = flag.Uint("p", 8080, "the port to use for serving HTTP requests")

var API = []struct {
	path        string
	method      string
	handler     func(http.ResponseWriter, *http.Request)
	description string
}{
	{"/ticker/events", "GET", getEventsHandler, "get list of events"},
	{"/ticker/events", "POST", postEventsHandler, "create an event"},
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
