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

func (self Session) String() string {
	var buffer bytes.Buffer
	buffer.WriteString(self.Value)
	buffer.WriteString("; ")
	buffer.WriteString("Expires:")
	buffer.WriteString(self.Expires.Format(time.RFC1123))
	buffer.WriteString("; ")
	return buffer.String()
}

func signinHandler(w http.ResponseWriter, r *http.Request) {
	mongoSession := getMongoSession()
	defer mongoSession.Close()

	if r.Method == "GET" {
		t, _ := template.ParseFiles("templates/layout.html", "templates/signin.html")
		title, err := template.New("title").Parse("Signin")
		if err != nil {
			fmt.Println("There was an error", err)
		}
		x, _ := t.AddParseTree("title", title.Tree)
		p := Signin{Message: "Hello!", Username: "", Password: ""}
		err2 := x.ExecuteTemplate(w, "layout", p)
		if err2 != nil {
			fmt.Println("There was an error", err2)
		}
	} else if r.Method == "POST" {
		r.ParseForm()
		dumpRequest(r)

		username := r.Form["username"][0]
		password := r.Form["password"][0]

		usersCollection := mongoSession.DB("accounts").C("users")
		sessionsCollection := mongoSession.DB("accounts").C("sessions")

		var user User
		usersCollection.Find(bson.M{"username": username}).One(&user)

		saltedPassword := md5HashWithSalt(password, "agent.io")

		if saltedPassword == user.Password {
			expires := time.Now().Add(3600 * 1000 * 1000 * 1000)
			var cookie http.Cookie
			cookie.Name = "session"
			cookie.Value = uuid.New()
			cookie.Path = "/"
			cookie.Expires = expires
			cookie.RawExpires = expires.Format(time.UnixDate)
			http.SetCookie(w, &cookie)

			var session Session
			session.Value = cookie.Value
			session.Expires = cookie.Expires
			session.Username = user.Username
			sessionsCollection.Insert(&session)

			http.Redirect(w, r, "/home", http.StatusFound)
		} else {
			t, _ := template.ParseFiles("templates/layout.html", "templates/signin.html")
			title, err := template.New("title").Parse("Try Again")
			if err != nil {
				fmt.Println("There was an error", err)
			}
			x, _ := t.AddParseTree("title", title.Tree)
			p := Signin{Message: "Please try again", Username: username, Password: ""}
			err2 := x.ExecuteTemplate(w, "layout", p)
			if err2 != nil {
				fmt.Println("There was an error", err2)
			}
		}
	}
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

func signoutHandler(w http.ResponseWriter, r *http.Request) {
	mongoSession := getMongoSession()
	defer mongoSession.Close()
	session, user, err := getSessionAndUser(r, mongoSession)
	if err != nil {
		fmt.Fprint(w, "No User")
	} else {
		fmt.Println(user.Username)
		fmt.Println(session.Username)
		sessionsCollection := mongoSession.DB("accounts").C("sessions")
		sessionsCollection.Remove(bson.M{"value": session.Value})
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

var port = flag.Uint("p", 8080, "the port to use for serving HTTP requests")

func main() {
	http.HandleFunc("/signin", signinHandler)
	http.HandleFunc("/signin/", signinHandler)
	http.HandleFunc("/signout", signoutHandler)
	http.HandleFunc("/signout/", signoutHandler)

	flag.Parse()
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", *port), nil))
}
