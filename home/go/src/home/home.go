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
	"bytes"
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
	"os"
	"time"
)

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

func md5HashWithSalt(input, salt string) string {
	hasher := hmac.New(md5.New, []byte(salt))
	hasher.Write([]byte(input))
	return hex.EncodeToString(hasher.Sum(nil))
}

type Signin struct {
	Message  string
	Username string
	Password string
}

type User struct {
	ID       bson.ObjectId `bson:"_id,omitempty"`
	Username string
	Password string
}

type Session struct {
	ID       bson.ObjectId `bson:"_id,omitempty"`
	Value    string
	Username string
	Expires  time.Time
}

func (self Session) String() string {
	var buffer bytes.Buffer
	buffer.WriteString(self.Value)
	buffer.WriteString("; ")
	buffer.WriteString("Expires:")
	buffer.WriteString(self.Expires.Format(time.RFC1123))
	buffer.WriteString("; ")
	return buffer.String()
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

func getSessionAndUser(r *http.Request, mongoSession *mgo.Session) (session Session, user User, err error) {
	cookie, err := r.Cookie("session")
	if err != nil {
		user.Username = ""
		session.Username = ""
	} else {
		if err != nil {
			panic(err)
		}
		sessionsCollection := mongoSession.DB("accounts").C("sessions")
		err = sessionsCollection.Find(bson.M{"value": cookie.Value}).One(&session)
		username := session.Username
		usersCollection := mongoSession.DB("accounts").C("users")
		err = usersCollection.Find(bson.M{"username": username}).One(&user)
	}
	return session, user, err
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	mongoSession := getMongoSession()
	defer mongoSession.Close()
	session, user, err := getSessionAndUser(r, mongoSession)

	if err != nil {
		http.Redirect(w, r, "/signin", http.StatusFound)
	} else {
		appsCollection := mongoSession.DB("control").C("apps")
		var apps []interface{}
		err = appsCollection.Find(bson.M{}).All(&apps)
		info := struct {
			Apps    []interface{}
			Session Session
		}{
			apps,
			session,
		}

		fmt.Println(user.Username)
		t, _ := template.ParseFiles("templates/layout.html", "templates/home.html", "templates/topbar.html")
		title, err := template.New("title").Parse("Home")
		if err != nil {
			fmt.Println("There was an error", err)
		}
		x, _ := t.AddParseTree("title", title.Tree)
		err2 := x.ExecuteTemplate(w, "layout", info)
		if err2 != nil {
			fmt.Println("There was an error", err2)
		}
	}
}

func myNotFoundHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "Hello!")
}

var port = flag.Uint("p", 8080, "the port to use for serving HTTP requests")

func main() {
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/home", homeHandler)
	http.HandleFunc("/home/", homeHandler)

	flag.Parse()
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", *port), nil))
}
