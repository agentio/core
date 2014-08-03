//
//  Copyright 2014 Radtastical Inc.
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.
//
package main

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// Events are regularly executed actions.
// Each event is an HTTP method to be performed on a URL.
// The interval between events is specified in seconds.
// Currently only periodic events are supported.
type Event struct {
	Id       bson.ObjectId `bson:"_id,omitempty" json:"id"`
	Name     string        `bson:"name" json:"name"`
	Periodic bool          `bson:"periodic" json:"periodic"`
	Interval float32       `bson:"interval" json:"interval"`
	Method   string        `bson:"method" json:"method"`
	Url      string        `bson:"url" json:"url"`
}

// QueueEntry instances are used to schedule Events on a queue.
// Each event typically has one QueueEntry scheduled.
// A new QueueEntry for an Event is scheduled when its predecessor completes.
type QueueEntry struct {
	Id      bson.ObjectId `bson:"_id,omitempty"`
	Time    time.Time     `bson:"time"`
	EventId bson.ObjectId `bson:"eventid"`
}

// Result instances store the results of event executions.
type Result struct {
	Id         bson.ObjectId `bson:"_id,omitempty" json:"id"`
	EventId    bson.ObjectId `bson:"eventid"`
	Time       time.Time     `bson:"time"`
	StatusCode int           `bson:"statuscode"`
	Body       string        `bson:"body"`
}

func md5HashWithSalt(input, salt string) string {
	hasher := hmac.New(md5.New, []byte(salt))
	hasher.Write([]byte(input))
	return hex.EncodeToString(hasher.Sum(nil))
}

func authorizeUser(username string, password string) (user User, err error) {
	saltedPassword := md5HashWithSalt(password, PasswordSalt)
	mongoSession, err := getMongoSession()
	if err != nil {
		return
	}
	defer mongoSession.Close()
	usersCollection := mongoSession.DB("accounts").C("users")
	err = usersCollection.Find(bson.M{"username": username, "password": saltedPassword}).One(&user)
	return
}

func getMongoSession() (mongoSession *mgo.Session, err error) {
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
	mongoSession, err = mgo.DialWithInfo(&dialInfo)
	if err == nil {
		mongoSession.SetMode(mgo.Monotonic, true)
	}
	return
}

func respondWithResult(w http.ResponseWriter, result interface{}) {
	jsonData, err := json.Marshal(result)
	if err != nil {
		fmt.Println(err)
	}
	w.Write(jsonData)
}

func respondWithStatus(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	w.Write([]byte(message))
}

func respondWithError(w http.ResponseWriter, err error) {
	respondWithStatus(w, 500, fmt.Sprintf("%v", err))
}

func getEventsHandler(w http.ResponseWriter, r *http.Request) {
	var events []Event
	mongoSession, err := getMongoSession()
	if err != nil {
		respondWithError(w, err)
		return
	}
	defer mongoSession.Close()
	collection := mongoSession.DB("ticker").C("events")
	err = collection.Find(nil).All(&events)
	if err != nil {
		respondWithError(w, err)
		return
	}
	if events == nil {
		events = []Event{}
	}
	respondWithResult(w, events)
}

const launchEventsImmediately = true

func enqueueEventWithId(eventid bson.ObjectId) (err error) {
	mongoSession, err := getMongoSession()
	if err != nil {
		return
	}
	defer mongoSession.Close()
	eventsCollection := mongoSession.DB("ticker").C("events")
	var event Event
	err = eventsCollection.Find(bson.M{"_id": eventid}).One(&event)
	if err != nil {
		return
	}
	var queueEntry QueueEntry
	if launchEventsImmediately {
		// if we use this line, we schedule the event to occur immediately
		queueEntry.Time = time.Now()
	} else {
		// if we use the following line, we schedule the event to occur one interval from now.
		queueEntry.Time = time.Now().Add(time.Duration(event.Interval*1000000) * time.Microsecond)
	}
	queueEntry.EventId = eventid
	queueCollection := mongoSession.DB("ticker").C("queue")
	err = queueCollection.Insert(queueEntry)
	if err != nil {
		return
	}
	return scheduleNextEventHandler()
}

func createEvent(event map[string]interface{}) (eventid bson.ObjectId, err error) {
	mongoSession, err := getMongoSession()
	if err != nil {
		return
	}
	defer mongoSession.Close()
	collection := mongoSession.DB("ticker").C("events")
	newId := bson.NewObjectId()
	event["_id"] = newId
	err = collection.Insert(event)
	if err != nil {
		return
	}
	err = enqueueEventWithId(newId)
	return newId, err
}

func postEventsHandler(w http.ResponseWriter, r *http.Request) {
	buffer, err := ioutil.ReadAll(r.Body)
	r.Body.Close()
	var event map[string]interface{}
	err = json.Unmarshal(buffer, &event)
	if err != nil {
		respondWithError(w, err)
		return
	}
	eventid, err := createEvent(event)
	if err != nil {
		respondWithError(w, err)
		return
	}
	result := map[string]interface{}{
		"message": "OK",
		"eventid": eventid,
	}
	respondWithResult(w, result)
}

func deleteAllEvents() (err error) {
	mongoSession, err := getMongoSession()
	if err != nil {
		return
	}
	defer mongoSession.Close()
	collection := mongoSession.DB("ticker").C("events")
	_, err = collection.RemoveAll(bson.M{})
	return
}

func deleteEventsHandler(w http.ResponseWriter, r *http.Request) {
	err := deleteAllEvents()
	if err != nil {
		respondWithError(w, err)
		return
	}
	result := map[string]interface{}{
		"message": "OK",
	}
	respondWithResult(w, result)
}

func getEvent(eventid string, event *Event) (err error) {
	mongoSession, err := getMongoSession()
	if err != nil {
		return
	}
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
	if err != nil {
		respondWithError(w, err)
		return
	}
	respondWithResult(w, event)
}

func postEventHandler(w http.ResponseWriter, r *http.Request) {
	respondWithStatus(w, 501, "not implemented")
}

func deleteEvent(event Event) (err error) {
	oid := event.Id
	mongoSession, err := getMongoSession()
	if err != nil {
		return
	}
	defer mongoSession.Close()
	collection := mongoSession.DB("ticker").C("events")
	return collection.Remove(bson.M{"_id": oid})
}

func deleteEventHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	eventid := vars["eventid"]
	var event Event
	err := getEvent(eventid, &event)
	if err != nil {
		respondWithError(w, err)
		return
	}
	err = deleteEvent(event)
	if err != nil {
		respondWithError(w, err)
		return
	}
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
		if false { // set to true to secure the API
			_, err := authorize(r)
			if err != nil {
				respondWithStatus(w, 401, "Unauthorized")
				return
			}
		}
		fn(w, r)
	}
}

var cancel chan int

func cancelNextEventHandler() (err error) {
	if cancel != nil {
		close(cancel)
		cancel = nil
	}
	return
}

func scheduleNextEventHandler() (err error) {
	cancelNextEventHandler()

	// look in db for next queue entry
	mongoSession, err := getMongoSession()
	if err != nil {
		return err
	}
	defer mongoSession.Close()
	queueCollection := mongoSession.DB("ticker").C("queue")
	var queueEntry QueueEntry
	err = queueCollection.Find(bson.M{}).Sort("time").One(&queueEntry)
	if err != nil {
		// if there is no queue entry, we're done
		return err
	}

	// since we found a queue entry,
	//fmt.Printf("Found queue entry %+v\n\n", queueEntry)
	// get its time of occurrence
	eventTime := queueEntry.Time
	// determine how long we need to wait to trigger it
	waitInterval := eventTime.Sub(time.Now())
	fmt.Printf("waiting %v\n", waitInterval)
	// launch a goroutine to handle it
	cancel = make(chan int)
	go func() {
		select {
		case <-time.After(waitInterval):
			fmt.Printf("processing queue at: %v\n", time.Now())
			mongoSession, err := getMongoSession()
			if err != nil {
				fmt.Printf("error getting database connection %+v\n", err)
				return
			}
			defer mongoSession.Close()
			eventsCollection := mongoSession.DB("ticker").C("events")
			queueCollection := mongoSession.DB("ticker").C("queue")
			// get the event associated with the queue entry
			var event Event
			err = eventsCollection.Find(bson.M{"_id": queueEntry.EventId}).One(&event)
			if err != nil {
				// if the event is gone, we don't handle it
				fmt.Printf("No event, removing entry from queue and continuing.\n")
				err = queueCollection.Remove(bson.M{"_id": queueEntry.Id})
				scheduleNextEventHandler()
				return
			}
			// perform the event action
			client := &http.Client{}
			req, err := http.NewRequest(event.Method, event.Url, nil)
			if err != nil {
				fmt.Printf("error creating request for event %+v\n", err)
			} else {
				req.Header.Set("User-Agent", "Ticker")
				fmt.Printf("calling %+v\n", req)
				resp, err := client.Do(req)
				// ignore errors when processing actions, these could be remote errors
				var result Result
				result.EventId = queueEntry.EventId
				result.Time = time.Now()
				if err == nil {
					fmt.Printf("response %+v\n\n", resp)
					defer resp.Body.Close()
					body, err := ioutil.ReadAll(resp.Body)
					if err == nil {
						fmt.Printf("received %+v bytes\n\n", len(body))
					} else {
						fmt.Printf("error reading response for event %+v\n", err)
					}
					result.Body = string(body)
					result.StatusCode = resp.StatusCode
				} else {
					fmt.Printf("error %+v\n\n", err)
					result.Body = fmt.Sprintf("%v", err)
					result.StatusCode = 0
				}
				resultsCollection := mongoSession.DB("ticker").C("results")
				err = resultsCollection.Insert(result)
				if err != nil {
					fmt.Printf("error saving response for event %+v\n", err)
				}
			}
			// if necessary, create the next queue entry
			if event.Periodic {
				timeStep := time.Duration(event.Interval*1000000) * time.Microsecond
				now := time.Now()
				var newQueueEntry QueueEntry
				newQueueEntry.Time = queueEntry.Time
				if now.Sub(newQueueEntry.Time) > 10*timeStep {
					// if we're too far in the past, take a step from now.
					newQueueEntry.Time = now.Add(timeStep)
				} else {
					// increment the new queue event time until it's in the future.
					for newQueueEntry.Time.Before(now) {
						newQueueEntry.Time = newQueueEntry.Time.Add(timeStep)
					}
				}
				newQueueEntry.EventId = queueEntry.EventId
				err = queueCollection.Insert(newQueueEntry)
				if err != nil {
					// if this fails, event scheduling stops.
					panic(err)
				}
			}
			// after everything has completed successfully, remove the old queue entry
			err = queueCollection.Remove(bson.M{"_id": queueEntry.Id})
			// schedule the next event
			scheduleNextEventHandler()
		case <-cancel:
			fmt.Println("stopping event processing")
		}
	}()
	return
}

func startHandler(w http.ResponseWriter, r *http.Request) {
	err := scheduleNextEventHandler()
	if err != nil {
		respondWithError(w, err)
		return
	}
	w.Write([]byte("START OK"))
}

func stopHandler(w http.ResponseWriter, r *http.Request) {
	err := cancelNextEventHandler()
	if err != nil {
		respondWithError(w, err)
		return
	}
	w.Write([]byte("STOP OK"))
}

type APImethod struct {
	path        string
	method      string
	handler     func(http.ResponseWriter, *http.Request)
	description string
}

func runAPI(API []APImethod) {
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
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func main() {
	scheduleNextEventHandler()
	runAPI([]APImethod{
		{"/ticker/start", "GET", startHandler, "start"},
		{"/ticker/stop", "GET", stopHandler, "stop"},
		{"/ticker/events", "GET", getEventsHandler, "get list of events"},
		{"/ticker/events", "POST", postEventsHandler, "create an event"},
		{"/ticker/events", "DELETE", deleteEventsHandler, "delete all events"},
		{"/ticker/events/{eventid}", "GET", getEventHandler, "get an event"},
		{"/ticker/events/{eventid}", "POST", postEventHandler, "send a command to an event (start, stop)"},
		{"/ticker/events/{eventid}", "DELETE", deleteEventHandler, "delete an event"},
	})
}
